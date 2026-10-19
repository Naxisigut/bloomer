package telemetry

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

type commandFunc func(context.Context, string, ...string) ([]byte, error)

// Limit stdout, discard stderr, use fixed executable paths and no shell. Some
// OS commands include process paths in errors; these must never enter the DB.
func command(ctx context.Context, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	cmd.WaitDelay = 100 * time.Millisecond
	var out limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out.Bytes(), err
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errors.New("command output exceeds limit")
	}
	return b.Buffer.Write(p)
}

func quality(err error) string {
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EPERM) {
		return PermissionDenied
	}
	return Unavailable
}
