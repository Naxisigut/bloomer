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

func TestDeviceIdentityAndLatestSampleReplace(t *testing.T) {
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
	for i, at := range []time.Time{now.Add(-time.Minute), now} {
		v := telemetry.NewSample(at)
		v.DeviceID = id
		v.CPUPercent = telemetry.Ptr(float64(i))
		v.Quality["cpu_percent"] = telemetry.OK
		v.ProviderRunning = telemetry.Ptr(false)
		v.Quality["provider_running"] = telemetry.OK
		if err = s.SaveDeviceSample(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := s.LatestDeviceSample(ctx)
	if err != nil || latest == nil || latest.CPUPercent == nil || *latest.CPUPercent != 1 {
		t.Fatalf("latest sample: %+v %v", latest, err)
	}
	if latest.GPUPercent != nil || latest.Quality["gpu_percent"] != telemetry.Unsupported {
		t.Fatal("NULL and zero/false did not round trip")
	}
	var n int
	if err = s.reader.QueryRow("SELECT count(*) FROM device_state_samples").Scan(&n); err != nil || n != 1 {
		t.Fatalf("expected one telemetry row: %d %v", n, err)
	}
}

func TestCleanupSkipsDeviceTelemetry(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	now := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC)
	id, err := s.SaveDeviceIdentity(ctx, telemetry.Hardware{OS: "darwin"}, now)
	if err != nil {
		t.Fatal(err)
	}
	v := telemetry.NewSample(now.Add(-config.Retention - time.Hour))
	v.DeviceID = id
	v.CPUPercent = telemetry.Ptr(1.0)
	v.Quality["cpu_percent"] = telemetry.OK
	if err = s.SaveDeviceSample(ctx, v); err != nil {
		t.Fatal(err)
	}
	done, err := s.CleanupBatch(ctx, now.Add(-config.Retention), now, 1000)
	if err != nil || !done {
		t.Fatal("cleanup did not finish", err)
	}
	var rows int
	if err = s.reader.QueryRow("SELECT count(*) FROM device_state_samples").Scan(&rows); err != nil || rows != 1 {
		t.Fatal("cleanup removed latest telemetry")
	}
}

func TestSchemaFourActivityMigrationPreservesUnknownAndHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent.db")
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for _, name := range []string{"001_initial.sql", "002_collection.sql", "003_device_state.sql", "004_provider_routing.sql"} {
		body, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: body}
	}
	if err = migrate(ctx, db, files, path); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC)
	_, err = db.Exec(`INSERT INTO device_identity VALUES(1,'device','{}',?); INSERT INTO device_state_samples(device_id,collected_at_us,duration_ms,provider_running,routing_state,routing_priority,quality_json) VALUES('device',?,1,1,'routable','normal','{"provider_running":"ok","routing_state":"ok","routing_priority":"ok"}')`, now.UnixMicro(), now.Add(-time.Minute).UnixMicro())
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old, err := s.LatestDeviceSample(ctx)
	if err != nil || old.IsIdle != nil || old.InferenceActive != nil || old.Quality["is_idle"] != telemetry.Unsupported || old.Quality["routing_state"] != "" {
		t.Fatalf("old state fabricated: %+v %v", old, err)
	}
	backups, _ := filepath.Glob(path + ".pre-v5-*.bak")
	if len(backups) != 1 {
		t.Fatal("schema 4 backup missing")
	}
	v := telemetry.NewSample(now)
	v.DeviceID = "device"
	v.IsIdle = telemetry.Ptr(false)
	v.InferenceActive = telemetry.Ptr(false)
	v.RequestWorkPending = telemetry.Ptr(false)
	v.LastDeviceEarningAt = telemetry.Ptr(now.Add(-time.Minute))
	v.LastEarningsSyncAt = telemetry.Ptr(now)
	v.EarningsProviderID = telemetry.Ptr("own-provider")
	v.Quality["is_idle"], v.Quality["inference_active"], v.Quality["request_work_pending"] = telemetry.OK, telemetry.OK, telemetry.OK
	if err = s.SaveDeviceSample(ctx, v); err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestDeviceSample(ctx)
	if err != nil || latest.IsIdle == nil || *latest.IsIdle || latest.LastDeviceEarningAt == nil || !latest.LastDeviceEarningAt.Equal(*v.LastDeviceEarningAt) || latest.LastEarningsSyncAt == nil || latest.EarningsProviderID == nil || *latest.EarningsProviderID != "own-provider" {
		t.Fatalf("activity round trip: %+v %v", latest, err)
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
	if v, err := s.SchemaVersion(ctx); err != nil || v != 7 {
		t.Fatal("schema did not advance", v, err)
	}
}

func TestSchemaFiveAlertMigrationAndTrustRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent.db")
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for _, name := range []string{"001_initial.sql", "002_collection.sql", "003_device_state.sql", "004_provider_routing.sql", "005_device_activity.sql"} {
		body, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: body}
	}
	if err = migrate(ctx, db, files, path); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 2, 0, 0, 0, time.UTC)
	if _, err = db.Exec(`INSERT INTO device_identity VALUES(1,'device','{}',?);INSERT INTO device_state_samples(device_id,collected_at_us,duration_ms,quality_json) VALUES('device',?,1,'{}')`, now.UnixMicro(), now.UnixMicro()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old, err := s.LatestDeviceSample(ctx)
	if err != nil || old.TrustReceivedAt != nil || old.SessionID != nil || old.Quality["trust_received_at"] != telemetry.Unsupported {
		t.Fatal(old, err)
	}
	backups, _ := filepath.Glob(path + ".pre-v6-*.bak")
	if len(backups) != 1 {
		t.Fatal("schema 5 backup missing")
	}
	v := telemetry.NewSample(now.Add(time.Minute))
	v.DeviceID = "device"
	v.TrustReceivedAt = telemetry.Ptr(float64(now.Unix()) + 0.125)
	v.SessionID = telemetry.Ptr("session")
	v.Quality["trust_received_at"], v.Quality["session_id"] = telemetry.OK, telemetry.OK
	if err = s.SaveDeviceSample(ctx, v); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestDeviceSample(ctx)
	if err != nil || got.TrustReceivedAt == nil || *got.TrustReceivedAt != *v.TrustReceivedAt || got.SessionID == nil || *got.SessionID != "session" {
		t.Fatal(got, err)
	}
}

func TestSchemaSevenPrunesHistoryKeepsLatest(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent.db")
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for _, name := range []string{"001_initial.sql", "002_collection.sql", "003_device_state.sql", "004_provider_routing.sql", "005_device_activity.sql", "006_alerts.sql"} {
		body, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: body}
	}
	if err = migrate(ctx, db, files, path); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 2, 0, 0, 0, time.UTC)
	if _, err = db.Exec(`INSERT INTO device_identity VALUES(1,'device','{}',?);
INSERT INTO device_state_samples(device_id,collected_at_us,duration_ms,quality_json) VALUES
('device',?,1,'{}'),('device',?,1,'{}')`, now.UnixMicro(), now.Add(-2*time.Minute).UnixMicro(), now.UnixMicro()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	backups, _ := filepath.Glob(path + ".pre-v7-*.bak")
	if len(backups) != 1 {
		t.Fatal("schema 6 backup missing")
	}
	var n int
	if err = s.reader.QueryRow("SELECT count(*) FROM device_state_samples").Scan(&n); err != nil || n != 1 {
		t.Fatal("migration did not prune history", n, err)
	}
	latest, err := s.LatestDeviceSample(ctx)
	if err != nil || latest == nil || !latest.CollectedAt.Equal(now) {
		t.Fatal(latest, err)
	}
}
