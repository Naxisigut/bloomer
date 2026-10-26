package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/netrates"
	"bloomer/agent/internal/store"
)

func TestNetworkRatesHTTP(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	settings := netrates.NewSettings(t.TempDir(), false, nil)
	settings.Load(time.Now())
	runner := netrates.NewRunner(config.DefaultNetworkRates(), settings, s, fakeStatsClient{}, nil, nil, time.Now, nil)
	h := New(s, testToken, "test", "", nil).WithNetworkRates(runner)
	auth := "Bearer " + testToken
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/v1/device/network-rates", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) || !strings.Contains(w.Body.String(), `"interval_minutes":2`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := request("GET", "/v1/device/network-rates?x=1", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := request("POST", "/v1/device/network-rates/settings", `{"auto_switch":true}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"auto_switch":true`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := request("POST", "/v1/device/network-rates/settings", `{"auto_switch":true,"force":true}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/v1/device/network-rates/switches", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"switches"`) {
		t.Fatal(w.Code, w.Body)
	}
	base := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Microsecond)
	next := base.Add(5 * time.Minute)
	if err = s.SaveNetRateRound(context.Background(), netrates.Snapshot{At: next}, []netrates.Interval{
		{StartedAt: base, EndedAt: next, Model: "a", Providers: 2, DeltaRequests: 10, DeltaTokens: 6000},
		{StartedAt: base, EndedAt: next, Model: "b", Providers: 1, DeltaRequests: 1, DeltaTokens: 3000},
	}, time.Now().Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/v1/device/network-rates/token-trend", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"a"`) || !strings.Contains(w.Body.String(), `"model":"b"`) || !strings.Contains(w.Body.String(), `"tokens":36000`) || strings.Contains(w.Body.String(), `"tokens":108000`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := request("POST", "/v1/device/network-rates/token-trend", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request("POST", "/v1/device/network-rates", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	h.WithNetworkRates(nil)
	if w := request("GET", "/v1/device/network-rates", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := request("POST", "/v1/device/network-rates/settings", `{"auto_switch":true}`); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/v1/device/network-rates/token-trend", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"series":[]`) {
		t.Fatal(w.Code, w.Body)
	}
}

type fakeStatsClient struct{}

func (fakeStatsClient) Stats(context.Context, netrates.Cohort) (netrates.Snapshot, error) {
	return netrates.Snapshot{}, errStats
}

func (fakeStatsClient) Pricing(context.Context) (map[string]int64, error) {
	return nil, errStats
}

var errStats = errors.New("stats unavailable")
