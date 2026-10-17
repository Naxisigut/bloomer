package management

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/release"
)

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func testPackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range release.Files {
		data := []byte("synthetic " + name)
		if name == "config.example.json" {
			data = []byte(`{"collection_enabled":false}`)
		}
		if name == "dev.bloomer.agent.plist.template" {
			var err error
			data, err = os.ReadFile("../../packaging/macos/" + name)
			if err != nil {
				t.Fatal(err)
			}
		}
		write(t, filepath.Join(dir, name), data)
	}
	if err := release.Write(dir, "1.0.0", "test-build"); err != nil {
		t.Fatal(err)
	}
	return dir
}
func testManager(t *testing.T) *Manager {
	t.Helper()
	home := filepath.Join(t.TempDir(), "user & test")
	t.Setenv("HOME", home)
	m := New(home)
	m.poll = time.Millisecond
	m.stopTimeout = 20 * time.Millisecond
	m.readyTimeout = 20 * time.Millisecond
	m.probe = func(string) error { return nil }
	m.health = func() (Result, error) { return Result{"version": "test", "data_stale": false}, nil }
	m.Tailscale = "/synthetic/tailscale"
	return m
}
func existingInstall(t *testing.T, m *Manager) {
	t.Helper()
	cfg, err := config.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	cfg.CollectionEnabled = false
	cfg.AccessTokenFile = filepath.Join(m.Base, "access.token")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	write(t, m.ConfigPath, data)
	write(t, filepath.Join(m.Base, "bin/old/bloomer-agent"), []byte("old executable"))
	if err = os.Symlink("old", m.Current); err != nil {
		t.Fatal(err)
	}
	write(t, m.Plist, []byte("original plist"))
	write(t, cfg.DatabasePath(), []byte("original database"))
	write(t, cfg.AccessTokenFile, []byte(strings.Repeat("a", 64)+"\n"))
}

type simulation struct {
	running, published, failPublish, failCheck bool
	commands                                   []string
}

func (s *simulation) attach(t *testing.T, m *Manager) {
	t.Helper()
	m.run = func(_ time.Duration, name string, args ...string) ([]byte, error) {
		command := name + " " + strings.Join(args, " ")
		s.commands = append(s.commands, command)
		if name == "/bin/launchctl" {
			switch args[0] {
			case "print":
				if args[1] == m.Job && !s.running {
					return nil, errors.New("not loaded")
				}
			case "bootout":
				s.running = false
			case "bootstrap":
				s.running = true
			}
			return nil, nil
		}
		if name == m.Tailscale {
			if args[0] == "status" {
				return []byte(`{"BackendState":"Running","Self":{"DNSName":"device.example.ts.net."}}`), nil
			}
			if len(args) > 1 && args[1] == "status" {
				if !s.published {
					return []byte(`{}`), nil
				}
				return []byte(`{"TCP":{"8443":{"HTTPS":true}},"Web":{"device.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:17890"}}}}}`), nil
			}
			if args[1] == "--bg" {
				s.published = true
				write(t, filepath.Join(m.Base, "agent.db"), []byte("new version database mutation"))
				if s.failPublish {
					return nil, errors.New("synthetic publication failure")
				}
			} else if args[len(args)-1] == "off" {
				s.published = false
			}
			return nil, nil
		}
		for i, arg := range args {
			if arg == "-check" && s.failCheck {
				return nil, errors.New("preflight failed")
			}
			if arg == "-backup" {
				b, err := os.ReadFile(filepath.Join(m.Base, "agent.db"))
				if err != nil {
					return nil, err
				}
				write(t, args[i+1], b)
			}
			if arg == "-restore" {
				b, err := os.ReadFile(args[i+1])
				if err != nil {
					return nil, err
				}
				write(t, filepath.Join(m.Base, "agent.db"), b)
			}
		}
		return nil, nil
	}
}

func TestPlanAndPackageValidation(t *testing.T) {
	m := testManager(t)
	dir := testPackage(t)
	m.run = func(time.Duration, string, ...string) ([]byte, error) {
		t.Fatal("plan executed a system command")
		return nil, nil
	}
	plan, err := m.Install(dir, 8443, true)
	if err != nil || plan["version"] != "1.0.0" {
		t.Fatalf("plan: %v %v", plan, err)
	}
	if _, err = os.Stat(m.Home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("plan changed user home")
	}
	write(t, filepath.Join(dir, "bloomer-agent"), []byte("tampered"))
	if _, err = m.Install(dir, 8443, true); err == nil {
		t.Fatal("accepted tampered package")
	}
	dir = testPackage(t)
	path := filepath.Join(dir, "bloomerctl")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("bloomer-agent", path); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Install(dir, 8443, true); err == nil {
		t.Fatal("accepted symlink package file")
	}
	dir = testPackage(t)
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "manifest.json"), []byte(strings.Replace(string(data), `"bloomerctl":`, `"../bloomerctl":`, 1)))
	if _, err = m.Install(dir, 8443, true); err == nil {
		t.Fatal("accepted unsafe manifest path")
	}
}

func TestServeOwnership(t *testing.T) {
	target := "http://127.0.0.1:17890"
	for _, tc := range []struct {
		name, body  string
		owned, fail bool
	}{
		{"empty", `{}`, false, false},
		{"owned", `{"TCP":{"8443":{"HTTPS":true}},"Web":{"host.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:17890"}}}}}`, true, false},
		{"other", `{"TCP":{"8443":{"HTTPS":true}},"Web":{"host.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9999"}}}}}`, false, true},
		{"tcp", `{"TCP":{"8443":{}}}`, false, true},
		{"other path", `{"TCP":{"8443":{"HTTPS":true}},"Web":{"host.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:17890"},"/other":{"Proxy":"http://127.0.0.1:9999"}}}}}`, false, true},
		{"funnel", `{"AllowFunnel":{"host.ts.net:8443":true}}`, false, true},
		{"unrelated", `{"TCP":{"443":{"HTTPS":true}}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg ServeConfig
			if err := json.Unmarshal([]byte(tc.body), &cfg); err != nil {
				t.Fatal(err)
			}
			owned, err := CheckServe(cfg, 8443, target)
			if owned != tc.owned || (err != nil) != tc.fail {
				t.Fatalf("owned=%v err=%v", owned, err)
			}
		})
	}
}

func TestUpgradeRollback(t *testing.T) {
	m := testManager(t)
	existingInstall(t, m)
	dir := testPackage(t)
	s := simulation{running: true, failPublish: true}
	s.attach(t, m)
	if _, err := m.Install(dir, 8443, false); err == nil || !strings.Contains(err.Error(), "synthetic publication failure") {
		t.Fatalf("expected publication failure: %v", err)
	}
	link, err := os.Readlink(m.Current)
	if err != nil || link != "old" {
		t.Fatalf("link=%q %v", link, err)
	}
	for path, want := range map[string]string{m.Plist: "original plist", filepath.Join(m.Base, "agent.db"): "original database", filepath.Join(m.Base, "access.token"): strings.Repeat("a", 64) + "\n"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("not restored: %s %s %v", path, got, err)
		}
	}
	if !s.running || s.published {
		t.Fatalf("rollback state: %+v", s)
	}
	if !strings.Contains(strings.Join(s.commands, "\n"), "-restore") {
		t.Fatal("rollback did not restore database")
	}
}

func TestInstallAndUpgradePreserveConfigAndToken(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		t.Run(fmt.Sprintf("fresh=%v", fresh), func(t *testing.T) {
			m := testManager(t)
			if !fresh {
				existingInstall(t, m)
			}
			dir := testPackage(t)
			beforeConfig, _ := os.ReadFile(m.ConfigPath)
			beforeToken, _ := os.ReadFile(filepath.Join(m.Base, "access.token"))
			s := simulation{running: !fresh}
			s.attach(t, m)
			result, err := m.Install(dir, 8443, false)
			if err != nil {
				t.Fatal(err)
			}
			if !s.running || !s.published || result["endpoint"] != "https://device.example.ts.net:8443" {
				t.Fatalf("bad installed state: %v", result)
			}
			if !fresh {
				after, _ := os.ReadFile(m.ConfigPath)
				if string(after) != string(beforeConfig) {
					t.Fatal("config changed")
				}
				after, _ = os.ReadFile(filepath.Join(m.Base, "access.token"))
				if string(after) != string(beforeToken) {
					t.Fatal("token changed")
				}
			}
			plist, err := os.ReadFile(m.Plist)
			if err != nil {
				t.Fatal(err)
			}
			var document struct{ XMLName xml.Name }
			if err = xml.Unmarshal(plist, &document); err != nil {
				t.Fatalf("unescaped plist path: %v", err)
			}
			if !strings.Contains(string(plist), "user &amp; test") {
				t.Fatal("plist did not escape path")
			}
			for _, path := range []string{m.ConfigPath, m.Plist, filepath.Join(m.Base, "management/bloomerctl"), filepath.Join(m.Base, "install-state.json")} {
				st, err := os.Stat(path)
				if err != nil || st.Mode().Perm()&0077 != 0 {
					t.Fatalf("not private: %s %v", path, err)
				}
			}
			status, err := m.Status()
			if err != nil || status["state"] != "running" {
				t.Fatalf("status: %v %v", status, err)
			}
			if err = m.Stop(true); err != nil || s.running {
				t.Fatalf("stop: %v", err)
			}
			if _, err = m.Start(); err != nil || !s.running {
				t.Fatalf("start: %v", err)
			}
		})
	}
}

func TestPreflightFailureLeavesOldServiceRunning(t *testing.T) {
	m := testManager(t)
	existingInstall(t, m)
	s := simulation{running: true, failCheck: true}
	s.attach(t, m)
	if _, err := m.Install(testPackage(t), 8443, false); err == nil {
		t.Fatal("expected preflight error")
	}
	for _, command := range s.commands {
		if strings.Contains(command, "bootout") || strings.Contains(command, " serve --bg") {
			t.Fatalf("preflight changed service: %s", command)
		}
	}
	if !s.running {
		t.Fatal("old service stopped")
	}
}

func TestStopWaitsForDatabaseLock(t *testing.T) {
	m := testManager(t)
	existingInstall(t, m)
	s := simulation{running: true}
	s.attach(t, m)
	file, err := os.OpenFile(filepath.Join(m.Base, "agent.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err = m.Stop(false); err == nil {
		t.Fatal("stop reported success while lock held")
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err = m.Stop(false); err != nil {
		t.Fatal(err)
	}
}

func TestHealthRejectsRedirectWithoutLeakingToken(t *testing.T) {
	m := testManager(t)
	existingInstall(t, m)
	called := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; t.Error("followed health redirect") }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("missing auth")
		}
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	cfg, err := m.settings()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Listen = strings.TrimPrefix(source.URL, "http://")
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	write(t, m.ConfigPath, b)
	if _, err = m.Health(); err == nil {
		t.Fatal("accepted redirect")
	}
	if called {
		t.Fatal("token sent to redirect")
	}
}
