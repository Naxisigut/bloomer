package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/upstream"
)

type Gap struct {
	DetectedAt, From, To time.Time
	Reason               string
}
type Round struct {
	AccountID                      string
	StartedAt, CompletedAt, Cutoff time.Time
	Mode, RecoveryReason           string
	Records                        []upstream.Earning
	Summary                        upstream.Summary
	Gap                            *Gap
}
type CommitResult struct{ New, Updated int }

func (s *Store) ExistingIDs(ctx context.Context, account string, records []upstream.Earning) (map[string]bool, error) {
	found := make(map[string]bool, len(records))
	stmt, err := s.reader.PrepareContext(ctx, "SELECT EXISTS(SELECT 1 FROM earnings WHERE account_id=? AND record_id=?)")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	for _, record := range records {
		var exists bool
		if err = stmt.QueryRowContext(ctx, account, record.RecordID).Scan(&exists); err != nil {
			return nil, err
		}
		found[record.RecordID] = exists
	}
	return found, nil
}

func (s *Store) CommitRound(ctx context.Context, round Round) (CommitResult, error) {
	var result CommitResult
	if round.AccountID == "" || round.AccountID != round.Summary.AccountID || round.CompletedAt.Before(round.StartedAt) {
		return result, errors.New("invalid round identity or checkpoint")
	}
	err := s.Write(ctx, func(tx *sql.Tx) error {
		var bound sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT bound_account_id FROM maintenance_state WHERE id=1").Scan(&bound); err != nil {
			return err
		}
		if bound.Valid && bound.String != round.AccountID {
			return ErrAccountMismatch
		}
		if !bound.Valid {
			var foreign bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_summary_current WHERE account_id<>?) OR EXISTS(SELECT 1 FROM sync_state WHERE account_id<>? AND (last_successful_sync_at_us IS NOT NULL OR coverage_status<>'uninitialized')) OR EXISTS(SELECT 1 FROM earnings WHERE account_id<>?)`, round.AccountID, round.AccountID, round.AccountID).Scan(&foreign); err != nil {
				return err
			}
			if foreign {
				return ErrAccountMismatch
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM sync_state WHERE account_id<>? AND last_successful_sync_at_us IS NULL AND coverage_status='uninitialized'", round.AccountID); err != nil {
				return err
			}
		}
		insert, err := tx.PrepareContext(ctx, `INSERT INTO earnings(account_id,record_id,provider_id,provider_key,job_id,model,amount_micro_usd,prompt_tokens,completion_tokens,created_at_us)
   VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,record_id) DO NOTHING`)
		if err != nil {
			return err
		}
		defer insert.Close()
		update, err := tx.PrepareContext(ctx, `UPDATE earnings SET provider_id=?,provider_key=?,job_id=?,model=?,amount_micro_usd=?,prompt_tokens=?,completion_tokens=?,created_at_us=?
   WHERE account_id=? AND record_id=? AND (provider_id IS NOT ? OR provider_key IS NOT ? OR job_id IS NOT ? OR model IS NOT ? OR amount_micro_usd IS NOT ? OR prompt_tokens IS NOT ? OR completion_tokens IS NOT ? OR created_at_us IS NOT ?)`)
		if err != nil {
			return err
		}
		defer update.Close()
		for _, r := range round.Records {
			if r.AccountID != round.AccountID {
				return ErrAccountMismatch
			}
			if r.CreatedAt.Before(round.Cutoff) {
				continue
			}
			// Rewards arrive with a blank Provider. Tagging them keeps the row
			// attributable without inventing a Provider for real task income.
			provider := rewardProvider(r.ProviderID, r.Model)
			res, err := insert.ExecContext(ctx, r.AccountID, r.RecordID, provider, r.ProviderKey, r.JobID, r.Model, r.AmountMicroUSD, r.PromptTokens, r.CompletionTokens, r.CreatedAt.UnixMicro())
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n > 0 {
				result.New++
				continue
			}
			res, err = update.ExecContext(ctx, provider, r.ProviderKey, r.JobID, r.Model, r.AmountMicroUSD, r.PromptTokens, r.CompletionTokens, r.CreatedAt.UnixMicro(), r.AccountID, r.RecordID,
				provider, r.ProviderKey, r.JobID, r.Model, r.AmountMicroUSD, r.PromptTokens, r.CompletionTokens, r.CreatedAt.UnixMicro())
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			if err != nil {
				return err
			}
			result.Updated += int(n)
		}
		summary := round.Summary
		_, err = tx.ExecContext(ctx, `INSERT INTO account_summary_current VALUES(?,?,?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET
   total_record_count=excluded.total_record_count,total_micro_usd=excluded.total_micro_usd,available_balance_micro_usd=excluded.available_balance_micro_usd,
   withdrawable_balance_micro_usd=excluded.withdrawable_balance_micro_usd,observed_at_us=excluded.observed_at_us`, round.AccountID, summary.TotalRecordCount, summary.TotalMicroUSD, summary.AvailableBalanceMicroUSD, summary.WithdrawableBalanceMicroUSD, round.CompletedAt.UnixMicro())
		if err != nil {
			return err
		}
		if round.Gap != nil {
			if err = mergeGap(ctx, tx, round.AccountID, *round.Gap); err != nil {
				return err
			}
		}
		var start sql.NullInt64
		var gap bool
		if err = tx.QueryRowContext(ctx, "SELECT min(created_at_us) FROM earnings WHERE account_id=? AND created_at_us>=?", round.AccountID, round.Cutoff.UnixMicro()).Scan(&start); err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM coverage_gaps WHERE account_id=? AND interruption_end_us>=?)", round.AccountID, round.Cutoff.UnixMicro()).Scan(&gap); err != nil {
			return err
		}
		coverage := "partial"
		if gap {
			coverage = "possible_gap"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO sync_state(account_id,last_attempt_at_us,last_successful_sync_at_us,last_sync_mode,coverage_start_us,coverage_status,last_new_count,last_updated_count)
   VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET last_attempt_at_us=excluded.last_attempt_at_us,last_successful_sync_at_us=excluded.last_successful_sync_at_us,
   last_error_code=NULL,last_sync_mode=excluded.last_sync_mode,coverage_start_us=excluded.coverage_start_us,coverage_status=excluded.coverage_status,last_new_count=excluded.last_new_count,last_updated_count=excluded.last_updated_count`, round.AccountID, round.StartedAt.UnixMicro(), round.CompletedAt.UnixMicro(), round.Mode, start, coverage, result.New, result.Updated)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE maintenance_state SET bound_account_id=?,last_error_code=NULL,last_sync_mode=?,recovery_reason=?,clock_high_water_us=? WHERE id=1`, round.AccountID, round.Mode, round.RecoveryReason, round.CompletedAt.UnixMicro())
		return err
	})
	if err != nil {
		return CommitResult{}, err
	}
	return result, nil
}

func mergeGap(ctx context.Context, tx *sql.Tx, account string, gap Gap) error {
	if gap.From.After(gap.To) {
		return errors.New("invalid gap interval")
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,discovered_at_us,interruption_start_us,interruption_end_us,reason FROM coverage_gaps
  WHERE account_id=? AND interruption_start_us<=? AND interruption_end_us>=?`, account, gap.To.Add(config.CollectionInterval).UnixMicro(), gap.From.Add(-config.CollectionInterval).UnixMicro())
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id, detected, from, to int64
		var reason string
		if err = rows.Scan(&id, &detected, &from, &to, &reason); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
		if from < gap.From.UnixMicro() {
			gap.From = time.UnixMicro(from).UTC()
		}
		if to > gap.To.UnixMicro() {
			gap.To = time.UnixMicro(to).UTC()
		}
		if detected < gap.DetectedAt.UnixMicro() {
			gap.DetectedAt = time.UnixMicro(detected).UTC()
		}
		if reason != gap.Reason {
			gap.Reason = "multiple_interruptions"
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, "DELETE FROM coverage_gaps WHERE id=?", id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO coverage_gaps(account_id,discovered_at_us,interruption_start_us,interruption_end_us,reason) VALUES(?,?,?,?,?)`, account, gap.DetectedAt.UnixMicro(), gap.From.UnixMicro(), gap.To.UnixMicro(), gap.Reason)
	return err
}
