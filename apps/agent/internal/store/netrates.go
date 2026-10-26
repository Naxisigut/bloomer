package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"bloomer/agent/internal/netrates"
)

func (s *Store) LoadNetRateBaseline(ctx context.Context) (netrates.Snapshot, bool, error) {
	var atUs int64
	err := s.reader.QueryRowContext(ctx, "SELECT snapshot_at_us FROM network_rate_state WHERE id=1").Scan(&atUs)
	if errors.Is(err, sql.ErrNoRows) {
		return netrates.Snapshot{}, false, nil
	}
	if err != nil {
		return netrates.Snapshot{}, false, err
	}
	rows, err := s.reader.QueryContext(ctx, "SELECT provider_id,model,requests,tokens FROM network_rate_baseline ORDER BY provider_id")
	if err != nil {
		return netrates.Snapshot{}, false, err
	}
	defer rows.Close()
	snap := netrates.Snapshot{At: time.UnixMicro(atUs).UTC()}
	for rows.Next() {
		var p netrates.ProviderSnap
		if err = rows.Scan(&p.ID, &p.Model, &p.Requests, &p.Tokens); err != nil {
			return netrates.Snapshot{}, false, err
		}
		snap.Providers = append(snap.Providers, p)
	}
	return snap, true, rows.Err()
}

func (s *Store) SaveNetRateRound(ctx context.Context, snap netrates.Snapshot, intervals []netrates.Interval, retainAfter time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM network_rate_baseline"); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, "INSERT INTO network_rate_baseline(provider_id,model,requests,tokens) VALUES(?,?,?,?)")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, p := range snap.Providers {
			if _, err = stmt.ExecContext(ctx, p.ID, p.Model, p.Requests, p.Tokens); err != nil {
				return err
			}
		}
		atUs := snap.At.UTC().UnixMicro()
		res, err := tx.ExecContext(ctx, "UPDATE network_rate_state SET snapshot_at_us=? WHERE id=1", atUs)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			if _, err = tx.ExecContext(ctx, "INSERT INTO network_rate_state(id,snapshot_at_us) VALUES(1,?)", atUs); err != nil {
				return err
			}
		}
		ins, err := tx.PrepareContext(ctx, `INSERT INTO network_rate_intervals(started_at_us,ended_at_us,model,scope,providers,busy_providers,delta_requests,delta_tokens,max_provider_tokens)
VALUES(?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer ins.Close()
		for _, iv := range intervals {
			if _, err = ins.ExecContext(ctx, iv.StartedAt.UTC().UnixMicro(), iv.EndedAt.UTC().UnixMicro(), iv.Model, iv.Scope, iv.Providers, iv.BusyProviders, iv.DeltaRequests, iv.DeltaTokens, iv.MaxProviderTokens); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM network_rate_intervals WHERE ended_at_us<?", retainAfter.UTC().UnixMicro())
		return err
	})
}

func (s *Store) NetRateIntervals(ctx context.Context, since time.Time) ([]netrates.Interval, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT started_at_us,ended_at_us,model,scope,providers,busy_providers,delta_requests,delta_tokens,max_provider_tokens
FROM network_rate_intervals WHERE ended_at_us>=? ORDER BY ended_at_us ASC, model ASC, scope ASC`, since.UTC().UnixMicro())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []netrates.Interval
	for rows.Next() {
		var iv netrates.Interval
		var started, ended int64
		if err = rows.Scan(&started, &ended, &iv.Model, &iv.Scope, &iv.Providers, &iv.BusyProviders, &iv.DeltaRequests, &iv.DeltaTokens, &iv.MaxProviderTokens); err != nil {
			return nil, err
		}
		iv.StartedAt = time.UnixMicro(started).UTC()
		iv.EndedAt = time.UnixMicro(ended).UTC()
		out = append(out, iv)
	}
	return out, rows.Err()
}

// ModelTokenTotals sums the account's billed prompt and completion tokens per model since a time.
func (s *Store) ModelTokenTotals(ctx context.Context, accountID string, since time.Time) (map[string]netrates.TokenTotals, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT model,count(*),coalesce(sum(prompt_tokens),0),coalesce(sum(completion_tokens),0)
FROM earnings WHERE account_id=? AND created_at_us>=? AND completion_tokens>0 GROUP BY model`, accountID, since.UTC().UnixMicro())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]netrates.TokenTotals{}
	for rows.Next() {
		var model string
		var t netrates.TokenTotals
		if err = rows.Scan(&model, &t.Records, &t.Prompt, &t.Completion); err != nil {
			return nil, err
		}
		out[model] = t
	}
	return out, rows.Err()
}

func (s *Store) InsertNetRateSwitch(ctx context.Context, r netrates.SwitchRecord) (int64, error) {
	body, err := marshalSwitchSummary(r.Summary)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO network_rate_switches(at_us,from_model,to_model,reason,status,error,job_id,summary_json)
VALUES(?,?,?,?,?,?,?,?)`, r.At.UTC().UnixMicro(), r.From, r.To, r.Reason, r.Status, nullString(r.Error), nullString(r.JobID), body)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM network_rate_switches WHERE id NOT IN (
SELECT id FROM network_rate_switches ORDER BY at_us DESC, id DESC LIMIT 200)`)
		return err
	})
	return id, err
}

func (s *Store) UpdateNetRateSwitch(ctx context.Context, id int64, status, errCode string) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE network_rate_switches SET status=?, error=? WHERE id=?", status, nullString(errCode), id)
		return err
	})
}

func (s *Store) UpdateNetRateSwitchJob(ctx context.Context, id int64, jobID string) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE network_rate_switches SET job_id=? WHERE id=?", nullString(jobID), id)
		return err
	})
}

func (s *Store) NetRateSwitches(ctx context.Context, limit int) ([]netrates.SwitchRecord, error) {
	if limit < 1 {
		limit = 1
	}
	rows, err := s.reader.QueryContext(ctx, `SELECT id,at_us,from_model,to_model,reason,status,error,job_id,summary_json
FROM network_rate_switches ORDER BY at_us DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []netrates.SwitchRecord
	for rows.Next() {
		var r netrates.SwitchRecord
		var atUs int64
		var errCode, jobID sql.NullString
		var summaryJSON string
		if err = rows.Scan(&r.ID, &atUs, &r.From, &r.To, &r.Reason, &r.Status, &errCode, &jobID, &summaryJSON); err != nil {
			return nil, err
		}
		r.At = time.UnixMicro(atUs).UTC()
		r.Error = errCode.String
		r.JobID = jobID.String
		if err = json.Unmarshal([]byte(summaryJSON), &r.Summary); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

const maxSwitchSummaryBytes = 4096

func marshalSwitchSummary(items []netrates.SummaryItem) (string, error) {
	trimmed := append([]netrates.SummaryItem(nil), items...)
	for {
		body, err := json.Marshal(trimmed)
		if err != nil {
			return "", err
		}
		if len(body) <= maxSwitchSummaryBytes || len(trimmed) == 0 {
			return string(body), nil
		}
		trimmed = trimmed[:len(trimmed)-1]
	}
}
