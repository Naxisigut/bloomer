// Package logging provides bounded private JSON logs without an external rotation dependency.
package logging

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"bloomer/agent/internal/localfs"
)

const (
	MaxFileBytes = 5_000_000
	MaxFiles     = 10 // active file plus nine backups: at most 50 MB
	MaxAge       = 14 * 24 * time.Hour
)

type Writer struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	size     int64
	day      string
	now      func() time.Time
	maxBytes int64
	maxFiles int
}

func New(dir string) (*slog.Logger, *Writer, error) {
	if err := localfs.PrivateDir(dir); err != nil {
		return nil, nil, err
	}
	w := &Writer{path: filepath.Join(dir, "agent.log"), now: time.Now, maxBytes: MaxFileBytes, maxFiles: MaxFiles}
	if err := w.open(); err != nil {
		return nil, nil, err
	}
	if err := w.prune(); err != nil {
		w.Close()
		return nil, nil, err
	}
	logger := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		key := strings.ToLower(a.Key)
		if strings.Contains(key, "token") || strings.Contains(key, "password") || strings.Contains(key, "authorization") || strings.Contains(key, "credential") || strings.Contains(key, "secret") {
			a.Value = slog.StringValue("[REDACTED]")
		}
		return a
	}}))
	return logger, w, nil
}

func (w *Writer) open() error {
	f, err := localfs.OpenPrivate(w.path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_APPEND)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.file = f
	w.size = st.Size()
	w.day = st.ModTime().UTC().Format(time.DateOnly)
	return nil
}
func (w *Writer) backup(i int) string { return w.path + "." + strconv.Itoa(i) }
func (w *Writer) prune() error {
	cutoff := w.now().Add(-MaxAge)
	for i := 1; i < w.maxFiles; i++ {
		p := w.backup(i)
		st, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return errors.New("log backup must be a regular file")
		}
		if st.ModTime().Before(cutoff) {
			if err = os.Remove(p); err != nil {
				return err
			}
		}
	}
	return nil
}
func (w *Writer) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	if err := os.Remove(w.backup(w.maxFiles - 1)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := w.maxFiles - 2; i >= 1; i-- {
		if err := os.Rename(w.backup(i), w.backup(i+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(w.path, w.backup(1)); err != nil {
		return err
	}
	if err := w.open(); err != nil {
		return err
	}
	return w.prune()
}
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	// A single oversized entry is rejected instead of breaking the disk bound.
	if int64(len(p)) > w.maxBytes {
		return 0, errors.New("log entry exceeds file size limit")
	}
	if w.size > 0 && (w.size+int64(len(p)) > w.maxBytes || w.day != w.now().UTC().Format(time.DateOnly)) {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
