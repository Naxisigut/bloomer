package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"bloomer/agent/internal/alert"
	"bloomer/agent/internal/config"
	"bloomer/agent/internal/store"
)

func TestAlertEndpointsAuthenticationValidationAndDisabledHistory(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := config.DefaultAlert(filepath.Join(t.TempDir(), "bark.key"))
	m := alert.New(db, p, nil)
	h := New(db, testToken, "test", "", nil).WithAlerts(m)
	request := func(method, path, body, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	auth := "Bearer " + testToken
	for _, tc := range []struct {
		method, path, body, auth string
		want                     int
	}{
		{"GET", "/v1/alerts", "", "", 401}, {"POST", "/v1/alerts/test", "", "Bearer wrong", 401},
		{"GET", "/v1/alerts", "", auth, 200}, {"POST", "/v1/alerts/test", "", auth, 503},
		{"POST", "/v1/alerts", "", auth, 405}, {"GET", "/v1/alerts/test", "", auth, 405},
		{"GET", "/v1/alerts?limit=100", "", auth, 400}, {"POST", "/v1/alerts/test?force=1", "", auth, 400},
		{"POST", "/v1/alerts/test", `{"key":"secret"}`, auth, 400},
	} {
		w := request(tc.method, tc.path, tc.body, tc.auth)
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
		}
	}
	p.Enabled = true
	h.WithAlerts(alert.New(db, p, nil))
	w := request("POST", "/v1/alerts/test", "", auth)
	if w.Code != 202 || strings.Contains(w.Body.String(), "bark.key") {
		t.Fatal(w.Code, w.Body)
	}
	var record alert.Record
	if err = json.Unmarshal(w.Body.Bytes(), &record); err != nil || record.Type != "test" || record.Level != "active" || record.DeliveryStatus != "pending" {
		t.Fatal(record, err)
	}
	h.WithAlerts(nil)
	w = request("GET", "/v1/alerts", "", auth)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"type":"test"`) {
		t.Fatal("disabled lost history", w.Code, w.Body)
	}
	r := httptest.NewRequest("OPTIONS", "/v1/alerts/test", nil)
	r.Header.Set("Origin", "https://dashboard.example")
	r.Header.Set("Access-Control-Request-Method", "POST")
	r.Header.Set("Access-Control-Request-Headers", "Authorization")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
}
