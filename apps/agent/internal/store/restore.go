package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"bloomer/agent/internal/localfs"
	"bloomer/agent/migrations"
)

// Restore requires the caller to hold the process lock and stop the service.
// Only standalone consistent backups are accepted; live WAL databases are rejected.
func Restore(ctx context.Context, source, destination string) error {
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) || filepath.Clean(source) == filepath.Clean(destination) {
		return errors.New("restore requires distinct absolute source and destination paths")
	}
	st, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("restore source must be a regular standalone database")
	}
	if wal, err := os.Stat(source + "-wal"); err == nil && wal.Size() > 0 {
		return errors.New("restore source has a WAL; create a consistent backup first")
	}
	if err = localfs.PrivateDir(filepath.Dir(destination)); err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: source}
	u.RawQuery = "mode=ro&immutable=1"
	backup, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer backup.Close()
	var integrity string
	if err = backup.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("restore backup integrity check failed")
	}
	var version int
	if err = backup.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	ms, err := loadMigrations(migrations.Files)
	if err != nil {
		return err
	}
	if version < 1 || version > len(ms) {
		return errors.New("restore schema is unsupported")
	}
	rows, err := backup.QueryContext(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var n int
		var sum string
		if err = rows.Scan(&n, &sum); err != nil {
			rows.Close()
			return err
		}
		count++
		if n != count || n > version || sum != ms[n-1].checksum {
			rows.Close()
			return errors.New("restore migration history mismatch")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if count != version {
		return errors.New("restore migration history incomplete")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temp := fmt.Sprintf("%s.restore-%d.tmp", destination, time.Now().UnixNano())
	output, err := localfs.OpenPrivate(temp, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	if _, err = io.Copy(output, input); err == nil {
		err = output.Sync()
	}
	closeErr := output.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Preserve a consistent pre-restore copy before replacing any current files.
	suffix := fmt.Sprintf(".pre-restore-%d.bak", time.Now().UnixNano())
	if _, err = os.Stat(destination); err == nil {
		current, err := Open(ctx, destination)
		if err != nil {
			return err
		}
		err = current.Backup(ctx, destination+suffix)
		closeErr = current.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, ending := range []string{"-wal", "-shm"} {
		if _, err = os.Lstat(destination + ending); err == nil {
			if err = os.Rename(destination+ending, destination+ending+suffix); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err = os.Rename(temp, destination); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
