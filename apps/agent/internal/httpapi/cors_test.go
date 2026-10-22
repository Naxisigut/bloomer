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
	h := New(s, testToken, "test", "", nil).WithControl(manager)
	for _, tc := range []struct {
		name, method, path, origin, auth, preflightMethod, headers, body string
		status                                                           int
	}{
		{name: "GET preflight without token", method: "OPTIONS", path: "/v1/health", origin: origin, preflightMethod: "GET", headers: "authorization", status: 204},
		{name: "POST preflight without token", method: "OPTIONS", path: "/v1/device/control/jobs", origin: origin, preflightMethod: "POST", headers: "Content-Type, AUTHORIZATION", status: 204},
		{name: "other origin preflight", method: "OPTIONS", path: "/v1/health", origin: "http://127.0.0.1:18081", preflightMethod: "GET", status: 204},
		{name: "null origin", method: "GET", path: "/v1/health", origin: "null", auth: "Bearer " + testToken, status: 200},
		{name: "other origin missing POST token", method: "POST", path: "/v1/device/control/jobs", origin: "https://dashboard.example", body: `{"request_id":"0123456789abcdef","action":"stop"}`, status: 401},
		{name: "no token", method: "GET", path: "/v1/health", origin: origin, status: 401},
		{name: "bad token", method: "POST", path: "/v1/device/control/jobs", origin: origin, auth: "Bearer incorrect", status: 401},
		{name: "authenticated GET", method: "GET", path: "/v1/health", origin: origin, auth: "Bearer " + testToken, status: 200},
		{name: "authenticated other origin POST", method: "POST", path: "/v1/device/control/jobs", origin: "https://other.example:9443", auth: "Bearer " + testToken, body: `{"request_id":"0123456789abcdef","action":"stop"}`, status: 202},
		{name: "invalid input", method: "GET", path: "/v1/earnings?limit=bad", origin: origin, auth: "Bearer " + testToken, status: 400},
		{name: "not found", method: "GET", path: "/v1/missing", origin: origin, auth: "Bearer " + testToken, status: 404},
		{name: "unsupported method", method: "DELETE", path: "/v1/health", origin: origin, auth: "Bearer " + testToken, status: 405},
		{name: "bad preflight method", method: "OPTIONS", path: "/v1/health", origin: origin, preflightMethod: "DELETE", status: 403},
		{name: "bad preflight headers", method: "OPTIONS", path: "/v1/health", origin: origin, preflightMethod: "GET", headers: "Authorization, X-Unsafe", status: 403},
		{name: "OPTIONS without method", method: "OPTIONS", path: "/v1/health", origin: origin, status: 403},
		{name: "null origin missing token", method: "GET", path: "/v1/health", origin: "null", status: 401},
		{name: "other origin bad GET token", method: "GET", path: "/v1/health", origin: "https://other.example", auth: "Bearer incorrect", status: 401},
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
			if w.Header().Get("Access-Control-Allow-Origin") != "*" || w.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("incorrect CORS headers", w.Header())
			}
			if tc.status == 204 {
				if w.Header().Get("Access-Control-Allow-Methods") != "GET, POST" || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
					t.Fatal("missing preflight headers")
				}
				if w.Header().Get("Access-Control-Max-Age") != "7200" {
					t.Fatalf("preflight cache: %q", w.Header().Get("Access-Control-Max-Age"))
				}
			}
		})
	}
	if len(manager.Status().Jobs) != 1 {
		t.Fatal("preflight or unauthorized request created a job")
	}
	s.Close()
	r := httptest.NewRequest("GET", "/v1/health", nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("database error lost CORS", w.Code, w.Header())
	}
}
