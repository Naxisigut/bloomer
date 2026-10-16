package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrAccountMismatch = errors.New("configured or upstream account differs from the bound account")

type State struct {
	AccountID                                                           string
	LastAttempt, LastSuccess, LastShutdown, LastCleanup, RetryNotBefore time.Time
	LastStarted                                                         time.Time
	ClockHighWater, CleanupCutoff, LastCleanupAttempt, CoverageStart    time.Time
	LastError, LastMode, RecoveryReason, CleanupError, CoverageStatus   string
	HistoryInitialized                                                  bool
}

func timestamp(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.UnixMicro(n.Int64).UTC()
}
func nullableTime(at time.Time) any {
	if at.IsZero() {
		return nil
	}
	return at.UTC().UnixMicro()
}

// LoadState takes one short local snapshot. It never leaves a transaction open
// during upstream I/O. Legacy single-account databases are inferred until bound.
func (s *Store) LoadState(ctx context.Context, configured string) (State, error) {
	tx, err := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback()
	state, err := readState(ctx, tx, configured)
	if err != nil {
		return state, err
	}
	return state, tx.Commit()
}

func readState(ctx context.Context, tx *sql.Tx, configured string) (State, error) {
	var state State
	state.CoverageStatus = "uninitialized"
	var err error
	var account, code, mode, reason, cleanupError sql.NullString
	var attempt, success, shutdown, cleanup, retry, high, cutoff, cleanupAttempt, start, started sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT bound_account_id,last_attempt_at_us,last_clean_shutdown_at_us,last_cleanup_success_at_us,
 retry_not_before_us,clock_high_water_us,cleanup_cutoff_us,last_cleanup_attempt_at_us,last_error_code,last_sync_mode,recovery_reason,cleanup_error_code,
 last_started_at_us FROM maintenance_state WHERE id=1`).Scan(&account, &attempt, &shutdown, &cleanup, &retry, &high, &cutoff, &cleanupAttempt, &code, &mode, &reason, &cleanupError, &started)
	if err != nil {
		return state, err
	}
	state.AccountID = account.String
	if state.AccountID == "" {
		rows, err := tx.QueryContext(ctx, `SELECT account_id FROM (SELECT account_id FROM account_summary_current UNION SELECT account_id FROM sync_state WHERE last_successful_sync_at_us IS NOT NULL OR coverage_status<>'uninitialized' UNION SELECT account_id FROM earnings) LIMIT 2`)
		if err != nil {
			return state, err
		}
		var found []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return state, err
			}
			found = append(found, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return state, err
		}
		if len(found) > 1 {
			return state, ErrAccountMismatch
		}
		if len(found) == 1 {
			state.AccountID = found[0]
		}
	}
	if configured != "" {
		if state.AccountID != "" && configured != state.AccountID {
			return state, ErrAccountMismatch
		}
		state.AccountID = configured
	}
	state.LastAttempt = timestamp(attempt)
	state.LastStarted = timestamp(started)
	state.LastShutdown = timestamp(shutdown)
	state.LastCleanup = timestamp(cleanup)
	state.RetryNotBefore = timestamp(retry)
	state.ClockHighWater = timestamp(high)
	state.CleanupCutoff = timestamp(cutoff)
	state.LastCleanupAttempt = timestamp(cleanupAttempt)
	state.LastError = code.String
	state.LastMode = mode.String
	state.RecoveryReason = reason.String
	state.CleanupError = cleanupError.String
	if state.AccountID != "" {
		err = tx.QueryRowContext(ctx, "SELECT last_successful_sync_at_us,coverage_start_us,coverage_status FROM sync_state WHERE account_id=?", state.AccountID).Scan(&success, &start, &state.CoverageStatus)
		if err != nil && err != sql.ErrNoRows {
			return state, err
		}
		state.LastSuccess = timestamp(success)
		state.CoverageStart = timestamp(start)
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_summary_current WHERE account_id=?) OR EXISTS(SELECT 1 FROM earnings WHERE account_id=?)`, state.AccountID, state.AccountID).Scan(&state.HistoryInitialized)
		if err != nil {
			return state, err
		}
		state.HistoryInitialized = state.HistoryInitialized || !state.LastSuccess.IsZero() || state.CoverageStatus != "uninitialized"
	}
	return state, nil
}

func (s *Store) RecordAttempt(ctx context.Context, account string, at time.Time, mode, reason string) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE maintenance_state SET last_attempt_at_us=?,last_sync_mode=?,recovery_reason=?,
  clock_high_water_us=CASE WHEN clock_high_water_us IS NULL OR clock_high_water_us<? THEN ? ELSE clock_high_water_us END WHERE id=1`, at.UnixMicro(), mode, reason, at.UnixMicro(), at.UnixMicro())
		if err != nil {
			return err
		}
		if account != "" {
			_, err = tx.ExecContext(ctx, `INSERT INTO sync_state(account_id,last_attempt_at_us,last_sync_mode) VALUES(?,?,?)
  ON CONFLICT(account_id) DO UPDATE SET last_attempt_at_us=excluded.last_attempt_at_us,last_sync_mode=excluded.last_sync_mode`, account, at.UnixMicro(), mode)
		}
		return err
	})
}

func (s *Store) RecordFailure(ctx context.Context, account, code string, retryAt time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE maintenance_state SET last_error_code=?,
  retry_not_before_us=CASE WHEN ? IS NOT NULL AND (retry_not_before_us IS NULL OR retry_not_before_us<?) THEN ? ELSE retry_not_before_us END WHERE id=1`, code, nullableTime(retryAt), nullableTime(retryAt), nullableTime(retryAt))
		if err != nil {
			return err
		}
		if account != "" {
			_, err = tx.ExecContext(ctx, "UPDATE sync_state SET last_error_code=? WHERE account_id=?", code, account)
		}
		return err
	})
}
