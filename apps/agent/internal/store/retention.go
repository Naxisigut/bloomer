package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"bloomer/agent/internal/config"
)

// StartCleanup freezes the cutoff durably so a restart resumes one task rather
// than replaying each missed week. A clock rollback can only narrow deletion.
func (s *Store) StartCleanup(ctx context.Context, at, cutoff time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE maintenance_state SET cleanup_cutoff_us=?,last_cleanup_attempt_at_us=?,cleanup_error_code=NULL WHERE id=1`, cutoff.UnixMicro(), at.UnixMicro())
		return err
	})
}
func (s *Store) RecordCleanupFailure(ctx context.Context, at time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE maintenance_state SET last_cleanup_attempt_at_us=?,cleanup_error_code='cleanup_failed' WHERE id=1`, at.UnixMicro())
		return err
	})
}

// CleanupBatch commits at most limit deletions. Only the final batch advances
// the successful maintenance checkpoint; prior committed batches survive errors.
func (s *Store) CleanupBatch(ctx context.Context, cutoff, completed time.Time, limit int) (bool, error) {
	if limit < 1 || limit > 1000 {
		return false, errors.New("invalid cleanup batch size")
	}
	done := false
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM earnings WHERE (account_id,record_id) IN
   (SELECT account_id,record_id FROM earnings WHERE created_at_us<? ORDER BY created_at_us LIMIT ?)`, cutoff.UnixMicro(), limit)
		if err != nil {
			return err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if count == int64(limit) {
			return nil
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM coverage_gaps WHERE interruption_end_us<?", cutoff.UnixMicro()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE coverage_gaps SET interruption_start_us=? WHERE interruption_start_us<?", cutoff.UnixMicro(), cutoff.UnixMicro()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sync_state SET
   coverage_start_us=(SELECT min(created_at_us) FROM earnings WHERE earnings.account_id=sync_state.account_id AND created_at_us>=?),
   coverage_status=CASE WHEN EXISTS(SELECT 1 FROM coverage_gaps WHERE coverage_gaps.account_id=sync_state.account_id) THEN 'possible_gap'
    WHEN coverage_status='uninitialized' THEN 'uninitialized' ELSE 'partial' END`, cutoff.UnixMicro()); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE maintenance_state SET last_cleanup_success_at_us=?,cleanup_cutoff_us=NULL,cleanup_error_code=NULL WHERE id=1`, completed.UnixMicro())
		if err == nil {
			done = true
		}
		return err
	})
	return done, err
}

type QueryRange struct {
	RetentionStart, From, To time.Time
	Truncated                bool
}

func RetainedRange(now, from, to time.Time) (QueryRange, error) {
	now = now.UTC().Truncate(time.Microsecond)
	cutoff := now.Add(-config.Retention)
	if to.IsZero() {
		to = now
	}
	if from.IsZero() {
		from = cutoff
	}
	if !from.Before(to) {
		return QueryRange{}, errors.New("from must be earlier than to")
	}
	r := QueryRange{RetentionStart: cutoff, From: from.UTC(), To: to.UTC()}
	if r.From.Before(cutoff) {
		r.From = cutoff
		r.Truncated = true
	}
	return r, nil
}

// RetainedEarnings is a streaming query foundation for P4. It always enforces
// the current 365-day boundary, independently of the physical cleanup schedule.
func (s *Store) RetainedEarnings(ctx context.Context, account string, now, from, to time.Time) (*sql.Rows, QueryRange, error) {
	window, err := RetainedRange(now, from, to)
	if err != nil {
		return nil, window, err
	}
	rows, err := s.reader.QueryContext(ctx, `SELECT record_id,provider_id,provider_key,job_id,model,amount_micro_usd,prompt_tokens,completion_tokens,created_at_us
  FROM earnings WHERE account_id=? AND created_at_us>=? AND created_at_us<? ORDER BY created_at_us DESC,record_id DESC`, account, window.From.UnixMicro(), window.To.UnixMicro())
	return rows, window, err
}
func (s *Store) RetainedTotals(ctx context.Context, account string, now, from, to time.Time) (count, amount int64, window QueryRange, err error) {
	window, err = RetainedRange(now, from, to)
	if err != nil {
		return
	}
	err = s.reader.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(amount_micro_usd),0) FROM earnings WHERE account_id=? AND created_at_us>=? AND created_at_us<?`, account, window.From.UnixMicro(), window.To.UnixMicro()).Scan(&count, &amount)
	return
}

type VisibleCoverage struct {
	Status   string
	Start    time.Time
	GapCount int64
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) Coverage(ctx context.Context, state State, now time.Time) (VisibleCoverage, error) {
	return readCoverage(ctx, s.reader, state, now)
}

// LoadHealth reads freshness and retained coverage from the same SQLite snapshot.
func (s *Store) LoadHealth(ctx context.Context, account string, now time.Time) (State, VisibleCoverage, error) {
	tx, err := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return State{}, VisibleCoverage{}, err
	}
	defer tx.Rollback()
	state, err := readState(ctx, tx, account)
	if err != nil {
		return state, VisibleCoverage{}, err
	}
	coverage, err := readCoverage(ctx, tx, state, now)
	if err != nil {
		return state, coverage, err
	}
	return state, coverage, tx.Commit()
}
func readCoverage(ctx context.Context, q rowQuerier, state State, now time.Time) (VisibleCoverage, error) {
	out := VisibleCoverage{Status: state.CoverageStatus}
	if state.AccountID == "" {
		return out, nil
	}
	var start sql.NullInt64
	cutoff := now.UTC().Truncate(time.Microsecond).Add(-config.Retention).UnixMicro()
	err := q.QueryRowContext(ctx, `SELECT (SELECT min(created_at_us) FROM earnings WHERE account_id=? AND created_at_us>=?),
  (SELECT count(*) FROM coverage_gaps WHERE account_id=? AND interruption_end_us>=?)`, state.AccountID, cutoff, state.AccountID, cutoff).Scan(&start, &out.GapCount)
	if err != nil {
		return out, err
	}
	out.Start = timestamp(start)
	if out.GapCount > 0 {
		out.Status = "possible_gap"
	} else if out.Status != "uninitialized" {
		out.Status = "partial"
	}
	return out, nil
}
