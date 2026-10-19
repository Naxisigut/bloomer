package service

import (
	"context"
	"log/slog"
	"time"

	"bloomer/agent/internal/store"
	"bloomer/agent/internal/telemetry"
)

type deviceSampler interface {
	Hardware(context.Context) telemetry.Hardware
	Collect(context.Context, time.Time) telemetry.Sample
}

// Device collection deliberately bypasses the earnings/cleanup activity lock:
// upstream waits must not erase the local observations that explain them.
// Only the short SQLite commits use Store.Write's existing serialization.
func runDeviceCollection(ctx context.Context, sampler deviceSampler, db *store.Store, clock Clock, logger *slog.Logger) {
	var deviceID string
	for ctx.Err() == nil {
		started := clock.Now().UTC()
		job, cancel := context.WithTimeout(ctx, 25*time.Second)
		if deviceID == "" {
			var err error
			deviceID, err = db.SaveDeviceIdentity(job, sampler.Hardware(job), started)
			if err != nil && ctx.Err() == nil {
				logger.Error("device identity save failed")
			}
		}
		if deviceID != "" && job.Err() == nil {
			sample := sampler.Collect(job, clock.Now())
			sample.DeviceID = deviceID
			if ctx.Err() == nil {
				// A timed-out probe may still produce useful partial observations.
				save, stop := context.WithTimeout(ctx, 5*time.Second)
				if err := db.SaveDeviceSample(save, sample); err != nil && ctx.Err() == nil {
					logger.Error("device sample save failed")
				}
				stop()
			}
		}
		cancel()
		next := started.Add(telemetry.Interval)
		if !next.After(clock.Now()) {
			next = clock.Now().Add(telemetry.Interval)
		}
		// Check the wall clock each second to recover promptly after sleep or
		// a clock adjustment, without issuing a burst of historical samples.
		observed := clock.Now()
		for ctx.Err() == nil && clock.Now().Before(next) {
			now := clock.Now()
			if now.Before(observed) {
				break
			}
			observed = now
			delay := next.Sub(now)
			if delay > time.Second {
				delay = time.Second
			}
			if err := clock.Wait(ctx, delay); err != nil {
				return
			}
		}
	}
}
