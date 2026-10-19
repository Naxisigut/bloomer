package telemetry

import (
	"context"
	"time"
)

type EarningsObservation struct {
	LastEarningAt *time.Time
	LastSyncAt    time.Time
	ProviderID    string
}

// Activity is a local observation combined with this device's earnings only.
// Never substitute account-wide earnings, missing flags, or an old sync.
func (p *Sampler) activity(ctx context.Context, s *Sample) {
	fail := func(code string) { s.ActivityError = Ptr(code); s.Quality["is_idle"] = Unavailable }
	if s.ProviderRunning != nil && !*s.ProviderRunning {
		fail("provider_not_running")
		return
	}
	if s.Quality["inference_active"] != OK || s.Quality["request_work_pending"] != OK {
		fail("task_state_unavailable")
		return
	}
	// Work is sufficient evidence of being busy even when earnings are unavailable.
	if *s.InferenceActive || *s.RequestWorkPending {
		s.IsIdle = Ptr(false)
		s.Quality["is_idle"] = OK
	}
	if s.providerPublicKey == "" {
		if s.IsIdle == nil {
			fail("device_identity_unavailable")
		}
		return
	}
	if p.LatestEarnings == nil {
		if s.IsIdle == nil {
			fail("device_earnings_unavailable")
		}
		return
	}
	observation, err := p.LatestEarnings(ctx, s.providerPublicKey, s.CollectedAt)
	if err != nil {
		if s.IsIdle == nil {
			fail("device_earnings_unavailable")
		}
		return
	}
	if observation.ProviderID != "" {
		s.EarningsProviderID = Ptr(observation.ProviderID)
	}
	s.LastDeviceEarningAt = observation.LastEarningAt
	if !observation.LastSyncAt.IsZero() {
		s.LastEarningsSyncAt = Ptr(observation.LastSyncAt)
	}
	if s.IsIdle != nil {
		return
	}
	age := s.CollectedAt.Sub(observation.LastSyncAt)
	if observation.LastSyncAt.IsZero() || age < 0 || age > 120*time.Second {
		fail("earnings_stale")
		return
	}
	if observation.LastEarningAt == nil {
		fail("no_device_earnings")
		return
	}
	age = s.CollectedAt.Sub(*observation.LastEarningAt)
	if age < 0 {
		fail("earnings_time_invalid")
		return
	}
	s.IsIdle = Ptr(age > 5*time.Minute)
	s.Quality["is_idle"] = OK
}
