package control

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCommandCancellationTerminatesCLIAndChildren(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := command(ctx, "/bin/sh", "-c", "sleep 30 & wait")
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation failed: %v (%s)", err, time.Since(start))
	}
}
func TestCommandNeverReturnsControlOutputOrRawErrors(t *testing.T) {
	body, err := command(context.Background(), "/bin/sh", "-c", "echo private-output; echo private-token >&2; exit 1")
	if err == nil || len(body) != 0 || strings.Contains(err.Error(), "private") {
		t.Fatalf("command output exposed: %q %v", body, err)
	}
}
