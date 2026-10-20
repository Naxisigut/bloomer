package httpapi

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bloomer/agent/internal/control"
	"bloomer/agent/internal/store"
)

func TestCORSAuthenticationAndErrors(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	binary := filepath.Join(dir, "fixture-cli")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := control.New(context.Background(), dir, binary, true)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	const origin = "http://127.0.0.1:18080"
	h := New(s, testToken, "test", "", nil).WithControl(manager).WithAllowedOrigins([]string{origin})
	for _, tc := range []struct {
		name, method, path, origin, auth, preflightMethod, headers, body string
		status                                                           int
		cors                                                             bool
	}{
		{name: "GET preflight without token", method: "OPTIONS", path: "/v1/health", origin: origin, preflightMethod: "GET", headers: "authorization", status: 204, cors: true},
		{name: "POST preflight without token", method: "OPTIONS", path: "/v1/device/control/jobs", origin: origin, preflightMethod: "POST", headers: "Content-Type, AUTHORIZATION", status: 204, cors: true},
		{name: "unlisted origin", method: "OPTIONS", path: "/v1/health", origin: "http://127.0.0.1:18081", preflightMethod: "GET", status: 403},
		{name: "null origin", method: "GET", path: "/v1/health", origin: "null", auth: "Bearer " + testToken, status: 403},
		{name: "foreign POST", method: "POST", path: "/v1/device/control/jobs", origin: "https://evil.example", auth: "Bearer " + testToken, body: `{"request_id":"0123456789abcdef","action":"stop"}`, status: 403},
		{name: "no token", method: "GET", path: "/v1/health", origin: origin, status: 401, cors: true},
		{name: "bad token", method: "POST", path: "/v1/device/control/jobs", origin: origin, auth: "Bearer incorrect", status: 401, cors: true},
		{name: "authenticated GET", method: "GET", path: "/v1/health", origin: origin, auth: "Bearer " + testToken, status: 200, cors: true},
		{name: "authenticated POST", method: "POST", path: "/v1/device/control/jobs", origin: origin, auth: "Bearer " + testToken, body: `{"request_id":"0123456789abcdef","action":"stop"}`, status: 202, cors: true},
		{name: "invalid input", method: "GET", path: "/v1/earnings?limit=bad", origin: origin, auth: "Bearer " + testToken, status: 400, cors: true},
		{name: "not found", method: "GET", path: "/v1/missing", origin: origin, auth: "Bearer " + testToken, status: 404, cors: true},
		{name: "unsupported method", method: "DELETE", path: "/v1/health", origin: origin, auth: "Bearer " + testToken, status: 405, cors: true},
		{name: "bad preflight method", method: "OPTIONS", path: "/v1/health", origin: origin, preflightMethod: "DELETE", status: 403, cors: true},
		{name: "bad preflight headers", method: "OPTIONS", path: "/v1/health", origin: origin, preflightMethod: "GET", headers: "Authorization, X-Unsafe", status: 403, cors: true},
		{name: "OPTIONS without method", method: "OPTIONS", path: "/v1/health", origin: origin, status: 403, cors: true},
		{name: "CLI GET", method: "GET", path: "/v1/health", auth: "Bearer " + testToken, status: 200},
		{name: "CLI POST idempotent", method: "POST", path: "/v1/device/control/jobs", auth: "Bearer " + testToken, body: `{"request_id":"0123456789abcdef","action":"stop"}`, status: 202},
		{name: "CLI no token", method: "GET", path: "/v1/health", status: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", tc.auth)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Access-Control-Request-Method", tc.preflightMethod)
			r.Header.Set("Access-Control-Request-Headers", tc.headers)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			want := ""
			if tc.cors {
				want = origin
			}
			if w.Header().Get("Access-Control-Allow-Origin") != want || w.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("incorrect CORS headers", w.Header())
			}
			if !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Origin") {
				t.Fatal("missing Vary")
			}
			if tc.status == 204 && (w.Header().Get("Access-Control-Allow-Methods") != "GET, POST" || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Authorization")) {
				t.Fatal("missing preflight headers")
			}
		})
	}
	s.Close()
	r := httptest.NewRequest("GET", "/v1/health", nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatal("database error lost CORS", w.Code, w.Header())
	}
}
