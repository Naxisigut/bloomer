// Package store owns SQLite connections and serializes all local writes.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"bloomer/agent/internal/localfs"
	"bloomer/agent/migrations"
	_ "modernc.org/sqlite"
)

type Store struct {
	writer, reader *sql.DB
	writeMu        sync.Mutex
}

func Open(ctx context.Context, path string) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("database path must be absolute")
	}
	if err := localfs.PrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := localfs.OpenPrivate(path, syscall.O_RDWR|syscall.O_CREAT)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	writer, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	s := &Store{writer: writer}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	var mode string
	if err = writer.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return nil, err
	}
	if mode != "wal" {
		return nil, errors.New("database did not enable WAL")
	}
	if err = migrate(ctx, writer, migrations.Files, path); err != nil {
		return nil, err
	}
	s.reader, err = sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, err
	}
	s.reader.SetMaxOpenConns(2)
	s.reader.SetMaxIdleConns(2)
	if err = s.reader.PingContext(ctx); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}

func dsn(path string, readOnly bool) string {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(FULL)")
	if readOnly {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Set("_txlock", "immediate")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ReadDB is query-only on every connection, including replacements in the pool.
// Callers must close rows and propagate request cancellation.
func (s *Store) ReadDB() *sql.DB { return s.reader }

// Write serializes transactional mutations; network I/O must happen before calling it.
func (s *Store) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordStartup(ctx context.Context, at time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE maintenance_state SET last_started_at_us=? WHERE id=1", at.UTC().UnixMicro())
		return err
	})
}
func (s *Store) RecordShutdown(ctx context.Context, at time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE maintenance_state SET last_clean_shutdown_at_us=? WHERE id=1", at.UTC().UnixMicro())
		return err
	})
}

func (s *Store) Backup(ctx context.Context, path string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return backupDB(ctx, s.writer, path)
}

// VACUUM INTO takes a consistent SQLite snapshot including committed WAL contents.
// Backups must be outside an active transaction and never overwrite another file.
func backupDB(ctx context.Context, db *sql.DB, path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("backup path must be absolute")
	}
	f, err := localfs.OpenPrivate(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.reader.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var r, w error
	if s.reader != nil {
		r = s.reader.Close()
	}
	if s.writer != nil {
		w = s.writer.Close()
	}
	return errors.Join(r, w)
}

func (s *Store) Check(ctx context.Context) error {
	var result string
	if err := s.reader.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("database quick_check failed")
	}
	return nil
}
