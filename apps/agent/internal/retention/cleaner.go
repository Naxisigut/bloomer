// Package retention performs weekly, resumable batches between collection rounds.
package retention

import (
	"context"
	"log/slog"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/health"
	"bloomer/agent/internal/store"
)

type Result struct {
	More   bool
	NextAt time.Time
}
type Cleaner struct {
	Store     *store.Store
	AccountID string
	Now       func() time.Time
	Runtime   *health.Runtime
	Logger    *slog.Logger
}

func (c *Cleaner) Step(ctx context.Context) (Result, error) {
	now := c.Now().UTC().Truncate(time.Microsecond)
	result := Result{NextAt: now.Add(config.CollectionInterval)}
	err := c.Store.Coordinate(ctx, func() error {
		state, err := c.Store.LoadState(ctx, c.AccountID)
		if err != nil {
			return err
		}
		if state.CleanupError != "" && now.Before(state.LastCleanupAttempt.Add(config.CollectionInterval)) {
			result.NextAt = state.LastCleanupAttempt.Add(config.CollectionInterval)
			return nil
		}
		if state.CleanupCutoff.IsZero() && !state.LastCleanup.IsZero() && !state.LastCleanup.After(now) && now.Sub(state.LastCleanup) < config.CleanupInterval {
			result.NextAt = state.LastCleanup.Add(config.CleanupInterval)
			return nil
		}
		cutoff := state.CleanupCutoff
		current := now.Add(-config.Retention)
		if cutoff.IsZero() || cutoff.After(current) {
			cutoff = current
		}
		c.Runtime.Cleanup("running", "")
		if state.CleanupCutoff.IsZero() || cutoff != state.CleanupCutoff || state.CleanupError != "" {
			if err = c.Store.StartCleanup(ctx, now, cutoff); err != nil {
				return err
			}
		}
		done, err := c.Store.CleanupBatch(ctx, cutoff, c.Now().UTC(), 1000)
		if err != nil {
			return err
		}
		result.More = !done
		if done {
			result.NextAt = c.Now().UTC().Add(config.CleanupInterval)
			c.Runtime.Cleanup("idle", "")
			if c.Logger != nil {
				c.Logger.Info("retention cleanup completed")
			}
		} else {
			result.NextAt = c.Now().UTC()
		}
		return nil
	})
	if err != nil {
		c.Runtime.Cleanup("failed", "cleanup_failed")
		// A bounded independent context persists the failure even after a task timeout.
		if ctx.Err() == nil {
			save, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if persistErr := c.Store.RecordCleanupFailure(save, now); persistErr != nil && c.Logger != nil {
				c.Logger.Error("save cleanup failure failed")
			}
		}
		if c.Logger != nil && ctx.Err() == nil {
			c.Logger.Error("retention cleanup failed")
		}
	}
	return result, err
}
