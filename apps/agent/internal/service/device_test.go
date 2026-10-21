package service

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"bloomer/agent/internal/store"
	"bloomer/agent/internal/telemetry"
)

type fakeDeviceSampler struct {
	collect func(time.Time) telemetry.Sample
}

func (f fakeDeviceSampler) Hardware(context.Context) telemetry.Hardware {
	return telemetry.Hardware{OS: "darwin"}
}
func (f fakeDeviceSampler) Collect(_ context.Context, at time.Time) telemetry.Sample {
	return f.collect(at)
}

func TestDeviceCollectionContinuesDuringUpstreamActivityWithoutBacklog(t *testing.T) {
	for _, sleep := range []bool{false, true} {
		t.Run(map[bool]string{false: "minute cadence", true: "wake from sleep"}[sleep], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			busy := make(chan struct{})
			release := make(chan struct{})
			released := make(chan struct{})
			go func() {
				defer close(released)
				_ = db.Coordinate(ctx, func() error { close(busy); <-release; return nil })
			}()
			<-busy
			defer func() { close(release); <-released }()
			base := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC)
			clock := &fakeClock{now: base}
			calls := 0
			observed := 0
			sampler := fakeDeviceSampler{collect: func(at time.Time) telemetry.Sample {
				calls++
				if calls == 1 && sleep {
					clock.waitHook = func() { clock.now = clock.now.Add(time.Hour) }
				}
				if calls == 2 {
					clock.waitHook = cancel
				}
				return telemetry.NewSample(at)
			}}
			done := make(chan struct{})
			go func() {
				defer close(done)
				runDeviceCollection(ctx, sampler, db, clock, slog.New(slog.NewTextHandler(io.Discard, nil)), func(check context.Context, sample telemetry.Sample) error {
					stored, err := db.LatestDeviceSample(check, sample.CollectedAt)
					if err != nil || stored == nil || !stored.CollectedAt.Equal(sample.CollectedAt) || stored.DeviceID != sample.DeviceID {
						t.Errorf("observer did not receive committed sample: %+v %v", stored, err)
					}
					observed++
					return nil
				})
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				cancel()
				t.Fatal("telemetry was blocked by upstream activity or did not stop")
			}
			if calls != 2 || observed != 2 {
				t.Fatalf("missed samples were queued: %d", calls)
			}
			page, err := db.DeviceSamples(context.Background(), clock.now, base.Add(-time.Second), clock.now.Add(time.Second), 0, 100)
			if err != nil || len(page.Items) != 2 {
				t.Fatalf("samples not committed: %+v %v", page, err)
			}
			gap := page.Items[0].CollectedAt.Sub(page.Items[1].CollectedAt)
			if !sleep && gap != time.Minute {
				t.Fatalf("unexpected cadence: %v", gap)
			}
			if sleep && gap < time.Hour {
				t.Fatal("sleep was filled with synthetic samples")
			}
		})
	}
}
