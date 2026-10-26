package netrates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientStatsFiltersAndStreams(t *testing.T) {
	fixture := `{
  "snapshot_at":"2026-10-22T10:00:00Z",
  "ignored_top_level":[1,2,3],
  "last_24h_prompt_tokens":8300,
  "last_24h_completion_tokens":1000,
  "providers":[
    {"id":"a","chip_family":"M4","chip_tier":"Pro","memory_gb":64,"current_model":"model-a","requests_served":10,"tokens_generated":100,"extra":{"nested":true}},
    {"id":"b","chip_family":"M3","memory_gb":64,"current_model":"skip","requests_served":1,"tokens_generated":1},
    {"id":"c","chip_family":"M4","memory_gb":32,"current_model":"skip","requests_served":1,"tokens_generated":1},
    {"id":"","chip_family":"M4","memory_gb":64,"current_model":"x","requests_served":1,"tokens_generated":1},
    {"id":"d","chip_family":"M4","memory_gb":64,"current_model":"","requests_served":1,"tokens_generated":1}
  ],
  "more_noise":{"providers":[{"id":"ghost"}]}
}`
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stats" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fixture))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.Stats(context.Background(), Cohort{ChipFamilies: []string{"M4", "M5"}, MemoryGB: 64})
	if err != nil {
		t.Fatal(err)
	}
	if snap.At.UTC().Format("2006-01-02T15:04:05Z") != "2026-10-22T10:00:00Z" || len(snap.Providers) != 1 || snap.Providers[0].ID != "a" || snap.Providers[0].Tier != "Pro" {
		t.Fatalf("unexpected snapshot %+v", snap)
	}
	if snap.Prompt24h != 8300 || snap.Output24h != 1000 || NetworkRatio(snap) != 8.3 {
		t.Fatalf("unexpected snapshot %+v", snap)
	}
}

func TestClientStatsBodyLimit(t *testing.T) {
	padding := strings.Repeat("x", maxResponseBytes)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"snapshot_at":"2026-10-22T10:00:00Z","providers":[],"pad":"` + padding + `"}`))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Stats(context.Background(), Cohort{ChipFamilies: []string{"M4"}, MemoryGB: 64}); err != ErrStatsUnavailable {
		t.Fatal("expected body limit error", err)
	}
}

func TestClientPricing(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/pricing" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"prices":[{"model":"a","input_price":20,"output_price":100},{"model":"","output_price":1},{"model":"b","output_price":-1},{"model":"d","input_price":-1,"output_price":1},{"model":"c","output_price":2000000}]}`))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	prices, err := c.Pricing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 2 || prices["a"] != (Price{Input: 20, Output: 100}) || prices["c"] != (Price{Output: 2000000}) {
		t.Fatal(prices)
	}
}

func TestClientErrorsDoNotLeakURL(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Stats(context.Background(), Cohort{ChipFamilies: []string{"M4"}, MemoryGB: 64})
	if err != ErrStatsUnavailable || strings.Contains(err.Error(), srv.URL) {
		t.Fatal(err)
	}
}
