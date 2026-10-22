package netrates

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"bloomer/agent/internal/localfs"
)

type settingsPayload struct {
	AutoSwitch      bool       `json:"auto_switch"`
	AutoSwitchSince *time.Time `json:"auto_switch_since,omitempty"`
	LastSwitchAt    *time.Time `json:"last_switch_at,omitempty"`
	Failures        int        `json:"failures"`
	BackoffUntil    *time.Time `json:"backoff_until,omitempty"`
}

type Settings struct {
	mu           sync.Mutex
	path         string
	defaultAuto  bool
	log          *slog.Logger
	autoSwitch   bool
	since        *time.Time
	lastSwitchAt *time.Time
	failures     int
	backoffUntil *time.Time
}

func NewSettings(dataDir string, defaultAuto bool, log *slog.Logger) *Settings {
	if log == nil {
		log = slog.Default()
	}
	return &Settings{path: filepath.Join(dataDir, "network-rates.json"), defaultAuto: defaultAuto, log: log}
}

func (s *Settings) Load(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoSwitch = s.defaultAuto
	s.since = nil
	s.lastSwitchAt = nil
	s.failures = 0
	s.backoffUntil = nil
	f, err := localfs.OpenPrivate(s.path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		if s.autoSwitch {
			t := now.UTC()
			s.since = &t
		}
		return
	}
	if err != nil {
		s.log.Warn("network rates settings unavailable", "error", "corrupt")
		if s.autoSwitch {
			t := now.UTC()
			s.since = &t
		}
		return
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, 8193))
	var p settingsPayload
	if err != nil || len(body) > 8192 || json.Unmarshal(body, &p) != nil || p.Failures < 0 {
		s.log.Warn("network rates settings unavailable", "error", "corrupt")
		if s.autoSwitch {
			t := now.UTC()
			s.since = &t
		}
		return
	}
	s.autoSwitch = p.AutoSwitch
	s.since = cloneTime(p.AutoSwitchSince)
	s.lastSwitchAt = cloneTime(p.LastSwitchAt)
	s.failures = p.Failures
	s.backoffUntil = cloneTime(p.BackoffUntil)
	if s.autoSwitch && s.since == nil {
		t := now.UTC()
		s.since = &t
	}
}

func (s *Settings) Snapshot() (autoSwitch bool, since, lastSwitch, backoff *time.Time, failures int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.autoSwitch, cloneTime(s.since), cloneTime(s.lastSwitchAt), cloneTime(s.backoffUntil), s.failures
}

func (s *Settings) SetAutoSwitch(on bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoSwitch = on
	if on {
		t := now.UTC()
		s.since = &t
	} else {
		s.since = nil
	}
	return s.persistLocked()
}

func (s *Settings) SetLastSwitchAt(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := now.UTC()
	s.lastSwitchAt = &t
	return s.persistLocked()
}

func (s *Settings) RecordFailure(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures++
	until := now.UTC().Add(backoffDuration(s.failures))
	s.backoffUntil = &until
	return s.persistLocked()
}

func (s *Settings) ResetFailures() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = 0
	s.backoffUntil = nil
	return s.persistLocked()
}

func (s *Settings) persistLocked() error {
	body, err := json.Marshal(settingsPayload{
		AutoSwitch: s.autoSwitch, AutoSwitchSince: s.since, LastSwitchAt: s.lastSwitchAt,
		Failures: s.failures, BackoffUntil: s.backoffUntil,
	})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".network-rates-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}

func backoffDuration(failures int) time.Duration {
	if failures < 1 {
		return 0
	}
	d := 30 * time.Minute
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= 6*time.Hour {
			return 6 * time.Hour
		}
	}
	if d > 6*time.Hour {
		return 6 * time.Hour
	}
	return d
}

func cloneTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	t := v.UTC()
	return &t
}
