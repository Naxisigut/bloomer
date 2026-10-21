package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bloomer/agent/internal/config"
)

func TestBarkFormatResponseAndKeyNeverPersisted(t *testing.T) {
	m, now, _ := setup(t)
	key := "sensitive-bark-key-NEVER-LOG"
	keyPath := filepath.Join(t.TempDir(), "bark.key")
	if err := os.WriteFile(keyPath, []byte(key+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var status, code atomic.Int64
	status.Store(200)
	code.Store(200)
	received := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/push" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("request format", r.Method, r.URL.Path, r.Header)
		}
		var data map[string]any
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			t.Error(err)
		}
		received <- data
		w.WriteHeader(int(status.Load()))
		fmt.Fprintf(w, `{"code":%d,"message":%q}`, code.Load(), key)
	}))
	defer server.Close()
	var log bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&log, nil))
	m.policy.BarkKeyFile = keyPath
	m.policy.BarkServer = server.URL
	m.policy.BindInterface = ""
	m.policy.DashboardURL = "https://example.com/dashboard"
	m.sender = newBark(m.policy, m.logger)
	s := sample(base)
	s.ProviderRunning = telemetryBool(false)
	evaluate(t, m, s)
	s.CollectedAt = base.Add(time.Minute)
	evaluate(t, m, s)
	*now = s.CollectedAt
	if ok, err := m.dispatch(context.Background()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	data := <-received
	if data["device_key"] != key || data["group"] != "darkbloom" || data["level"] != "critical" || data["sound"] != "alarm" || data["url"] != m.policy.DashboardURL || !strings.Contains(data["body"].(string), "2026-10-21 10:01:00") {
		t.Fatal("invalid payload", data)
	}
	if _, err := m.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.dispatch(context.Background())
	second := <-received
	if _, ok := second["sound"]; ok || second["level"] != "active" {
		t.Fatal(second)
	}
	status.Store(500)
	code.Store(500)
	if _, err := m.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.dispatch(context.Background())
	rows := history(t, m)
	encoded, _ := json.Marshal(rows)
	if strings.Contains(log.String(), key) || strings.Contains(string(encoded), key) {
		t.Fatal("secret in records/logs")
	}
	// Includes the durable outbox and engine state, not only the public records.
	for _, table := range []string{"alert_records", "alert_outbox", "alert_state"} {
		column := "record_json"
		if table == "alert_state" {
			column = "state_json"
		}
		var n int
		if err := m.db.ReadDB().QueryRow("SELECT count(*) FROM "+table+" WHERE "+column+" LIKE ?", "%"+key+"%").Scan(&n); err != nil || n != 0 {
			t.Fatal("secret persisted", table, n, err)
		}
	}
	status.Store(200)
	code.Store(400)
	err := m.sender.Send(context.Background(), m.record("test", "test", base, base, "test"))
	if err == nil || strings.Contains(err.Error(), key) {
		t.Fatal("Bark application failure accepted/leaked")
	}
}
func telemetryBool(v bool) *bool { return &v }
func TestBarkKeyPermissionsLinksLimitsAndRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bark.key")
	for _, tc := range []struct {
		mode  os.FileMode
		value string
		ok    bool
	}{{0600, "short-bark-key\n", true}, {0644, "secret", false}, {0400, "secret", false}, {0600, "", false}, {0600, "two keys", false}, {0600, strings.Repeat("x", 4097), false}} {
		os.Chmod(path, 0600)
		if err := os.WriteFile(path, []byte(tc.value), 0600); err != nil {
			t.Fatal(err)
		}
		os.Chmod(path, tc.mode)
		_, err := readKey(path)
		if (err == nil) != tc.ok {
			t.Fatal("key validation", tc.mode, err)
		}
	}
	os.Chmod(path, 0600)
	os.WriteFile(path, []byte("rotated-key"), 0600)
	key, err := readKey(path)
	if err != nil || key != "rotated-key" {
		t.Fatal("key did not rotate")
	}
	link := filepath.Join(dir, "link")
	os.Symlink(path, link)
	if _, err = readKey(link); err == nil {
		t.Fatal("followed symlink")
	}
	if _, err = readKey(dir); err == nil {
		t.Fatal("accepted directory")
	}
}
func TestBarkBindingFallbackProxyAndRedirect(t *testing.T) {
	var log bytes.Buffer
	p := config.DefaultAlert("")
	p.BindInterface = "missing-bloomer-interface"
	b := newBark(p, slog.New(slog.NewTextHandler(&log, nil)))
	tr := b.transport()
	defer tr.CloseIdleConnections()
	if tr.Proxy != nil || !strings.Contains(log.String(), "default route") {
		t.Fatal("proxy enabled/fallback unlogged")
	}
	// Exercise an actual IPv4 dial on the loopback interface, including Darwin IP_BOUND_IF.
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback == 0 {
			continue
		}
		_, ip := interfaceIPv4(iface.Name)
		if ip == nil {
			continue
		}
		listener, err := net.Listen("tcp4", net.JoinHostPort(ip.String(), "0"))
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		p.BindInterface = iface.Name
		tr = newBark(p, b.logger).transport()
		defer tr.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		conn, err := tr.DialContext(ctx, "tcp", listener.Addr().String())
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if !conn.LocalAddr().(*net.TCPAddr).IP.Equal(ip) {
			t.Fatal("wrong source address")
		}
		conn.Close()
		break
	}
	keyPath := filepath.Join(t.TempDir(), "bark.key")
	os.WriteFile(keyPath, []byte("private-key"), 0600)
	p.BarkKeyFile = keyPath
	p.BindInterface = ""
	var destinationCalls atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	p.BarkServer = redirect.URL
	if err := newBark(p, b.logger).Send(context.Background(), Record{Level: "active"}); err == nil || destinationCalls.Load() != 0 {
		t.Fatal("redirect forwarded key")
	}
}
