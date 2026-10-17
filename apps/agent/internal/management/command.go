package management

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const commandWaitDelay = 2 * time.Second

// Keep JSON stdout separate from diagnostics, and keep a noisy CLI from using
// unbounded memory. Writers still consume all bytes after reaching their limit.
type commandBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *commandBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *commandBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func commandDiagnostic(label string, b *commandBuffer) string {
	data := b.Bytes()
	truncated := b.truncated || len(data) > 4096
	if len(data) > 4096 {
		data = data[:4096]
	}
	message := strings.TrimSpace(strings.ToValidUTF8(string(data), ""))
	if truncated {
		message += "\n[output truncated]"
	}
	if message == "" {
		return ""
	}
	return "\n" + label + ":\n" + message
}

func killCommandGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	// The group contains only this command and its descendants, never the
	// existing Tailscale daemon or launchd-managed agent.
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func runCommand(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killCommandGroup(cmd) }
	cmd.WaitDelay = commandWaitDelay
	stdout := &commandBuffer{limit: 1 << 20}
	stderr := &commandBuffer{limit: 16 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if err == nil && stdout.truncated {
		err = errors.New("command stdout exceeds 1 MiB")
	}
	if err != nil {
		// Also clean up when a wrapper exits before its descendants, so that a
		// failed command cannot change Serve later while rollback is running.
		cleanupErr := killCommandGroup(cmd)
		if errors.Is(cleanupErr, os.ErrProcessDone) {
			cleanupErr = nil
		}
		reason := "command failed"
		if ctx.Err() != nil {
			reason = "command timed out after " + timeout.String()
		}
		return stdout.Bytes(), fmt.Errorf("%s: %s: %w%s%s", reason, filepath.Base(name),
			errors.Join(err, ctx.Err(), cleanupErr), commandDiagnostic("stdout", stdout), commandDiagnostic("stderr", stderr))
	}
	return stdout.Bytes(), nil
}
