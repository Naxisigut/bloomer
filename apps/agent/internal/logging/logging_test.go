package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotationRetentionAndRedaction(t *testing.T) {
	dir := t.TempDir()
	logger, w, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("test", "authorization", "do-not-save-me")
	b, _ := os.ReadFile(filepath.Join(dir, "agent.log"))
	if strings.Contains(string(b), "do-not-save-me") || !strings.Contains(string(b), "[REDACTED]") {
		t.Fatal("secret attribute not redacted")
	}
	w.maxBytes = 128
	w.maxFiles = 3
	for i := 0; i < 20; i++ {
		if _, err = w.Write([]byte(strings.Repeat("a", 80))); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	var total int64
	for _, e := range entries {
		st, _ := e.Info()
		total += st.Size()
		if st.Mode().Perm() != 0600 {
			t.Fatal("nonprivate log")
		}
	}
	if len(entries) != 3 || total > 3*128 {
		t.Fatalf("unbounded logs: files=%d bytes=%d", len(entries), total)
	}
	old := time.Now().Add(-MaxAge - time.Hour)
	os.Chtimes(w.backup(1), old, old)
	if err = w.prune(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(w.backup(1)); !os.IsNotExist(err) {
		t.Fatal("old backup retained")
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("x")); err == nil {
		t.Fatal("closed log accepted write")
	}
}
