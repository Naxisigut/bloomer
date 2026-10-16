package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProcessLock(t *testing.T) {
	if path := os.Getenv("BLOOMER_TEST_LOCK"); path != "" {
		lock, err := AcquireLock(path)
		if err == nil {
			lock.Close()
			os.Exit(1)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "agent.lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessLock$")
	cmd.Env = append(os.Environ(), "BLOOMER_TEST_LOCK="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("second process obtained lock: %s %v", out, err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := AcquireLock(path)
	if err != nil {
		t.Fatal("stale file blocked new instance:", err)
	}
	next.Close()
	link := filepath.Join(t.TempDir(), "symlink.lock")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if lock, err := AcquireLock(link); err == nil {
		lock.Close()
		t.Fatal("symlink lock accepted")
	}
}
