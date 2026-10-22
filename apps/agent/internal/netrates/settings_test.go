package netrates

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSettingsDefaultAndPersist(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	s := NewSettings(dir, true, nil)
	s.Load(now)
	on, since, _, backoff, failures := s.Snapshot()
	if !on || since == nil || failures != 0 || backoff != nil {
		t.Fatal(on, since, failures, backoff)
	}
	if err := s.SetAutoSwitch(false, now); err != nil {
		t.Fatal(err)
	}
	on, since, _, _, _ = s.Snapshot()
	if on || since != nil {
		t.Fatal(on, since)
	}
	body, err := os.ReadFile(filepath.Join(dir, "network-rates.json"))
	if err != nil || len(body) == 0 {
		t.Fatal(err, body)
	}
}

func TestSettingsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "network-rates.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	s := NewSettings(dir, false, nil)
	s.Load(now)
	on, since, _, _, _ := s.Snapshot()
	if on || since != nil {
		t.Fatal(on, since)
	}
}

func TestBackoffGrowthAndCap(t *testing.T) {
	if backoffDuration(1) != 30*time.Minute {
		t.Fatal(backoffDuration(1))
	}
	if backoffDuration(2) != time.Hour {
		t.Fatal(backoffDuration(2))
	}
	if backoffDuration(10) != 6*time.Hour {
		t.Fatal(backoffDuration(10))
	}
}
