package httpapi

import (
	"bloomer/agent/internal/config"
	"bloomer/agent/internal/health"
	"bloomer/agent/internal/upstream"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bloomer/agent/internal/store"
)

func TestAuthenticatedOfflineHealth(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	token := strings.Repeat("a", 64)
	handler := New(s, token, "test", "acct_example", nil)
	for _, tc := range []struct {
		path, method, auth, origin string
		status                     int
		code                       string
	}{
		{"/v1/health", "GET", "", "", 401, "unauthorized"},
		{"/v1/health", "GET", "Bearer invalid", "", 401, "unauthorized"},
		{"/v1/health", "GET", "Bearer " + token, "https://untrusted.example", 403, "origin_forbidden"},
		{"/v1/health", "POST", "Bearer " + token, "", 405, "method_not_allowed"},
		{"/v1/earnings", "GET", "Bearer " + token, "", 404, "not_found"},
		{"/v1/health", "GET", "Bearer " + token, "", 200, ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", tc.auth)
		req.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if w.Header().Get("X-Request-ID") == "" || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("unsafe/missing headers")
		}
		if tc.status != 200 {
			var body APIError
			if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != tc.code || body.RequestID == "" {
				t.Fatalf("invalid error: %s", w.Body.String())
			}
		} else {
			var body map[string]any
			json.Unmarshal(w.Body.Bytes(), &body)
			if body["data_stale"] != true || body["collection_state"] != "idle" || body["last_successful_sync_at"] != nil {
				t.Fatal("offline service claims fresh data")
			}
		}
	}
	s.Close()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 503 || strings.Contains(w.Body.String(), "SELECT") {
		t.Fatal("database failure masked or SQL leaked")
	}
}

func TestHealthUsesCachedFreshnessCoverageAndPersistentThrottle(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	round := store.Round{AccountID: "a", StartedAt: now, CompletedAt: now, Cutoff: now.Add(-config.Retention), Mode: "initial", Summary: upstream.Summary{AccountID: "a", TotalRecordCount: 1}, Records: []upstream.Earning{{AccountID: "a", RecordID: "one", Model: "m", CreatedAt: now.Add(-time.Hour)}}, Gap: &store.Gap{DetectedAt: now, From: now.Add(-time.Hour), To: now, Reason: "test"}}
	if _, err = s.CommitRound(context.Background(), round); err != nil {
		t.Fatal(err)
	}
	runtime := health.New()
	token := strings.Repeat("x", 64)
	handler := New(s, token, "test", "", runtime)
	read := func() map[string]any {
		t.Helper()
		request := httptest.NewRequest("GET", "/v1/health", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request)
		if w.Code != 200 {
			t.Fatalf("health: %d %s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	body := read()
	if body["data_stale"] != false || body["collection_state"] != "idle" || body["phase"] != "P3" || body["coverage"].(map[string]any)["gap_count"] != float64(1) {
		t.Fatalf("fresh state hidden: %+v", body)
	}
	if err = s.RecordFailure(context.Background(), "a", "rate_limited", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	body = read()
	if body["collection_state"] != "rate_limited" || body["last_error"] != "rate_limited" || body["retry_not_before"] == nil || body["data_stale"] != false {
		t.Fatal("cached data/failure status wrong")
	}
	// Failed upstream requests do not trigger another collection from this query.
	if err = s.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE sync_state SET last_successful_sync_at_us=?", now.Add(-3*time.Minute).UnixMicro())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if read()["data_stale"] != true {
		t.Fatal("old data reported as fresh")
	}
	runtime.Collection("degraded", "database_unavailable", "")
	if read()["last_error"] != "database_unavailable" {
		t.Fatal("in-memory write failure not visible")
	}
}
