package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bloomer/agent/internal/store"
	"bloomer/agent/internal/telemetry"
)

func TestDeviceAPIOfflineAccountFreshnessAndBoundedHistory(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC)
	h := New(s, testToken, "test", "", nil)
	h.now = func() time.Time { return now }
	request := func(path, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// Reuse only the token from the query fixtures, without an account snapshot.
	auth := "Bearer " + testToken
	if w := request("/v1/device/state", ""); w.Code != 401 {
		t.Fatal("unauthenticated telemetry exposed")
	}
	w := request("/v1/device/state", auth)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sample":null`) || !strings.Contains(w.Body.String(), `"data_stale":true`) {
		t.Fatalf("empty history: %d %s", w.Code, w.Body)
	}
	id, err := s.SaveDeviceIdentity(context.Background(), telemetry.Hardware{OS: "darwin"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		v := telemetry.NewSample(now.Add(time.Duration(i-2) * time.Minute))
		v.DeviceID = id
		v.CPUPercent = telemetry.Ptr(float64(i))
		v.Quality["cpu_percent"] = telemetry.OK
		v.InferenceActive, v.RequestWorkPending, v.IsIdle = telemetry.Ptr(false), telemetry.Ptr(false), telemetry.Ptr(true)
		v.LastDeviceEarningAt = telemetry.Ptr(now.Add(-6 * time.Minute))
		v.Quality["inference_active"], v.Quality["request_work_pending"], v.Quality["is_idle"] = telemetry.OK, telemetry.OK, telemetry.OK
		if err = s.SaveDeviceSample(context.Background(), v); err != nil {
			t.Fatal(err)
		}
	}
	w = request("/v1/device/state", auth)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"data_stale":false`) {
		t.Fatalf("fresh telemetry unavailable without earnings: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), `"is_idle":true`) || !strings.Contains(w.Body.String(), `"inference_active":false`) || strings.Contains(w.Body.String(), "routing_") {
		t.Fatalf("activity payload incorrect: %s", w.Body)
	}

	w = request("/v1/device/state/history?limit=2", auth)
	var body struct {
		Items []telemetry.Sample `json:"items"`
		Next  *int64             `json:"next_before_id"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Items) != 2 || body.Next == nil {
		t.Fatalf("history: %s", w.Body)
	}
	for _, path := range []string{"/v1/device/state?limit=1", "/v1/device/state/history?limit=501", "/v1/device/state/history?limit=0", "/v1/device/state/history?before_id=0", "/v1/device/state/history?limit=1&limit=2", "/v1/device/state/history?from=oops"} {
		if w := request(path, auth); w.Code != 400 {
			t.Fatalf("invalid query %s: %d", path, w.Code)
		}
	}
	h.now = func() time.Time { return now.Add(3 * time.Minute) }
	if w := request("/v1/device/state", auth); !strings.Contains(w.Body.String(), `"data_stale":true`) {
		t.Fatal("stale telemetry looks live")
	}
	// Wall-clock rollback leaves a future-dated observation visible and stale.
	h.now = func() time.Time { return now.Add(-time.Minute) }
	if w := request("/v1/device/state", auth); !strings.Contains(w.Body.String(), `"data_stale":true`) {
		t.Fatal("future observation looks live")
	}
}
