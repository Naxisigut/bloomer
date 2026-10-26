package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"bloomer/agent/internal/longtask"
)

func TestLongTaskRestartSettings(t *testing.T) {
	watch := longtask.New(t.TempDir(), false, nil, nil)
	h := New(nil, testToken, "test", "", nil).WithLongTask(watch)
	request := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/device/long-task-restart", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := request("POST", `{"enabled":true}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) || !watch.Enabled() {
		t.Fatal(w.Code, w.Body)
	}
	if w := request("POST", `{"enabled":true,"force":true}`); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request("DELETE", ""); w.Code != 405 {
		t.Fatal(w.Code, w.Body)
	}
	missing := New(nil, testToken, "test", "", nil)
	r := httptest.NewRequest("POST", "/v1/device/long-task-restart", strings.NewReader(`{"enabled":true}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	missing.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
}
