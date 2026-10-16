package service

import (
	"context"
	"time"

	"bloomer/agent/internal/collector"
	"bloomer/agent/internal/config"
	"bloomer/agent/internal/retention"
)

type Clock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}
type WallClock struct{}

func (WallClock) Now() time.Time { return time.Now().UTC() }
func (WallClock) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type roundWorker interface {
	RunOnce(context.Context) (collector.Result, error)
}
type cleanupWorker interface {
	Step(context.Context) (retention.Result, error)
}

type Scheduler struct {
	Collector roundWorker
	Cleaner   cleanupWorker
	Clock     Clock
}

// Run owns both jobs. At most one task is active; every batch yields to a due
// collection. A one-second wall-clock check catches sleep and clock changes even
// on systems whose monotonic timers stop during suspend. Missed ticks never queue.
func (s Scheduler) Run(ctx context.Context) {
	nextRound := s.Clock.Now().UTC()
	nextCleanup := nextRound
	observed := nextRound
	for ctx.Err() == nil {
		now := s.Clock.Now().UTC()
		if now.Before(observed) {
			nextRound = now
			nextCleanup = now
		}
		observed = now
		if !now.Before(nextRound) {
			result, _ := s.Collector.RunOnce(ctx)
			if ctx.Err() != nil {
				return
			}
			ended := s.Clock.Now().UTC()
			nextRound = now.Add(config.CollectionInterval)
			if !nextRound.After(ended) {
				nextRound = ended.Add(config.CollectionInterval)
			}
			if result.RetryUntil.After(nextRound) {
				nextRound = result.RetryUntil
			}
			continue
		}
		if !now.Before(nextCleanup) {
			result, _ := s.Cleaner.Step(ctx)
			if ctx.Err() != nil {
				return
			}
			nextCleanup = result.NextAt
			if !result.More && !nextCleanup.After(now) {
				nextCleanup = now.Add(config.CollectionInterval)
			}
			continue
		}
		deadline := nextRound
		if nextCleanup.Before(deadline) {
			deadline = nextCleanup
		}
		wait := deadline.Sub(now)
		if wait > time.Second {
			wait = time.Second
		}
		if err := s.Clock.Wait(ctx, wait); err != nil {
			return
		}
	}
}
