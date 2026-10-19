package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestActivityFiveMinuteBoundaryAndUnknownSources(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name            string
		active, pending bool
		age             time.Duration
		syncAge         time.Duration
		missing         bool
		want            *bool
		code            string
	}{
		{"busy", true, false, 10 * time.Minute, 0, false, Ptr(false), ""},
		{"queued", false, true, 10 * time.Minute, 0, false, Ptr(false), ""},
		{"exactly five minutes", false, false, 5 * time.Minute, 0, false, Ptr(false), ""},
		{"over five minutes", false, false, 5*time.Minute + time.Microsecond, 0, false, Ptr(true), ""},
		{"recent earnings", false, false, time.Minute, 0, false, Ptr(false), ""},
		{"stale sync", false, false, 10 * time.Minute, 121 * time.Second, false, nil, "earnings_stale"},
		{"future sync", false, false, 10 * time.Minute, -time.Second, false, nil, "earnings_stale"},
		{"future earnings", false, false, -time.Second, 0, false, nil, "earnings_time_invalid"},
		{"no earnings", false, false, 0, 0, true, nil, "no_device_earnings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSample(now)
			s.InferenceActive = Ptr(tc.active)
			s.RequestWorkPending = Ptr(tc.pending)
			s.providerPublicKey = "local-secret-public-key"
			s.Quality["inference_active"], s.Quality["request_work_pending"] = OK, OK
			p := &Sampler{LatestEarnings: func(_ context.Context, key string, _ time.Time) (EarningsObservation, error) {
				if key != s.providerPublicKey {
					t.Fatal("identity lost")
				}
				o := EarningsObservation{LastSyncAt: now.Add(-tc.syncAge), ProviderID: "own"}
				if !tc.missing {
					o.LastEarningAt = Ptr(now.Add(-tc.age))
				}
				return o, nil
			}}
			p.activity(context.Background(), &s)
			if (s.IsIdle == nil) != (tc.want == nil) || (s.IsIdle != nil && *s.IsIdle != *tc.want) {
				t.Fatalf("wrong idle: %+v", s)
			}
			if tc.code != "" && (s.ActivityError == nil || *s.ActivityError != tc.code) {
				t.Fatalf("wrong reason: %+v", s)
			}
			body, _ := json.Marshal(s)
			if strings.Contains(string(body), "local-secret-public-key") {
				t.Fatal("identity leaked")
			}
		})
	}
	s := NewSample(now)
	s.InferenceActive = Ptr(false)
	s.RequestWorkPending = Ptr(false)
	(&Sampler{}).activity(context.Background(), &s)
	if s.IsIdle != nil {
		t.Fatal("missing quality became idle")
	}
	s.Quality["inference_active"], s.Quality["request_work_pending"] = OK, OK
	s.providerPublicKey = "key"
	p := &Sampler{LatestEarnings: func(context.Context, string, time.Time) (EarningsObservation, error) {
		return EarningsObservation{}, errors.New("private upstream error")
	}}
	p.activity(context.Background(), &s)
	if s.IsIdle != nil || *s.ActivityError != "device_earnings_unavailable" {
		t.Fatal("failed source fabricated idle")
	}
	s.InferenceActive = Ptr(true)
	p.activity(context.Background(), &s)
	if s.IsIdle == nil || *s.IsIdle {
		t.Fatal("busy work depends on earnings")
	}
}

func TestProviderTaskFlagsRequireFreshLiveStateAndPreserveFalse(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name  string
		age   time.Duration
		alive bool
		flags string
		known bool
	}{
		{"fresh", 0, true, `,"inference_active":false,"request_work_pending":false`, true},
		{"old version missing flags", 0, true, "", false},
		{"stale", 91 * time.Second, true, `,"inference_active":false,"request_work_pending":false`, false},
		{"stopped", 0, false, `,"inference_active":false,"request_work_pending":false`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := map[string]any{"schema": 1, "pid": 42, "written_at": now.Add(-tc.age).Unix()}
			if tc.flags != "" {
				d["inference_active"] = false
				d["request_work_pending"] = false
			}
			body, _ := json.Marshal(d)
			p := &Sampler{read: func(string) ([]byte, error) { return body, nil }, process: func(int, *int64) (bool, error) { return tc.alive, nil }, run: func(context.Context, string, ...string) ([]byte, error) { return []byte("0"), nil }}
			s := NewSample(now)
			p.provider(context.Background(), &s)
			if (s.InferenceActive != nil) != tc.known || (s.RequestWorkPending != nil) != tc.known {
				t.Fatalf("flags: %+v", s)
			}
			if tc.known && (*s.InferenceActive || *s.RequestWorkPending || s.Quality["inference_active"] != OK) {
				t.Fatal("false lost")
			}
		})
	}
}
