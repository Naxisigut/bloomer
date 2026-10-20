package control

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 2<<20 {
		return 0, errors.New("output limit")
	}
	return b.Buffer.Write(p)
}
func command(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "DARKBLOOM_NO_UPDATE_CHECK=1", "TERM=dumb")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var out boundedOutput
	// Only discovery JSON is captured. Downloads can emit unbounded progress.
	if len(args) > 1 && args[0] == "models" && (args[1] == "list" || args[1] == "catalog") {
		cmd.Stdout = &out
	} else {
		cmd.Stdout = io.Discard
	}
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return out.Bytes(), err
}
