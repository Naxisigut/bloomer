package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/telemetry"
	"bloomer/agent/migrations"
)

func TestDeviceIdentityHistoryPaginationAndRetention(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	now := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC)
	id, err := s.SaveDeviceIdentity(ctx, telemetry.Hardware{OS: "darwin", Chip: telemetry.Ptr("Apple M4")}, now)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.SaveDeviceIdentity(ctx, telemetry.Hardware{OS: "darwin"}, now.Add(time.Minute))
	if err != nil || id2 != id {
		t.Fatal("installation identity changed")
	}
	identity, err := s.DeviceIdentity(ctx)
	if err != nil || identity.Hardware.Chip == nil || *identity.Hardware.Chip != "Apple M4" {
		t.Fatal("partial hardware probe erased known metadata")
	}
	for _, at := range []time.Time{now.Add(-config.Retention - time.Second), now.Add(-config.Retention), now.Add(-time.Minute), now} {
		v := telemetry.NewSample(at)
		v.DeviceID = id
		v.CPUPercent = telemetry.Ptr(float64(0))
		v.Quality["cpu_percent"] = telemetry.OK
		v.ProviderRunning = telemetry.Ptr(false)
		v.Quality["provider_running"] = telemetry.OK
		if err = s.SaveDeviceSample(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.DeviceSamples(ctx, now, now.Add(-2*config.Retention), now.Add(time.Second), 0, 2)
	if err != nil || len(page.Items) != 2 || page.NextBeforeID == nil {
		t.Fatalf("first page: %+v %v", page, err)
	}
	v := page.Items[0]
	if v.CPUPercent == nil || *v.CPUPercent != 0 || v.ProviderRunning == nil || *v.ProviderRunning || v.GPUPercent != nil || v.Quality["gpu_percent"] != telemetry.Unsupported {
		t.Fatal("NULL and zero/false did not round trip")
	}
	second, err := s.DeviceSamples(ctx, now, now.Add(-2*config.Retention), now.Add(time.Second), *page.NextBeforeID, 2)
	if err != nil || len(second.Items) != 1 || second.NextBeforeID != nil || !second.Items[0].CollectedAt.Equal(now.Add(-config.Retention)) {
		t.Fatalf("retention or paging: %+v %v", second, err)
	}
	done, err := s.CleanupBatch(ctx, now.Add(-config.Retention), now, 1)
	if err != nil || done {
		t.Fatal("full telemetry deletion batch should yield")
	}
	done, err = s.CleanupBatch(ctx, now.Add(-config.Retention), now, 1)
	if err != nil || !done {
		t.Fatal("cleanup did not finish")
	}
	var n int
	if err = s.reader.QueryRow("SELECT count(*) FROM device_state_samples").Scan(&n); err != nil || n != 3 {
		t.Fatal("cleanup removed retained samples")
	}
}

func TestSchemaTwoUpgradeKeepsEarningsAndBacksUp(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent.db")
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for _, name := range []string{"001_initial.sql", "002_collection.sql"} {
		body, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: body}
	}
	if err = migrate(ctx, db, files, path); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO earnings VALUES('a','one','p','key','job','model',12,1,1,1780000000123456)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err = s.reader.QueryRow("SELECT count(*) FROM earnings").Scan(&n); err != nil || n != 1 {
		t.Fatal("upgrade damaged earnings")
	}
	backups, _ := filepath.Glob(path + ".pre-v3-*.bak")
	if len(backups) != 1 {
		t.Fatal("schema 2 upgrade backup missing")
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != 3 {
		t.Fatal("schema did not advance")
	}
}
