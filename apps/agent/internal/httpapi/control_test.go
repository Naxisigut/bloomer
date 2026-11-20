package httpapi

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bloomer/agent/internal/control"
)

func TestControlAuthenticationMethodsAndStrictRequests(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "missing-darkbloom")
	m, err := control.New(context.Background(), dir, binary, true)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	h := New(nil, testToken, "test", "", nil).WithControl(m)
	request := func(method, path, body, auth, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", auth)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	valid := `{"request_id":"0123456789abcdef","action":"stop"}`
	auth := "Bearer " + testToken
	for _, tc := range []struct {
		method, path, body, auth, origin string
		want                             int
	}{
		{"POST", "/v1/device/control/jobs", valid, "", "", 401},
		{"POST", "/v1/device/control/jobs", valid, "Bearer incorrect", "https://dashboard.example", 401},
		{"GET", "/v1/device/control", "", auth, "", 200},
		{"GET", "/v1/device/control/jobs", "", auth, "", 200},
		{"GET", "/v1/device/control?force=1", "", auth, "", 400},
		{"GET", "/v1/device/control/unknown", "", auth, "", 404},
		{"POST", "/v1/device/control", valid, auth, "", 405},
		{"DELETE", "/v1/device/control/jobs", "", auth, "", 405},
		{"POST", "/v1/device/control/jobs", `null`, auth, "", 400},
		{"POST", "/v1/device/control/jobs", valid + ` {}`, auth, "", 400},
		{"POST", "/v1/device/control/jobs", `{"request_id":"0123456789abcdef","action":"stop","force":true}`, auth, "", 400},
		{"POST", "/v1/device/control/jobs", `{"request_id":"0123456789abcdef","action":"force_restart"}`, auth, "", 400},
		{"POST", "/v1/device/control/jobs", `{"request_id":"0123456789abcdef","action":"idle_restart"}`, auth, "", 400},
		{"POST", "/v1/device/control/jobs", `{"request_id":"0123456789abcdef","action":"download","models":["--force"]}`, auth, "", 400},
		{"POST", "/v1/device/control/jobs", valid, auth, "", 503},
	} {
		w := request(tc.method, tc.path, tc.body, tc.auth, tc.origin)
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
		}
	}
	// A harmless fixture CLI exercises actual asynchronous HTTP acceptance.
	if err = os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	w := request("POST", "/v1/device/control/jobs", valid, auth, "")
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"status":"running"`) {
		t.Fatal(w.Code, w.Body)
	}
}
