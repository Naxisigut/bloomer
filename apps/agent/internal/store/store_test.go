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

	"bloomer/agent/internal/upstream"
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
	if err != nil || version != 2 {
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
	second, err := fs.ReadFile(migrations.Files, "002_collection.sql")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{
		"001_initial.sql":    &fstest.MapFile{Data: initial},
		"002_collection.sql": &fstest.MapFile{Data: second},
		"003_upgrade.sql":    &fstest.MapFile{Data: []byte("CREATE TABLE should_rollback(id INTEGER); INVALID SQL;")},
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
	if count != 0 || v != 2 {
		t.Fatal("migration did not roll back DDL and version")
	}
	backups, _ := filepath.Glob(path + ".pre-v3-*.bak")
	if len(backups) != 1 {
		t.Fatal("upgrade backup missing")
	}
	files["003_upgrade.sql"].Data = []byte("CREATE TABLE upgraded(id INTEGER) STRICT;")
	if err = migrate(ctx, s.writer, files, path); err != nil {
		t.Fatal(err)
	}
	s.writer.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	if v != 3 {
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

func TestP1DatabaseUpgradePreservesCheckpoints(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "p1.db")
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := fs.ReadFile(migrations.Files, "001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, fstest.MapFS{"001_initial.sql": &fstest.MapFile{Data: initial}}, path); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO sync_state(account_id,last_successful_sync_at_us,coverage_status) VALUES('a',1234567,'partial'); UPDATE maintenance_state SET retry_not_before_us=7654321,last_clean_shutdown_at_us=4567890`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err := s.LoadState(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if state.AccountID != "a" || state.LastSuccess.UnixMicro() != 1234567 || state.RetryNotBefore.UnixMicro() != 7654321 || state.LastShutdown.UnixMicro() != 4567890 {
		t.Fatalf("upgrade lost checkpoints: %+v", state)
	}
	backups, _ := filepath.Glob(path + ".pre-v2-*.bak")
	if len(backups) != 1 {
		t.Fatal("P1 upgrade backup missing")
	}
}

func TestFailedAttemptDoesNotBindAnAccount(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	if err := s.RecordAttempt(ctx, "failed-config-account", at, "initial", "first_import"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadState(ctx, "a"); err != nil {
		t.Fatal("uncommitted account became bound:", err)
	}
	if _, err := s.CommitRound(ctx, Round{AccountID: "a", StartedAt: at, CompletedAt: at, Mode: "initial", Summary: upstream.Summary{AccountID: "a"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadState(ctx, "failed-config-account"); !errors.Is(err, ErrAccountMismatch) {
		t.Fatal("successful account was not bound")
	}
	var count int
	if err := s.ReadDB().QueryRow("SELECT count(*) FROM sync_state").Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed account state leaked into bound database: %d %v", count, err)
	}
}

func TestConsistentRestorePreservesBackupAndCurrentRecoveryCopy(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if err := s.Write(ctx, func(tx *sql.Tx) error { return addEarning(ctx, tx, "a", "original") }); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "consistent.db")
	if err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	originalBackup, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Write(ctx, func(tx *sql.Tx) error { return addEarning(ctx, tx, "a", "later") }); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err = Restore(ctx, backup, path); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var count int
	if err = restored.ReadDB().QueryRow("SELECT count(*) FROM earnings").Scan(&count); err != nil || count != 1 {
		t.Fatalf("wrong restored history: %d %v", count, err)
	}
	after, err := os.ReadFile(backup)
	if err != nil || string(after) != string(originalBackup) {
		t.Fatal("restore mutated the source backup")
	}
	recovery, _ := filepath.Glob(path + ".pre-restore-*.bak")
	if len(recovery) != 1 {
		t.Fatal("pre-restore recovery copy missing")
	}
	previous, err := Open(ctx, recovery[0])
	if err != nil {
		t.Fatal(err)
	}
	defer previous.Close()
	if err = previous.ReadDB().QueryRow("SELECT count(*) FROM earnings").Scan(&count); err != nil || count != 2 {
		t.Fatal("pre-restore copy lost later data")
	}
}
func TestRestoreRejectsLiveAndUnsupportedSources(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if err := s.Write(ctx, func(tx *sql.Tx) error { return addEarning(ctx, tx, "a", "live") }); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "destination.db")
	if err := Restore(ctx, path, destination); err == nil {
		t.Fatal("live WAL source accepted")
	}
	backup := filepath.Join(t.TempDir(), "future.db")
	if err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn(backup, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = Restore(ctx, backup, destination); err == nil {
		t.Fatal("future backup accepted")
	}
	if _, err = os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("failed validation changed destination")
	}
}
