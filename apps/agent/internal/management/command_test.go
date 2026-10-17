package management

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunCommandPreservesDiagnosticsAndJSON(t *testing.T) {
	output, err := runCommand(time.Second, "/bin/sh", "-c", `printf '{"ok":true}'; printf 'warning' >&2`)
	if err != nil || string(output) != `{"ok":true}` {
		t.Fatalf("stderr contaminated JSON: %q %v", output, err)
	}
	output, err = runCommand(time.Second, "/bin/sh", "-c", `printf 'https://login.tailscale.com/example'; printf 'HTTPS authorization required' >&2; exit 7`)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("lost exit status: %v", err)
	}
	for _, want := range []string{"https://login.tailscale.com/example", "HTTPS authorization required"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("lost diagnostic %q: %v (stdout %q)", want, err, output)
		}
	}
}

func assertChildStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		// An orphan may briefly remain as a zombie before the OS reaps it.
		state, err := exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(state) == 0 && len(exitErr.Stderr) == 0 {
				return // ps reports no matching process.
			}
			t.Fatalf("could not verify child %d: %v", pid, err)
		}
		if strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("command child %d is still running", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func childPID(t *testing.T, output []byte) int {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || pid <= 1 {
		t.Fatalf("invalid child PID: %q", output)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

func TestRunCommandTimeoutKillsWrapperAndChild(t *testing.T) {
	start := time.Now()
	output, err := runCommand(250*time.Millisecond, "/bin/sh", "-c", `/bin/sleep 30 & printf '%s\n' "$!"; wait`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost timeout cause: %v", err)
	}
	pid := childPID(t, output)
	if time.Since(start) > commandWaitDelay+2*time.Second {
		t.Fatalf("command ignored deadline: %s", time.Since(start))
	}
	assertChildStopped(t, pid)
}

// A detached descendant cannot be killed through the wrapper's process group.
// It deliberately inherits both pipes to exercise the independent WaitDelay
// fallback. The calling test explicitly cleans up this synthetic descendant.
func TestCommandProcessHelper(t *testing.T) {
	switch os.Getenv("BLOOMER_COMMAND_TEST_HELPER") {
	case "child":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "wrapper":
		cmd := exec.Command(os.Args[0], "-test.run=^TestCommandProcessHelper$")
		cmd.Env = append(os.Environ(), "BLOOMER_COMMAND_TEST_HELPER=child")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println(cmd.Process.Pid)
		os.Exit(0)
	}
}

func TestRunCommandWaitDelayBoundsInheritedPipes(t *testing.T) {
	t.Setenv("BLOOMER_COMMAND_TEST_HELPER", "wrapper")
	start := time.Now()
	output, err := runCommand(10*time.Second, os.Args[0], "-test.run=^TestCommandProcessHelper$")
	_ = childPID(t, output)
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("expected pipe deadline: %v", err)
	}
	if time.Since(start) > commandWaitDelay+3*time.Second {
		t.Fatalf("inherited pipes kept command waiting: %s", time.Since(start))
	}
}

func TestRunCommandLimitsOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "noisy-output")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", (1<<20)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := runCommand(time.Second, "/bin/cat", path)
	if err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") || len(output) != 1<<20 || len(err.Error()) > 5000 {
		t.Fatalf("unbounded command output: bytes=%d error=%v", len(output), err)
	}
}
