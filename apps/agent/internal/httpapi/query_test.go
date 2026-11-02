package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/health"
	"bloomer/agent/internal/store"
	"bloomer/agent/internal/upstream"
)

const testToken = "synthetic-dashboard-token-01234567890123456789"

func queryAPI(t *testing.T, now *time.Time, records []upstream.Earning) (*Handler, *store.Store) {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	_, err = s.CommitRound(context.Background(), store.Round{AccountID: "a", StartedAt: *now, CompletedAt: *now, Cutoff: now.Add(-2 * config.Retention), Mode: "initial", Records: records, Summary: upstream.Summary{AccountID: "a", TotalRecordCount: 100000, TotalMicroUSD: 9007199254740993, AvailableBalanceMicroUSD: 7000, WithdrawableBalanceMicroUSD: 6000}})
	if err != nil {
		t.Fatal(err)
	}
	h := New(s, testToken, "test", "", health.New())
	h.now = func() time.Time { return *now }
	return h, s
}
func earning(id, model string, at time.Time, amount int64) upstream.Earning {
	return upstream.Earning{AccountID: "a", RecordID: id, ProviderID: "p", ProviderKey: "pk", JobID: "j", Model: model, CreatedAt: at, AmountMicroUSD: amount}
}
func callAPI(t *testing.T, h *Handler, path string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON: %s", w.Body.String())
	}
	return w.Code, body
}
func items(body map[string]any) []any { return body["items"].([]any) }
func TestQueryAmountsScopesAndErrors(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	cut := now.Add(-config.Retention)
	h, s := queryAPI(t, &now, []upstream.Earning{earning("boundary", "m", cut, 9007199254740993), earning("negative", "m", now.Add(-time.Hour), -3), earning("expired", "m", cut.Add(-time.Microsecond), 999)})
	status, overview := callAPI(t, h, "/v1/account/overview")
	if status != 200 {
		t.Fatal(overview)
	}
	if overview["official"].(map[string]any)["total_micro_usd"] != "9007199254740993" || overview["local_last_365_days"].(map[string]any)["amount_micro_usd"] != "9007199254740990" {
		t.Fatal("float precision loss or lifetime scope mixed")
	}
	status, page := callAPI(t, h, "/v1/earnings?from="+url.QueryEscape(cut.Add(-time.Hour).Format(time.RFC3339Nano)))
	if status != 200 || len(items(page)) != 2 || page["meta"].(map[string]any)["range_truncated"] != true {
		t.Fatal("retention range not enforced")
	}
	status, stats := callAPI(t, h, "/v1/earnings/stats?group_by=model&timezone=UTC")
	if status != 200 || items(stats)[0].(map[string]any)["amount_micro_usd"] != "9007199254740990" {
		t.Fatal("stats scope/precision wrong")
	}
	if err := s.RecordFailure(context.Background(), "a", "authentication_failed", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if status, _ := callAPI(t, h, "/v1/account/overview"); status != 200 {
		t.Fatal("upstream failure hid local cache")
	}
	for _, path := range []string{"/v1/earnings?limit=0", "/v1/earnings?limit=501", "/v1/earnings?limit=1&limit=2", "/v1/earnings?from=bad", "/v1/earnings?unknown=x", "/v1/earnings/stats", "/v1/earnings/stats?timezone=Local", "/v1/earnings/stats?timezone=No/SuchZone", "/v1/earnings/stats?timezone=UTC&group_by=invalid"} {
		if status, body := callAPI(t, h, path); status != 400 || body["request_id"] == nil {
			t.Fatalf("invalid query accepted %s: %d %+v", path, status, body)
		}
	}
}
func TestAccountSummaryRollingTotals(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	h, _ := queryAPI(t, &now, []upstream.Earning{
		earning("hour-edge", "m", now.Add(-time.Hour), 3),
		earning("recent", "m", now.Add(-30*time.Minute), 5),
		earning("day", "n", now.Add(-2*time.Hour), 7),
		earning("day-edge", "n", now.Add(-24*time.Hour), 13),
		earning("older", "m", now.Add(-25*time.Hour), 11),
	})
	status, body := callAPI(t, h, "/v1/account/summary")
	if status != 200 {
		t.Fatal(body)
	}
	total := func(key string) (string, float64) {
		value := body[key].(map[string]any)
		return value["amount_micro_usd"].(string), value["record_count"].(float64)
	}
	if amount, count := total("last_hour"); amount != "8" || count != 2 {
		t.Fatalf("last hour %s/%v", amount, count)
	}
	if amount, count := total("last_24_hours"); amount != "28" || count != 4 {
		t.Fatalf("last 24 hours %s/%v", amount, count)
	}
	if amount, count := total("local_last_365_days"); amount != "39" || count != 5 {
		t.Fatalf("retained %s/%v", amount, count)
	}
	hour := body["last_hour"].(map[string]any)
	if hour["from"] != now.Add(-time.Hour).Format(time.RFC3339Nano) || hour["to"] != now.Format(time.RFC3339Nano) {
		t.Fatalf("last hour bounds %+v", hour)
	}
	if body["official"].(map[string]any)["available_balance_micro_usd"] != "7000" || body["meta"].(map[string]any)["range_truncated"] != false {
		t.Fatalf("summary overview mismatch %+v", body)
	}
	if status, body := callAPI(t, h, "/v1/account/summary?timezone=UTC"); status != 400 || body["request_id"] == nil {
		t.Fatalf("unknown parameter accepted: %d %+v", status, body)
	}
}
func TestStableCursorWithMovingRetentionAndConcurrentInsert(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	original := now
	rows := make([]upstream.Earning, 123)
	for i := range rows {
		rows[i] = earning(fmt.Sprintf("r-%03d", i), "m", now.Add(-time.Hour), 1)
	}
	h, s := queryAPI(t, &now, rows)
	seen := map[string]bool{}
	next := ""
	pageCount := 0
	for {
		path := "/v1/earnings?limit=7"
		if next != "" {
			path += "&cursor=" + url.QueryEscape(next)
		}
		status, body := callAPI(t, h, path)
		if status != 200 {
			t.Fatalf("ordinary time advance invalidated cursor: %+v", body)
		}
		for _, raw := range items(body) {
			id := raw.(map[string]any)["record_id"].(string)
			if seen[id] || id == "concurrent-new" {
				t.Fatalf("duplicate/newer insertion appeared mid-pagination: %s", id)
			}
			seen[id] = true
		}
		pageCount++
		if pageCount == 1 {
			now = now.Add(time.Second)
			_, err := s.CommitRound(context.Background(), store.Round{AccountID: "a", StartedAt: now, CompletedAt: now, Cutoff: now.Add(-config.Retention), Mode: "normal", Records: []upstream.Earning{earning("concurrent-new", "m", original.Add(-time.Minute), 2)}, Summary: upstream.Summary{AccountID: "a", TotalRecordCount: 100001}})
			if err != nil {
				t.Fatal(err)
			}
		}
		if body["next_cursor"] == nil {
			break
		}
		next = body["next_cursor"].(string)
		now = now.Add(2 * time.Second)
		if pageCount > 30 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 123 {
		t.Fatalf("pagination lost records: %d", len(seen))
	}
	if status, body := callAPI(t, h, "/v1/earnings?limit=1"); status != 200 || items(body)[0].(map[string]any)["record_id"] != "concurrent-new" {
		t.Fatal("refresh did not see newer insertion")
	}
}
func TestCursorMismatchExpiryAndOldOrderingKey(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	cut := now.Add(-config.Retention)
	h, _ := queryAPI(t, &now, []upstream.Earning{earning("a", "m", cut.Add(time.Second), 1), earning("b", "m", cut.Add(time.Second), 1)})
	status, first := callAPI(t, h, "/v1/earnings?model=m&limit=1")
	if status != 200 {
		t.Fatal(first)
	}
	c := first["next_cursor"].(string)
	for _, suffix := range []string{"&model=other", "&from=2026-01-01T00%3A00%3A00Z", "&to=2026-10-15T00%3A00%3A00Z"} {
		if status, _ := callAPI(t, h, "/v1/earnings?cursor="+url.QueryEscape(c)+suffix); status != 400 {
			t.Fatal("mismatched cursor accepted")
		}
	}
	parts := strings.Split(c, ".")
	parts[1] = strings.Repeat("A", len(parts[1]))
	if status, _ := callAPI(t, h, "/v1/earnings?cursor="+url.QueryEscape(strings.Join(parts, "."))); status != 400 {
		t.Fatal("tampered cursor accepted")
	}
	now = now.Add(2 * time.Second)
	status, empty := callAPI(t, h, "/v1/earnings?cursor="+url.QueryEscape(c))
	if status != 200 || len(items(empty)) != 0 || empty["next_cursor"] != nil {
		t.Fatal("expired ordering key did not end normally")
	}
	now = now.Add(config.Retention)
	status, expired := callAPI(t, h, "/v1/earnings?cursor="+url.QueryEscape(c))
	if status != 400 || expired["code"] != "cursor_expired" {
		t.Fatal("entire expired cursor range was accepted")
	}
}
func TestIANAStatsDSTAndFractionalOffset(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	parse := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	h, _ := queryAPI(t, &now, []upstream.Earning{
		earning("fall1", "fall", parse("2026-11-01T05:30:00Z"), 2), earning("fall2", "fall", parse("2026-11-01T06:30:00Z"), 3),
		earning("spring1", "spring", parse("2026-03-08T06:30:00Z"), 1), earning("spring2", "spring", parse("2026-03-08T07:30:00Z"), 1),
		earning("lord1", "lord", parse("2026-04-04T14:45:00Z"), 1), earning("lord2", "lord", parse("2026-04-04T15:15:00Z"), 1),
	})
	for _, tc := range []struct {
		model string
		hours float64
	}{{"fall", 25}, {"spring", 23}} {
		status, body := callAPI(t, h, "/v1/earnings/stats?timezone=America%2FNew_York&group_by=day&model="+tc.model)
		if status != 200 || len(items(body)) != 1 {
			t.Fatal(body)
		}
		row := items(body)[0].(map[string]any)
		if parse(row["to"].(string)).Sub(parse(row["from"].(string))).Hours() != tc.hours {
			t.Fatalf("calendar day assumed 24 hours: %+v", row)
		}
	}
	status, body := callAPI(t, h, "/v1/earnings/stats?timezone=America%2FNew_York&group_by=hour&model=fall")
	if status != 200 || len(items(body)) != 2 || items(body)[0].(map[string]any)["key"] == items(body)[1].(map[string]any)["key"] {
		t.Fatal("repeated hour merged")
	}
	status, body = callAPI(t, h, "/v1/earnings/stats?timezone=Australia%2FLord_Howe&group_by=hour&model=lord")
	if status != 200 || len(items(body)) != 2 {
		t.Fatal(body)
	}
	last := items(body)[1].(map[string]any)
	if parse(last["to"].(string)).Sub(parse(last["from"].(string))) != 30*time.Minute {
		t.Fatal("half-hour DST transition misbucketed")
	}
}

func TestIntegerAggregateIntermediateOverflowAndCancelledQueries(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	h, _ := queryAPI(t, &now, []upstream.Earning{earning("a", "m", now.Add(-time.Hour), 9223372036854775807), earning("b", "m", now.Add(-time.Hour), 1), earning("c", "m", now.Add(-time.Hour), -1)})
	for _, path := range []string{"/v1/account/overview", "/v1/earnings/stats?timezone=UTC&group_by=model"} {
		status, body := callAPI(t, h, path)
		if status != 200 {
			t.Fatal(body)
		}
		amount := ""
		if path == "/v1/account/overview" {
			amount = body["local_last_365_days"].(map[string]any)["amount_micro_usd"].(string)
		} else {
			amount = items(body)[0].(map[string]any)["amount_micro_usd"].(string)
		}
		if amount != "9223372036854775807" {
			t.Fatal("intermediate aggregate overflowed")
		}
	}
	for i := 0; i < 4; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		request := httptest.NewRequest("GET", "/v1/earnings/stats?timezone=UTC", nil).WithContext(ctx)
		request.Header.Set("Authorization", "Bearer "+testToken)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != 504 {
			t.Fatalf("cancelled query misreported: %d", response.Code)
		}
	}
	if status, _ := callAPI(t, h, "/v1/earnings"); status != 200 {
		t.Fatal("cancelled queries leaked read connections")
	}
	if status, body := callAPI(t, h, "/v1/earnings?to=2020-01-01T00%3A00%3A00Z"); status != 200 || len(items(body)) != 0 || body["meta"].(map[string]any)["range_truncated"] != true {
		t.Fatal("old end range did not explain available retention")
	}
	if status, _ := callAPI(t, h, "/v1/earnings?model=%zz"); status != 400 {
		t.Fatal("malformed percent encoding accepted")
	}
}
