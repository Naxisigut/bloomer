package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"bloomer/agent/migrations"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private data #1", "agent.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}
func addEarning(ctx context.Context, tx *sql.Tx, account, id string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO earnings VALUES(?,?, 'p', 'pk', 'job', 'model', -12, 0, 1, 1780000000123456)`, account, id)
	return err
}
func TestSchemaConnectionsAndAtomicWrites(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	version, err := s.SchemaVersion(ctx)
	if err != nil || version != 1 {
		t.Fatalf("schema: %d %v", version, err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal("database is not private")
	}
	for _, pool := range []*sql.DB{s.writer, s.reader} {
		count := pool.Stats().MaxOpenConnections
		var held []*sql.Conn
		for i := 0; i < count; i++ {
			c, err := pool.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			held = append(held, c)
			for name, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 5000, "synchronous": 2} {
				var got int
				if err = c.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil || got != want {
					t.Fatalf("connection pragma %s=%d: %v", name, got, err)
				}
			}
		}
		for _, c := range held {
			c.Close()
		}
	}
	if _, err = s.reader.ExecContext(ctx, "DELETE FROM earnings"); err == nil {
		t.Fatal("read pool allowed mutation")
	}
	s.reader.SetMaxIdleConns(0)
	if _, err = s.reader.ExecContext(ctx, "DELETE FROM earnings"); err == nil {
		t.Fatal("new read connection allowed mutation")
	}
	err = s.Write(ctx, func(tx *sql.Tx) error {
		if err := addEarning(ctx, tx, "a", "same-id"); err != nil {
			return err
		}
		return addEarning(ctx, tx, "b", "same-id")
	})
	if err != nil {
		t.Fatal(err)
	}
	fail := errors.New("simulated commit-stage failure")
	err = s.Write(ctx, func(tx *sql.Tx) error {
		if err := addEarning(ctx, tx, "a", "rollback-id"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO sync_state(account_id,last_successful_sync_at_us) VALUES('a',42)"); err != nil {
			return err
		}
		return fail
	})
	if !errors.Is(err, fail) {
		t.Fatal(err)
	}
	var count int
	if err = s.reader.QueryRowContext(ctx, "SELECT count(*) FROM earnings").Scan(&count); err != nil || count != 2 {
		t.Fatalf("partial transaction persisted: %v", err)
	}
	if err = s.reader.QueryRowContext(ctx, "SELECT count(*) FROM sync_state").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed transaction advanced checkpoint: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = s.Write(cancelled, func(tx *sql.Tx) error { return addEarning(ctx, tx, "a", "cancelled") }); err == nil {
		t.Fatal("cancelled write succeeded")
	}
	if err = s.Check(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestBackupIncludesWALAndCheckpoints(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	at := time.UnixMicro(1800000000123456)
	if _, err := s.writer.ExecContext(ctx, "PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { return addEarning(ctx, tx, "a", "in-wal") }); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordStartup(ctx, at); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordShutdown(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(ctx, path); err == nil {
		t.Fatal("backup overwrote an existing file")
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var record string
	if err = restored.ReadDB().QueryRowContext(ctx, "SELECT record_id FROM earnings").Scan(&record); err != nil || record != "in-wal" {
		t.Fatalf("WAL content missing: %s %v", record, err)
	}
	var start, stop int64
	if err = restored.ReadDB().QueryRowContext(ctx, "SELECT last_started_at_us,last_clean_shutdown_at_us FROM maintenance_state").Scan(&start, &stop); err != nil || start != at.UnixMicro() || stop != at.Add(time.Second).UnixMicro() {
		t.Fatalf("checkpoint mismatch: %v", err)
	}
}
func TestMigrationRollbackUpgradeAndVersionRejection(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	initial, err := fs.ReadFile(migrations.Files, "001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{
		"001_initial.sql": &fstest.MapFile{Data: initial},
		"002_upgrade.sql": &fstest.MapFile{Data: []byte("CREATE TABLE should_rollback(id INTEGER); INVALID SQL;")},
	}
	if err = migrate(ctx, s.writer, files, path); err == nil {
		t.Fatal("broken migration succeeded")
	}
	var count, v int
	if err = s.writer.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='should_rollback'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err = s.writer.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if count != 0 || v != 1 {
		t.Fatal("migration did not roll back DDL and version")
	}
	backups, _ := filepath.Glob(path + ".pre-v2-*.bak")
	if len(backups) != 1 {
		t.Fatal("upgrade backup missing")
	}
	files["002_upgrade.sql"].Data = []byte("CREATE TABLE upgraded(id INTEGER) STRICT;")
	if err = migrate(ctx, s.writer, files, path); err != nil {
		t.Fatal(err)
	}
	s.writer.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	if v != 2 {
		t.Fatal("schema version did not advance")
	}
	if _, err = s.writer.ExecContext(ctx, "PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(ctx, path); err == nil {
		reopened.Close()
		t.Fatal("future database was downgraded")
	} else if !strings.Contains(err.Error(), "newer") {
		t.Fatal(err)
	}
}
func TestMigrationChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.writer.ExecContext(ctx, "UPDATE schema_migrations SET checksum='tampered'"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, s.writer, migrations.Files, path); err == nil {
		t.Fatal("tampered migration history accepted")
	}
}

func TestFailedInitialMigrationLeavesEmptyDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "initial.db")
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	broken := fstest.MapFS{"001_initial.sql": &fstest.MapFile{Data: []byte("CREATE TABLE transient(id INTEGER); INVALID SQL;")}}
	if err = migrate(ctx, db, broken, path); err == nil {
		t.Fatal("broken initial migration succeeded")
	}
	var tables, version int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || version != 0 {
		t.Fatal("failed initial migration left partial schema")
	}
	if err = migrate(ctx, db, migrations.Files, path); err != nil {
		t.Fatal("initial migration could not be retried:", err)
	}
}

func TestRejectUnversionedForeignDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "foreign.db")
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "CREATE TABLE foreign_data(id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(ctx, path); err == nil {
		s.Close()
		t.Fatal("foreign database silently adopted")
	}
}
