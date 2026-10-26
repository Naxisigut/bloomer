package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProviderFreshStaleMissingAndProcessIdentity(t *testing.T) {
	now := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC)
	base := map[string]any{"schema": 1, "pid": 42, "version": "0.9.22", "written_at": now.Unix(), "current_model": "model-a", "warm_models": []string{"model-a"}, "process_identity": map[string]any{"pid": 42, "start_time_micros": 12345}, "stats": map[string]any{"requests_served": 7}, "trust": map[string]any{"status": "online", "trust_level": "hardware", "reason": "secret error"}, "auth_token": "secret-token"}
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		alive  bool
		want   string
		ready  *bool
	}{
		{"fresh", func(map[string]any) {}, true, OK, Ptr(true)},
		{"not loaded", func(d map[string]any) { d["warm_models"] = []string{} }, true, OK, Ptr(false)},
		{"load transition", func(d map[string]any) { d["load_transition_active"] = true }, true, OK, Ptr(false)},
		{"load failed", func(d map[string]any) {
			d["slots"] = []map[string]any{{"model": "model-a", "load_error": "private error"}}
		}, true, OK, Ptr(false)},
		{"missing readiness", func(d map[string]any) { delete(d, "warm_models") }, true, Unsupported, nil},
		{"stale", func(d map[string]any) { d["written_at"] = now.Add(-91 * time.Second).Unix() }, true, Stale, nil},
		{"future", func(d map[string]any) { d["written_at"] = now.Add(time.Minute).Unix() }, true, Invalid, nil},
		{"old process", func(map[string]any) {}, false, NotRunning, nil},
		{"unsupported schema", func(d map[string]any) { d["schema"] = 2 }, true, Unsupported, nil},
		{"identity conflict", func(d map[string]any) { d["process_identity"] = map[string]any{"pid": 43, "start_time_micros": 12345} }, true, Invalid, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := make(map[string]any)
			for k, v := range base {
				d[k] = v
			}
			tc.change(d)
			body, _ := json.Marshal(d)
			p := &Sampler{read: func(string) ([]byte, error) { return body, nil }, process: func(pid int, start *int64) (bool, error) {
				if pid != 42 || start == nil || *start != 12345 {
					t.Fatal("process identity was not checked")
				}
				return tc.alive, nil
			}, run: func(context.Context, string, ...string) ([]byte, error) { return []byte("12.5"), nil }}
			s := NewSample(now)
			p.provider(context.Background(), &s)
			if s.Quality["model_ready"] != tc.want {
				t.Fatalf("quality: %+v", s)
			}
			if (s.ModelReady == nil) != (tc.ready == nil) || (s.ModelReady != nil && *s.ModelReady != *tc.ready) {
				t.Fatalf("readiness: %+v", s)
			}
			if s.ProviderConnected != nil || s.DispatchTier != nil || s.Quality["provider_connected"] != Unsupported {
				t.Fatal("trust was misrepresented as connection/tier")
			}
			if tc.want == Stale || tc.want == Invalid || tc.want == NotRunning {
				if s.CurrentModel != nil {
					t.Fatal("old live state leaked")
				}
			}
			encoded, _ := json.Marshal(s)
			if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "private error") || strings.Contains(string(encoded), "requests_served") || strings.Contains(string(encoded), "process_start") {
				t.Fatal("non-whitelisted provider data leaked")
			}
			if tc.name == "fresh" && (s.ProcessStartMicros == nil || *s.ProcessStartMicros != 12345 || s.RequestsServed == nil || *s.RequestsServed != 7) {
				t.Fatal("fresh state did not keep process and request identity")
			}
		})
	}
	for _, tc := range []struct {
		body []byte
		err  error
		want string
	}{
		{nil, os.ErrNotExist, Unavailable}, {nil, os.ErrPermission, PermissionDenied}, {[]byte(`{"schema":1,"pid":42}`), nil, Invalid}, {[]byte(`not json`), nil, Invalid},
	} {
		p := &Sampler{read: func(string) ([]byte, error) { return tc.body, tc.err }}
		s := NewSample(now)
		p.provider(context.Background(), &s)
		if s.ProviderRunning != nil || s.Quality["provider_running"] != tc.want {
			t.Fatalf("missing observation became false: %+v", s)
		}
	}
}

func TestStateReadDoesNotFollowLinksOrAcceptNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readState(link); err == nil {
		t.Fatal("followed a symlink")
	}
	if _, err := readState(dir); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("directory accepted: %v", err)
	}
	if _, err := readState(path); err != nil {
		t.Fatal(err)
	}
}

func TestTrustEvidenceFreshnessPrecisionAndIsolation(t *testing.T) {
	now := time.Date(2026, 10, 21, 2, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, trust string
		age         time.Duration
		quality     string
		want        bool
	}{
		{"fractional", `{"status":"online","received_at":HEARTBEAT,"authorization":{"session_id":"provider-session"}}`, 0, OK, true},
		{"stale", `{"received_at":HEARTBEAT,"authorization":{"session_id":"provider-session"}}`, 91 * time.Second, Stale, false},
		{"future state", `{"received_at":HEARTBEAT}`, -6 * time.Second, Invalid, false},
		{"missing", `{}`, 0, Unsupported, false},
		{"invalid heartbeat", `{"received_at":"bad","authorization":{"session_id":"provider-session"}}`, 0, Invalid, false},
		{"invalid trust", `[]`, 0, Invalid, false},
		{"future heartbeat", `{"received_at":253402300799}`, 0, Invalid, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trust := strings.ReplaceAll(tc.trust, "HEARTBEAT", fmt.Sprintf("%.3f", float64(now.Unix())-9.875))
			body := []byte(`{"schema":1,"pid":42,"written_at":` + fmt.Sprint(now.Add(-tc.age).Unix()) + `,"current_model":"model","warm_models":["model"],"trust":` + trust + `}`)
			p := &Sampler{read: func(string) ([]byte, error) { return body, nil }, process: func(int, *int64) (bool, error) { return true, nil }, run: func(context.Context, string, ...string) ([]byte, error) { return []byte("0"), nil }}
			s := NewSample(now)
			p.provider(context.Background(), &s)
			if s.Quality["trust_received_at"] != tc.quality || (s.TrustReceivedAt != nil) != tc.want {
				t.Fatalf("%+v", s)
			}
			if tc.want && (*s.TrustReceivedAt != float64(now.Unix())-9.875 || s.SessionID == nil || *s.SessionID != "provider-session") {
				t.Fatal("precision/session lost")
			}
			if tc.age == 0 && (s.ProviderRunning == nil || !*s.ProviderRunning || s.ModelReady == nil || !*s.ModelReady) {
				t.Fatal("optional trust corrupted process/model semantics")
			}
			if s.ProviderConnected != nil || s.Quality["provider_connected"] != Unsupported {
				t.Fatal("legacy connection semantics changed")
			}
		})
	}
}
