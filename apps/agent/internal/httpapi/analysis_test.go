package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"testing"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/store"
	"bloomer/agent/internal/upstream"
)

func seedDeviceProviders(t *testing.T, s *store.Store, attestationKey string, providerIDs ...string) {
	t.Helper()
	digest := sha256.Sum256([]byte(attestationKey))
	hash := hex.EncodeToString(digest[:])
	err := s.Write(context.Background(), func(tx *sql.Tx) error {
		for _, id := range providerIDs {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO device_provider_ids(identity_hash,provider_id) VALUES(?,?)`, hash, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func earningProvider(id, model, provider string, at time.Time, amount int64) upstream.Earning {
	e := earning(id, model, at, amount)
	e.ProviderID = provider
	return e
}

func analysisPath(from, to time.Time, extra string) string {
	q := "from=" + url.QueryEscape(from.UTC().Format(time.RFC3339Nano)) +
		"&to=" + url.QueryEscape(to.UTC().Format(time.RFC3339Nano)) +
		"&timezone=Asia%2FShanghai"
	if extra != "" {
		q += "&" + extra
	}
	return "/v1/earnings/analysis?" + q
}

func TestAnalysisValidationAndIdentity(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	from := now.Add(-7 * 24 * time.Hour)
	to := now
	h, s := queryAPI(t, &now, nil)
	seedDeviceProviders(t, s, "key", "own")

	base := analysisPath(from, to, "granularity=day")
	for _, path := range []string{
		"/v1/earnings/analysis?timezone=Asia%2FShanghai",
		base + "&unknown=x",
		analysisPath(from, to, "granularity=day&timezone=Local"),
		analysisPath(from, to, "granularity=day&timezone=No%2FSuch"),
		analysisPath(from, to, "granularity=invalid"),
		analysisPath(to, from, "granularity=day"),
		analysisPath(from, from.Add(367*24*time.Hour), "granularity=day"),
		analysisPath(from, from.Add(32*24*time.Hour), "granularity=hour"),
		base + "&model=a&model=a",
		base + "&" + repeatModels(maxModelFilters+1),
	} {
		status, body := callAPI(t, h, path)
		if status != 400 || body["code"] != "invalid_request" {
			t.Fatalf("expected invalid_request for %s: %d %+v", path, status, body)
		}
	}
	if status, body := callAPI(t, h, base+"&"+repeatModels(maxModelFilters)); status != 200 {
		t.Fatalf("%d distinct models allowed: %d %+v", maxModelFilters, status, body)
	}
	h2, _ := queryAPI(t, &now, []upstream.Earning{
		earningProvider("r1", "alpha", "other", now.Add(-time.Hour), 100),
	})
	if status, body := callAPI(t, h2, analysisPath(from, to, "granularity=day")); status != 409 || body["code"] != "device_identity_unavailable" {
		t.Fatalf("missing device providers: %d %+v", status, body)
	}
}

func repeatModels(n int) string {
	out := ""
	for i := 0; i < n; i++ {
		if out != "" {
			out += "&"
		}
		out += fmt.Sprintf("model=m%d", i)
	}
	return out
}

func TestAnalysisDeviceScopeModelsFilterAndEmpty(t *testing.T) {
	now := time.Date(2026, 6, 15, 4, 0, 0, 0, time.UTC)
	// Monday 2026-06-15 12:00 in Asia/Shanghai = 04:00 UTC
	mondayNoon := time.Date(2026, 6, 15, 4, 0, 0, 0, time.UTC)
	from := now.Add(-24 * time.Hour)
	to := now.Add(2 * time.Hour)
	h, s := queryAPI(t, &now, []upstream.Earning{
		earningProvider("own1", "keep", "own", mondayNoon, 10),
		earningProvider("own2", "drop", "own", mondayNoon.Add(time.Hour), 20),
		earningProvider("other", "ghost", "foreign", mondayNoon, 999),
	})
	seedDeviceProviders(t, s, "device-key", "own")

	path := analysisPath(from, to, "granularity=hour&model=keep")
	status, body := callAPI(t, h, path)
	if status != 200 {
		t.Fatal(body)
	}
	if body["totals"].(map[string]any)["amount_micro_usd"] != "10" || body["totals"].(map[string]any)["record_count"] != float64(1) {
		t.Fatalf("filtered totals wrong: %+v", body["totals"])
	}
	models := body["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("models list should ignore filter: %+v", models)
	}
	if models[0].(map[string]any)["model"] != "drop" || models[0].(map[string]any)["amount_micro_usd"] != "20" {
		t.Fatal("models not sorted by amount desc")
	}
	series := body["series"].([]any)
	if len(series) != 1 {
		t.Fatalf("expected one series bucket: %+v", series)
	}
	bucket := series[0].(map[string]any)
	items := bucket["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["model"] != "keep" {
		t.Fatal("series items wrong", items)
	}
	wh := body["weekday_hour"].([]any)
	if len(wh) != 1 {
		t.Fatalf("weekday_hour: %+v", wh)
	}
	row := wh[0].(map[string]any)
	if row["weekday"] != float64(1) || row["hour"] != float64(12) {
		t.Fatalf("weekday/hour in Shanghai: %+v", row)
	}
	for _, key := range []string{"models", "series", "hour_of_day", "weekday_hour"} {
		if body[key] == nil {
			t.Fatalf("%s must be array not null", key)
		}
	}

	hEmpty, sEmpty := queryAPI(t, &now, nil)
	seedDeviceProviders(t, sEmpty, "k", "own")
	if status, body := callAPI(t, hEmpty, analysisPath(from, to, "granularity=day")); status != 200 {
		t.Fatal(body)
	} else if body["totals"].(map[string]any)["record_count"] != float64(0) || len(body["models"].([]any)) != 0 {
		t.Fatal("empty result shape", body)
	}
}

func TestAnalysisRetentionTruncation(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	cut := now.Add(-config.Retention)
	h, s := queryAPI(t, &now, []upstream.Earning{
		earningProvider("old", "m", "own", cut.Add(-time.Hour), 5),
		earningProvider("ok", "m", "own", cut.Add(time.Second), 7),
	})
	seedDeviceProviders(t, s, "k", "own")
	status, body := callAPI(t, h, analysisPath(cut.Add(-24*time.Hour), now, "granularity=day"))
	if status != 200 || body["meta"].(map[string]any)["range_truncated"] != true {
		t.Fatal(body)
	}
	if body["totals"].(map[string]any)["amount_micro_usd"] != "7" {
		t.Fatal("retention filter wrong", body)
	}
}
