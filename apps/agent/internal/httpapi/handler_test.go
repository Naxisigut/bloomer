package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"bloomer/agent/internal/store"
)

func TestAuthenticatedOfflineHealth(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	token := strings.Repeat("a", 64)
	handler := New(s, token, "test", "acct_example")
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
			if body["data_stale"] != true || body["collection_state"] != "disabled" || body["last_successful_sync_at"] != nil {
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
