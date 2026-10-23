package httpapi

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bloomer/agent/internal/store"
	"bloomer/agent/internal/telemetry"
)

func TestDeviceAPIOfflineAccountFreshnessAndLatestOnly(t *testing.T) {
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
	auth := "Bearer " + testToken
	if w := request("/v1/device/state", ""); w.Code != 401 {
		t.Fatal("unauthenticated telemetry exposed")
	}
	w := request("/v1/device/state", auth)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sample":null`) || !strings.Contains(w.Body.String(), `"data_stale":true`) || !strings.Contains(w.Body.String(), `"collection_enabled":true`) {
		t.Fatalf("empty history: %d %s", w.Code, w.Body)
	}
	if w := request("/v1/device/state/history", auth); w.Code != 404 {
		t.Fatalf("history endpoint removed: %d", w.Code)
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
	if !strings.Contains(w.Body.String(), `"cpu_percent":2`) || strings.Contains(w.Body.String(), `"cpu_percent":0`) {
		t.Fatalf("latest sample not retained: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), `"is_idle":true`) || !strings.Contains(w.Body.String(), `"inference_active":false`) || strings.Contains(w.Body.String(), "routing_") {
		t.Fatalf("activity payload incorrect: %s", w.Body)
	}
	for _, path := range []string{"/v1/device/state?limit=1", "/v1/device/state?from=oops"} {
		if w := request(path, auth); w.Code != 400 {
			t.Fatalf("invalid query %s: %d", path, w.Code)
		}
	}
	h.now = func() time.Time { return now.Add(3 * time.Minute) }
	if w := request("/v1/device/state", auth); !strings.Contains(w.Body.String(), `"data_stale":true`) {
		t.Fatal("stale telemetry looks live")
	}
	h.now = func() time.Time { return now.Add(-time.Minute) }
	if w := request("/v1/device/state", auth); !strings.Contains(w.Body.String(), `"data_stale":true`) {
		t.Fatal("future observation looks live")
	}
	h.WithDeviceStateEnabled(false)
	if w := request("/v1/device/state", auth); !strings.Contains(w.Body.String(), `"collection_enabled":false`) {
		t.Fatalf("disabled device collection not reported: %s", w.Body)
	}
}
