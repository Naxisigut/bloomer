// Package collector implements one bounded, atomic collection round.
package collector

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/health"
	"bloomer/agent/internal/store"
	"bloomer/agent/internal/upstream"
)

type Source interface {
	Fetch(context.Context, int, string) (upstream.Snapshot, error)
}
type Collector struct {
	Store     *store.Store
	Source    Source
	AccountID string
	Now       func() time.Time
	Runtime   *health.Runtime
	Logger    *slog.Logger
}
type Result struct {
	Mode, Reason string
	Requests     []int
	New, Updated int
	Skipped      bool
	RetryUntil   time.Time
}

// Plan compares persisted wall-clock instants, including a high-water mark from
// failed attempts. A successful recovery establishes a new clock baseline.
// Shutdown time never hides failures preceding that shutdown.
func Plan(state store.State, now time.Time) (mode, reason string) {
	now = now.UTC()
	if state.LastSuccess.After(now) || state.ClockHighWater.After(now) || state.LastAttempt.After(now) || (state.LastShutdown.After(now) && (state.LastStarted.IsZero() || state.LastSuccess.Before(state.LastStarted))) {
		return "recovery", "clock_anomaly"
	}
	if state.LastSuccess.IsZero() {
		if state.HistoryInitialized {
			return "recovery", "checkpoint_missing"
		}
		return "initial", "first_import"
	}
	if now.Sub(state.LastSuccess) >= config.RecoveryThreshold {
		return "recovery", "long_interruption"
	}
	return "normal", ""
}

func (c *Collector) RunOnce(parent context.Context) (Result, error) {
	result := Result{}
	account := c.AccountID
	ctx, cancel := context.WithTimeout(parent, config.RoundTimeout)
	defer cancel()
	failureCode := "database_unavailable"
	err := c.Store.Coordinate(ctx, func() error {
		state, err := c.Store.LoadState(ctx, c.AccountID)
		if err != nil {
			return err
		}
		account = state.AccountID
		started := c.Now().UTC().Truncate(time.Microsecond)
		if state.RetryNotBefore.After(started) {
			result.Skipped = true
			result.RetryUntil = state.RetryNotBefore
			c.Runtime.Collection("rate_limited", state.LastError, state.RecoveryReason)
			return nil
		}
		result.Mode, result.Reason = Plan(state, started)
		if err = c.Store.RecordAttempt(ctx, account, started, result.Mode, result.Reason); err != nil {
			return err
		}
		c.Runtime.Collection("collecting", state.LastError, result.Reason)
		cutoff := started.Add(-config.Retention)
		windows := config.Windows()
		if result.Mode != "normal" {
			windows = []int{1000}
		}
		merged := map[string]upstream.Earning{}
		overlap := false
		saturated := false
		var summary upstream.Summary
		for _, limit := range windows {
			result.Requests = append(result.Requests, limit)
			snapshot, err := c.Source.Fetch(ctx, limit, account)
			if err != nil {
				failureCode = "network_failed"
				return err
			}
			if account == "" {
				account = snapshot.Summary.AccountID
			}
			if account == "" || snapshot.Summary.AccountID != account {
				failureCode = "account_mismatch"
				return store.ErrAccountMismatch
			}
			summary = snapshot.Summary
			// The production decoder has already folded duplicates. Keep this invariant
			// at the interface boundary as well for alternative sources and tests.
			unique := map[string]upstream.Earning{}
			for _, r := range snapshot.Earnings {
				if r.AccountID != account {
					failureCode = "account_mismatch"
					return store.ErrAccountMismatch
				}
				if previous, ok := unique[r.RecordID]; ok && previous != r {
					failureCode = "invalid_response"
					return &upstream.Error{Code: failureCode}
				}
				unique[r.RecordID] = r
			}
			records := make([]upstream.Earning, 0, len(unique))
			for _, r := range unique {
				if r.CreatedAt.Before(cutoff) {
					delete(merged, r.RecordID)
					continue
				}
				records = append(records, r)
			}
			known, err := c.Store.ExistingIDs(ctx, account, records)
			if err != nil {
				return err
			}
			added := 0
			for _, r := range records {
				if known[r.RecordID] {
					overlap = true
				} else {
					added++
				}
				merged[r.RecordID] = r
			}
			saturated = limit == 1000 && added == limit
			if added != limit {
				break
			}
		}
		completed := c.Now().UTC().Truncate(time.Microsecond)
		if completed.Before(started) {
			failureCode = "clock_changed"
			return &upstream.Error{Code: failureCode}
		}
		round := store.Round{AccountID: account, StartedAt: started, CompletedAt: completed, Cutoff: cutoff, Mode: result.Mode, RecoveryReason: result.Reason, Summary: summary}
		for _, r := range merged {
			round.Records = append(round.Records, r)
		}
		sort.Slice(round.Records, func(i, j int) bool { return round.Records[i].RecordID < round.Records[j].RecordID })
		if state.HistoryInitialized && (saturated || (result.Mode == "recovery" && !overlap)) {
			from := state.LastSuccess
			if from.IsZero() {
				from = state.CoverageStart
			}
			if from.IsZero() || from.After(started) {
				from = started
			}
			if from.Before(cutoff) {
				from = cutoff
			}
			reason := "window_saturated"
			if result.Mode == "recovery" && !overlap {
				reason = "recovery_no_overlap"
			}
			if result.Reason == "clock_anomaly" {
				reason = "clock_anomaly"
			}
			round.Gap = &store.Gap{DetectedAt: completed, From: from, To: started, Reason: reason}
		}
		committed, err := c.Store.CommitRound(ctx, round)
		if err != nil {
			failureCode = "database_unavailable"
			return err
		}
		result.New = committed.New
		result.Updated = committed.Updated
		c.Runtime.Collection("idle", "", result.Reason)
		if c.Logger != nil {
			c.Logger.Info("collection completed", "mode", result.Mode, "reason", result.Reason, "requests", len(result.Requests), "new", result.New, "updated", result.Updated, "possible_gap", round.Gap != nil)
		}
		return nil
	})
	if err != nil {
		var sourceError *upstream.Error
		if errors.As(err, &sourceError) {
			failureCode = sourceError.Code
			result.RetryUntil = sourceError.RetryAt
		}
		if errors.Is(err, store.ErrAccountMismatch) {
			failureCode = "account_mismatch"
		}
		if ctx.Err() != nil {
			failureCode = "round_timeout"
		}
		if parent.Err() != nil {
			failureCode = "request_cancelled"
		}
		status := "degraded"
		if failureCode == "rate_limited" {
			status = "rate_limited"
		}
		if parent.Err() == nil {
			save, stop := context.WithTimeout(context.Background(), 5*time.Second)
			persistErr := c.Store.RecordFailure(save, account, failureCode, result.RetryUntil)
			stop()
			if persistErr != nil {
				failureCode = "database_unavailable"
				status = "degraded"
			}
			if c.Logger != nil {
				c.Logger.Error("collection failed", "code", failureCode, "mode", result.Mode)
			}
		}
		c.Runtime.Collection(status, failureCode, result.Reason)
	}
	return result, err
}
