// Package management controls the current user's Bloomer LaunchAgent without a scripting runtime.
package management

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/localfs"
	"bloomer/agent/internal/release"
)

const Label = "dev.bloomer.agent"

type Result = map[string]any
type command func(time.Duration, string, ...string) ([]byte, error)

type Manager struct {
	Home, Base, ConfigPath, Current, Plist, Domain, Job, Tailscale string
	run                                                            command
	health                                                         func() (Result, error)
	probe                                                          func(string) error
	poll, stopTimeout, readyTimeout                                time.Duration
}

func New(home string) *Manager {
	base := filepath.Join(home, "Library/Application Support/Bloomer/agent")
	domain := "gui/" + strconv.Itoa(os.Getuid())
	m := &Manager{Home: home, Base: base, ConfigPath: filepath.Join(base, "config.json"), Current: filepath.Join(base, "bin/current"),
		Plist: filepath.Join(home, "Library/LaunchAgents", Label+".plist"), Domain: domain, Job: domain + "/" + Label,
		run: runCommand, poll: 200 * time.Millisecond, stopTimeout: 45 * time.Second, readyTimeout: 25 * time.Second}
	for _, path := range []string{"/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale", "/Applications/Tailscale.app/Contents/MacOS/Tailscale"} {
		if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
			m.Tailscale = path
			break
		}
	}
	m.health = m.Health
	m.probe = func(address string) error {
		l, err := net.Listen("tcp", address)
		if err != nil {
			return errors.New("agent listen port is unavailable")
		}
		return l.Close()
	}
	return m
}

func runCommand(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	b, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("command failed or timed out: %s", filepath.Base(name))
	}
	return b, nil
}
func (m *Manager) command(name string, args ...string) ([]byte, error) {
	return m.run(30*time.Second, name, args...)
}
func (m *Manager) loaded() bool {
	_, err := m.command("/bin/launchctl", "print", m.Job)
	return err == nil
}
func (m *Manager) settings() (config.Config, error) { return config.Load(m.ConfigPath) }

func (m *Manager) Health() (Result, error) {
	cfg, err := m.settings()
	if err != nil {
		return nil, err
	}
	token, err := config.ReadToken(cfg.AccessTokenFile)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+cfg.Listen+"/v1/health", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("health redirects are not allowed") }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("local health endpoint unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("local health endpoint is not ready")
	}
	var health Result
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&health)
	return health, err
}

func (m *Manager) Stop(disable bool) error {
	if disable {
		if _, err := m.command("/bin/launchctl", "disable", m.Job); err != nil {
			return err
		}
	}
	if m.loaded() {
		if _, err := m.command("/bin/launchctl", "bootout", m.Job); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(m.stopTimeout)
	for {
		free := true
		if _, err := os.Lstat(m.ConfigPath); err == nil {
			cfg, err := m.settings()
			if err != nil {
				return err
			}
			free, err = lockFree(cfg.LockPath())
			if err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if !m.loaded() && free {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("LaunchAgent did not stop and release its database lock")
		}
		time.Sleep(m.poll)
	}
}

func (m *Manager) Start() (Result, error) {
	if _, err := m.command("/bin/launchctl", "enable", m.Job); err != nil {
		return nil, err
	}
	if !m.loaded() {
		if _, err := m.command("/bin/launchctl", "bootstrap", m.Domain, m.Plist); err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(m.readyTimeout)
	for {
		if health, err := m.health(); err == nil {
			return health, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("LaunchAgent did not become healthy; inspect launchctl and agent logs")
		}
		time.Sleep(m.poll)
	}
}

func (m *Manager) Status() (Result, error) {
	if !m.loaded() {
		return Result{"state": "stopped"}, nil
	}
	health, err := m.health()
	if err != nil {
		return nil, err
	}
	out := Result{"state": "running"}
	for _, key := range []string{"version", "data_stale", "last_successful_sync_at", "collection_state"} {
		out[key] = health[key]
	}
	return out, nil
}

type ServeConfig struct {
	TCP map[string]struct{ HTTPS bool } `json:"TCP"`
	Web map[string]struct {
		Handlers map[string]struct{ Proxy string }
	} `json:"Web"`
	AllowFunnel map[string]bool `json:"AllowFunnel"`
}

func CheckServe(cfg ServeConfig, port int, target string) (bool, error) {
	key := strconv.Itoa(port)
	suffix := ":" + key
	for host, enabled := range cfg.AllowFunnel {
		if enabled && strings.HasSuffix(host, suffix) {
			return false, errors.New("requested port is publicly exposed by Funnel")
		}
	}
	webCount := 0
	owned := false
	for host, web := range cfg.Web {
		if !strings.HasSuffix(host, suffix) {
			continue
		}
		webCount++
		if len(web.Handlers) == 1 && web.Handlers["/"].Proxy == target {
			owned = true
		} else {
			return false, errors.New("Tailscale HTTPS port already serves another application")
		}
	}
	tcp, exists := cfg.TCP[key]
	if !exists && webCount == 0 {
		return false, nil
	}
	if exists && tcp.HTTPS && webCount == 1 && owned {
		return true, nil
	}
	return false, errors.New("Tailscale HTTPS port already serves another application")
}
func (m *Manager) tailscaleState() (string, ServeConfig, error) {
	var serve ServeConfig
	if m.Tailscale == "" {
		return "", serve, errors.New("Tailscale CLI unavailable")
	}
	b, err := m.command(m.Tailscale, "status", "--json")
	if err != nil {
		return "", serve, err
	}
	var status struct {
		BackendState string
		Self         struct{ DNSName string }
	}
	if err = json.Unmarshal(b, &status); err != nil {
		return "", serve, errors.New("invalid Tailscale status")
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	if status.BackendState != "Running" || !strings.HasSuffix(dns, ".ts.net") {
		return "", serve, errors.New("Tailscale is not running or HTTPS DNS name is unavailable")
	}
	b, err = m.command(m.Tailscale, "serve", "status", "--json")
	if err != nil {
		return "", serve, err
	}
	if err = json.Unmarshal(b, &serve); err != nil {
		return "", serve, errors.New("invalid Tailscale Serve status")
	}
	return dns, serve, nil
}

// WithLock serializes management mutations separately from the agent's database lock.
func (m *Manager) WithLock(fn func() (Result, error)) (Result, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("LaunchAgent management requires macOS")
	}
	if err := localfs.PrivateDir(m.Base); err != nil {
		return nil, err
	}
	f, err := localfs.OpenPrivate(filepath.Join(m.Base, "management.lock"), syscall.O_RDWR|syscall.O_CREAT)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.New("another management operation is running")
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func (m *Manager) Install(directory string, port int, plan bool) (Result, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("invalid HTTPS port")
	}
	manifest, err := release.Verify(directory)
	if err != nil {
		return nil, err
	}
	version := manifest.Version + "-" + manifest.SHA256["bloomer-agent"][:12]
	releaseDir := filepath.Join(m.Base, "bin", version)
	if plan {
		return Result{"version": manifest.Version, "binary": filepath.Join(releaseDir, "bloomer-agent"), "config": m.ConfigPath, "plist": m.Plist, "https_port": port}, nil
	}
	if runtime.GOOS != "darwin" {
		return nil, errors.New("LaunchAgent installation requires macOS")
	}
	if _, err = m.command("/bin/launchctl", "print", m.Domain); err != nil {
		return nil, err
	}
	dns, before, err := m.tailscaleState()
	if err != nil {
		return nil, err
	}
	for _, path := range []string{m.Base, filepath.Join(m.Base, "bin"), releaseDir, filepath.Dir(m.Plist)} {
		if err = localfs.PrivateDir(path); err != nil {
			return nil, err
		}
	}
	previous := ""
	if st, e := os.Lstat(m.Current); e == nil {
		if st.Mode()&os.ModeSymlink == 0 {
			return nil, errors.New("managed current binary path must be a symlink")
		}
		previous, err = os.Readlink(m.Current)
		if err != nil {
			return nil, err
		}
		resolved, e := filepath.EvalSymlinks(m.Current)
		if e != nil {
			return nil, e
		}
		binRoot, e := filepath.EvalSymlinks(filepath.Join(m.Base, "bin"))
		if e != nil {
			return nil, e
		}
		rel, e := filepath.Rel(binRoot, resolved)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return nil, errors.New("current binary link escapes managed versions")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	oldPlist, err := optionalFile(m.Plist)
	if err != nil {
		return nil, err
	}
	if st, e := os.Lstat(m.ConfigPath); e == nil {
		if !st.Mode().IsRegular() {
			return nil, errors.New("managed configuration must be a regular file")
		}
	} else if errors.Is(e, os.ErrNotExist) {
		b, e := os.ReadFile(filepath.Join(directory, "config.example.json"))
		if e != nil {
			return nil, e
		}
		if err = atomicWrite(m.ConfigPath, b, 0600); err != nil {
			return nil, err
		}
	} else {
		return nil, e
	}
	if err = os.Chmod(m.ConfigPath, 0600); err != nil {
		return nil, err
	}
	cfg, err := m.settings()
	if err != nil {
		return nil, err
	}
	for _, path := range []string{cfg.DataDir, cfg.LogDir} {
		if err = localfs.PrivateDir(path); err != nil {
			return nil, err
		}
	}
	candidate := filepath.Join(releaseDir, "bloomer-agent")
	b, err := os.ReadFile(filepath.Join(directory, "bloomer-agent"))
	if err != nil {
		return nil, err
	}
	if err = atomicWrite(candidate, b, 0755); err != nil {
		return nil, err
	}
	if _, err = m.command(candidate, "-config", m.ConfigPath, "-check"); err != nil {
		return nil, err
	}
	target := "http://" + cfg.Listen
	owned, err := CheckServe(before, port, target)
	if err != nil {
		return nil, err
	}
	tokenBefore, err := optionalFile(cfg.AccessTokenFile)
	if err != nil {
		return nil, err
	}
	wasRunning := m.loaded()
	backup := ""
	published := false
	switched := false
	// All failures after stopping the old service restore its database and running state.
	perform := func() (Result, error) {
		if err := m.Stop(false); err != nil {
			return nil, err
		}
		if err := m.probe(cfg.Listen); err != nil {
			return nil, err
		}
		if _, err := os.Stat(cfg.DatabasePath()); err == nil {
			backupDir := filepath.Join(m.Base, "backups")
			if err = localfs.PrivateDir(backupDir); err != nil {
				return nil, err
			}
			backupPath := filepath.Join(backupDir, fmt.Sprintf("before-upgrade-%d.db", time.Now().UnixNano()))
			oldBinary := candidate
			if previous != "" {
				oldBinary = filepath.Join(m.Current, "bloomer-agent")
			}
			if _, err = m.run(2*time.Minute, oldBinary, "-config", m.ConfigPath, "-backup", backupPath); err != nil {
				return nil, err
			}
			backup = backupPath
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := replaceLink(m.Current, version); err != nil {
			return nil, err
		}
		switched = true
		template, err := os.ReadFile(filepath.Join(directory, "dev.bloomer.agent.plist.template"))
		if err != nil {
			return nil, err
		}
		var escapedBinary, escapedConfig strings.Builder
		if err = xml.EscapeText(&escapedBinary, []byte(filepath.Join(m.Current, "bloomer-agent"))); err != nil {
			return nil, err
		}
		if err = xml.EscapeText(&escapedConfig, []byte(m.ConfigPath)); err != nil {
			return nil, err
		}
		plist := strings.NewReplacer("@BINARY@", escapedBinary.String(), "@CONFIG@", escapedConfig.String()).Replace(string(template))
		if err = atomicWrite(m.Plist, []byte(plist), 0600); err != nil {
			return nil, err
		}
		if _, err = m.Start(); err != nil {
			return nil, err
		}
		if !owned {
			// A timed-out command may have changed Serve. Rollback removes only this previously empty port.
			published = true
			if _, err = m.command(m.Tailscale, "serve", "--bg", "--https="+strconv.Itoa(port), target); err != nil {
				return nil, err
			}
		}
		_, after, err := m.tailscaleState()
		if err != nil {
			return nil, err
		}
		if ok, e := CheckServe(after, port, target); e != nil {
			return nil, e
		} else if !ok {
			return nil, errors.New("Tailscale HTTPS route was not installed")
		}
		if tokenBefore != nil {
			after, err := os.ReadFile(cfg.AccessTokenFile)
			if err != nil {
				return nil, err
			}
			if string(after) != string(tokenBefore) {
				return nil, errors.New("upgrade replaced the dashboard token")
			}
		}
		managementDir := filepath.Join(m.Base, "management")
		if err = localfs.PrivateDir(managementDir); err != nil {
			return nil, err
		}
		ctl, err := os.ReadFile(filepath.Join(directory, "bloomerctl"))
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(filepath.Join(managementDir, "bloomerctl"), ctl, 0700); err != nil {
			return nil, err
		}
		endpoint := "https://" + dns
		if port != 443 {
			endpoint += ":" + strconv.Itoa(port)
		}
		state := Result{"version": manifest.Version, "sha256": manifest.SHA256["bloomer-agent"], "installed_at": time.Now().UTC().Format(time.RFC3339Nano), "endpoint": endpoint, "serve_port": port}
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(filepath.Join(m.Base, "install-state.json"), append(data, '\n'), 0600); err != nil {
			return nil, err
		}
		state["token_preserved"] = tokenBefore != nil
		return state, nil
	}
	result, err := perform()
	if err == nil {
		return result, nil
	}
	// Never touch the database or binary while the new agent still holds its lock.
	if stopErr := m.Stop(false); stopErr != nil {
		return nil, errors.Join(err, fmt.Errorf("rollback could not stop service: %w", stopErr))
	}
	var rollbackErr error
	if published {
		_, e := m.command(m.Tailscale, "serve", "--https="+strconv.Itoa(port), "off")
		rollbackErr = errors.Join(rollbackErr, e)
	}
	if backup != "" {
		_, e := m.run(2*time.Minute, candidate, "-config", m.ConfigPath, "-restore", backup)
		if e != nil {
			return nil, errors.Join(err, rollbackErr, fmt.Errorf("rollback restore failed; backup retained at %s", backup))
		}
	}
	if switched {
		if previous != "" {
			rollbackErr = errors.Join(rollbackErr, replaceLink(m.Current, previous))
		} else {
			rollbackErr = errors.Join(rollbackErr, os.Remove(m.Current))
		}
	}
	if oldPlist != nil {
		rollbackErr = errors.Join(rollbackErr, atomicWrite(m.Plist, oldPlist, 0600))
	} else if e := os.Remove(m.Plist); e != nil && !errors.Is(e, os.ErrNotExist) {
		rollbackErr = errors.Join(rollbackErr, e)
	}
	if wasRunning && previous != "" && rollbackErr == nil {
		_, e := m.Start()
		rollbackErr = errors.Join(rollbackErr, e)
	} else if !wasRunning {
		_, e := m.command("/bin/launchctl", "disable", m.Job)
		rollbackErr = errors.Join(rollbackErr, e)
	}
	return nil, errors.Join(err, rollbackErr)
}

func optionalFile(path string) ([]byte, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("managed file must be regular and not a symlink")
	}
	return os.ReadFile(path)
}

func (m *Manager) Restore(backup string) (Result, error) {
	if !filepath.IsAbs(backup) {
		return nil, errors.New("backup path must be absolute")
	}
	running := m.loaded()
	if err := m.Stop(false); err != nil {
		return nil, err
	}
	if _, err := m.run(2*time.Minute, filepath.Join(m.Current, "bloomer-agent"), "-config", m.ConfigPath, "-restore", backup); err != nil {
		return nil, err
	}
	if running {
		if _, err := m.Start(); err != nil {
			return nil, err
		}
	}
	return Result{"state": "restored"}, nil
}
