package netrates

import (
	"testing"
	"time"
)

func TestBlockedReasonPriority(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	since := now.Add(-10 * time.Minute)
	min := 30 * time.Minute
	base := switchInput{
		now: now, autoSwitch: true, autoSince: &since, samples: 6, window: 6, minInterval: min,
		currentOK: true, currentModel: "a", downloaded: map[string]bool{"a": true, "gemma-4-26b-qat-4bit": true},
		fallbackModel: "gemma-4-26b-qat-4bit", ratio: 0.2, nearZeroMicro: 1000,
		recommendation: &Recommendation{Model: "b", USDPerHourMicro: 200000},
		candidates: []Candidate{
			{Model: "a", Downloaded: true, USDPerHourMicro: 100000, AvgProviders: 5},
			{Model: "b", Downloaded: true, USDPerHourMicro: 200000, AvgProviders: 5},
		},
	}
	if blockedReason(switchInput{autoSwitch: false}) != "auto_switch_off" {
		t.Fatal("auto_switch_off")
	}
	if blockedReason(switchInput{autoSwitch: true, samples: 3, window: 6}) != "insufficient_samples" {
		t.Fatal("insufficient_samples")
	}
	if blockedReason(switchInput{autoSwitch: true, samples: 6, window: 6, autoSince: &since, now: now, minInterval: min}) != "warming_up" {
		t.Fatal("warming_up")
	}
	last := now.Add(-5 * time.Minute)
	if blockedReason(switchInput{autoSwitch: true, samples: 6, window: 6, autoSince: ptr(now.Add(-2 * time.Hour)), now: now, minInterval: min, lastModelChange: last}) != "min_interval" {
		t.Fatal("min_interval")
	}
	backoff := now.Add(time.Hour)
	if blockedReason(switchInput{autoSwitch: true, samples: 6, window: 6, autoSince: ptr(now.Add(-2 * time.Hour)), now: now, minInterval: min, backoffUntil: &backoff}) != "backoff" {
		t.Fatal("backoff")
	}
	if blockedReason(switchInput{autoSwitch: true, samples: 6, window: 6, autoSince: ptr(now.Add(-2 * time.Hour)), now: now, minInterval: min, controlBusy: true}) != "control_busy" {
		t.Fatal("control_busy")
	}
	if blockedReason(switchInput{autoSwitch: true, samples: 6, window: 6, autoSince: ptr(now.Add(-2 * time.Hour)), now: now, minInterval: min, modelsUnavailable: true}) != "models_unavailable" {
		t.Fatal("models_unavailable")
	}
	if blockedReason(switchInput{autoSwitch: true, samples: 6, window: 6, autoSince: ptr(now.Add(-2 * time.Hour)), now: now, minInterval: min}) != "current_model_unknown" {
		t.Fatal("current_model_unknown")
	}
	in := base
	in.autoSince = ptr(now.Add(-2 * time.Hour))
	in.lastModelChange = now.Add(-2 * time.Hour)
	in.currentModel = "b"
	if blockedReason(in) != "already_optimal" {
		t.Fatal("already_optimal", blockedReason(in))
	}
	in.currentModel = "a"
	in.recommendation = &Recommendation{Model: "b", USDPerHourMicro: 115000}
	if blockedReason(in) != "below_threshold" {
		t.Fatal("below_threshold", blockedReason(in))
	}
	in.recommendation = &Recommendation{Model: "b", USDPerHourMicro: 120000}
	if blockedReason(in) != "" {
		t.Fatal("should switch", blockedReason(in))
	}
}

func TestFallbackGemmaBlocked(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	fallback := "gemma-4-26b-qat-4bit"
	in := switchInput{
		autoSwitch: true, samples: 6, window: 6, now: now, autoSince: ptr(now.Add(-2 * time.Hour)),
		minInterval: 30 * time.Minute, currentOK: true, currentModel: "a", fallbackModel: fallback,
		ratio: 0.2, nearZeroMicro: 1000, downloaded: map[string]bool{"a": true},
		candidates: []Candidate{{Model: "a", Downloaded: true, USDPerHourMicro: 1, AvgProviders: 5, Insufficient: false}},
		recommendation: &Recommendation{Model: fallback, Fallback: true},
	}
	if blockedReason(in) != "fallback_not_downloaded" {
		t.Fatal(blockedReason(in))
	}
}

func ptr(t time.Time) *time.Time { return &t }
