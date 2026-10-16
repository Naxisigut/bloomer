package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"bloomer/agent/internal/collector"
	"bloomer/agent/internal/config"
	"bloomer/agent/internal/retention"
)

type fakeClock struct {
	now      time.Time
	waitHook func()
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Wait(ctx context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	if c.waitHook != nil {
		hook := c.waitHook
		c.waitHook = nil
		hook()
	}
	return ctx.Err()
}

type fakeCollector func(context.Context) (collector.Result, error)

func (f fakeCollector) RunOnce(ctx context.Context) (collector.Result, error) { return f(ctx) }

type fakeCleaner func(context.Context) (retention.Result, error)

func (f fakeCleaner) Step(ctx context.Context) (retention.Result, error) { return f(ctx) }
func TestSchedulerNoBacklogAfterLongRoundAndSleep(t *testing.T) {
	base := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var starts []time.Duration
	cleanup := 0
	scheduler := Scheduler{Clock: clock}
	scheduler.Collector = fakeCollector(func(context.Context) (collector.Result, error) {
		starts = append(starts, clock.now.Sub(base))
		if len(starts) == 1 {
			clock.now = clock.now.Add(80 * time.Second)
		}
		if len(starts) == 2 {
			clock.waitHook = func() { clock.now = clock.now.Add(time.Hour) }
		}
		if len(starts) == 4 {
			cancel()
		}
		return collector.Result{}, nil
	})
	scheduler.Cleaner = fakeCleaner(func(context.Context) (retention.Result, error) {
		cleanup++
		return retention.Result{NextAt: clock.now.Add(config.CleanupInterval)}, nil
	})
	scheduler.Run(ctx)
	if !reflect.DeepEqual(starts, []time.Duration{0, 140 * time.Second, 3741 * time.Second, 3801 * time.Second}) {
		t.Fatalf("missed ticks queued or overlapped: %v", starts)
	}
	if cleanup != 1 {
		t.Fatal("weekly task was replayed")
	}
}
func TestSchedulerThrottleAndCollectionPriorityBetweenBatches(t *testing.T) {
	base := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var starts []time.Duration
	var events []string
	batches := 0
	scheduler := Scheduler{Clock: clock}
	scheduler.Collector = fakeCollector(func(context.Context) (collector.Result, error) {
		starts = append(starts, clock.now.Sub(base))
		events = append(events, "collect")
		if len(starts) == 1 {
			return collector.Result{RetryUntil: base.Add(125 * time.Second)}, nil
		}
		if len(starts) == 3 {
			cancel()
		}
		return collector.Result{}, nil
	})
	scheduler.Cleaner = fakeCleaner(func(context.Context) (retention.Result, error) {
		batches++
		events = append(events, "batch")
		if batches == 1 {
			clock.now = base.Add(125 * time.Second)
			return retention.Result{More: true, NextAt: clock.now}, nil
		}
		return retention.Result{NextAt: clock.now.Add(config.CleanupInterval)}, nil
	})
	scheduler.Run(ctx)
	if !reflect.DeepEqual(starts, []time.Duration{0, 125 * time.Second, 185 * time.Second}) || !reflect.DeepEqual(events[:4], []string{"collect", "batch", "collect", "batch"}) {
		t.Fatalf("rate limit/priority broken: starts=%v events=%v", starts, events)
	}
}
func TestSchedulerClockRollbackReevaluatesRound(t *testing.T) {
	base := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var starts []time.Time
	scheduler := Scheduler{Clock: clock, Collector: fakeCollector(func(context.Context) (collector.Result, error) {
		starts = append(starts, clock.now)
		if len(starts) == 1 {
			clock.waitHook = func() { clock.now = clock.now.Add(-time.Hour) }
		} else {
			cancel()
		}
		return collector.Result{}, nil
	}), Cleaner: fakeCleaner(func(context.Context) (retention.Result, error) {
		return retention.Result{NextAt: clock.now.Add(config.CleanupInterval)}, nil
	})}
	scheduler.Run(ctx)
	if len(starts) != 2 || !starts[1].Before(starts[0]) {
		t.Fatalf("clock rollback waited for obsolete deadline: %v", starts)
	}
}
