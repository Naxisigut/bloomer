package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type migration struct {
	version             int
	name, sql, checksum string
}

func loadMigrations(files fs.FS) ([]migration, error) {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var result []migration
	for i, name := range names {
		prefix, _, ok := strings.Cut(filepath.Base(name), "_")
		n, err := strconv.Atoi(prefix)
		if !ok || err != nil || n != i+1 {
			return nil, errors.New("migration versions must be contiguous starting at 1")
		}
		body, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, err
		}
		result = append(result, migration{n, name, string(body), fmt.Sprintf("%x", sha256.Sum256(body))})
	}
	if len(result) == 0 {
		return nil, errors.New("no embedded migrations")
	}
	return result, nil
}

// migrate is called with the process lock held and no other writers running.
// It backs up an existing schema before upgrading. Each SQL file and its version
// are committed together; a failed file rolls back without advancing the version.
func migrate(ctx context.Context, db *sql.DB, files fs.FS, path string) error {
	ms, err := loadMigrations(files)
	if err != nil {
		return err
	}
	var version int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(ms) {
		return fmt.Errorf("database schema %d is newer than supported schema %d", version, len(ms))
	}
	if version == 0 {
		var tables int
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return err
		}
		if tables != 0 {
			return errors.New("unversioned nonempty database cannot be adopted")
		}
	}
	if version > 0 {
		rows, err := db.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations ORDER BY version")
		if err != nil {
			return errors.New("database migration history is missing")
		}
		count := 0
		for rows.Next() {
			var v int
			var checksum string
			if err = rows.Scan(&v, &checksum); err != nil {
				rows.Close()
				return err
			}
			count++
			if v != count || v > version || checksum != ms[v-1].checksum {
				rows.Close()
				return errors.New("database migration history or checksum mismatch")
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if count != version {
			return errors.New("database migration version mismatch")
		}
	}
	if version == len(ms) {
		return nil
	}
	if version > 0 {
		backup := fmt.Sprintf("%s.pre-v%d-%d.bak", path, version+1, time.Now().UTC().UnixNano())
		if err = backupDB(ctx, db, backup); err != nil {
			return fmt.Errorf("pre-migration backup failed: %w", err)
		}
	}
	for _, m := range ms[version:] {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		applyErr := func() error {
			if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY, name TEXT NOT NULL, checksum TEXT NOT NULL, applied_at_us INTEGER NOT NULL
   ) STRICT`); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES(?,?,?,?)", m.version, m.name, m.checksum, time.Now().UTC().UnixMicro()); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version))
			return err
		}()
		if applyErr != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s failed: %w", m.name, applyErr)
		}
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", m.name, err)
		}
	}
	return nil
}
