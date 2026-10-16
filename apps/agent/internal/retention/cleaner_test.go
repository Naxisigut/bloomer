package retention

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/health"
	"bloomer/agent/internal/store"
	"bloomer/agent/internal/upstream"
)

var epoch = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)

func open(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func exec(t *testing.T, s *store.Store, query string, args ...any) {
	t.Helper()
	if err := s.Write(context.Background(), func(tx *sql.Tx) error { _, err := tx.Exec(query, args...); return err }); err != nil {
		t.Fatal(err)
	}
}
func state(t *testing.T, s *store.Store) store.State {
	t.Helper()
	v, err := s.LoadState(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func scalar(t *testing.T, s *store.Store, q string) int64 {
	t.Helper()
	var n int64
	if err := s.ReadDB().QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func populate(t *testing.T, s *store.Store, count int) {
	t.Helper()
	cutoff := epoch.Add(-config.Retention)
	keep := []upstream.Earning{
		{AccountID: "a", RecordID: "boundary", Model: "m", AmountMicroUSD: 7, CreatedAt: cutoff},
		{AccountID: "a", RecordID: "recent", Model: "m", AmountMicroUSD: 9, CreatedAt: epoch.Add(-time.Hour)},
	}
	if _, err := s.CommitRound(context.Background(), store.Round{AccountID: "a", StartedAt: epoch.Add(-time.Minute), CompletedAt: epoch.Add(-time.Minute), Cutoff: cutoff, Mode: "initial", Records: keep, Summary: upstream.Summary{AccountID: "a", TotalMicroUSD: 123456789, TotalRecordCount: 5000}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(context.Background(), func(tx *sql.Tx) error {
		stmt, err := tx.Prepare("INSERT INTO earnings VALUES('a',?,'p','pk','j','m',1,0,0,?)")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i := 0; i < count; i++ {
			if _, err = stmt.Exec(fmt.Sprintf("old-%04d", i), cutoff.Add(-time.Microsecond).UnixMicro()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestRetentionBoundaryBeforePhysicalCleanup(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "agent.db"))
	populate(t, s, 1)
	ctx := context.Background()
	from := epoch.Add(-config.Retention - time.Hour)
	count, amount, window, err := s.RetainedTotals(ctx, "a", epoch, from, time.Time{})
	if err != nil || count != 2 || amount != 16 || !window.Truncated {
		t.Fatalf("expired total included: %d %d %+v %v", count, amount, window, err)
	}
	rows, rangeInfo, err := s.RetainedEarnings(ctx, "a", epoch, from, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for rows.Next() {
		var id, p, pk, j, m string
		var amount, prompt, completion, at int64
		if err = rows.Scan(&id, &p, &pk, &j, &m, &amount, &prompt, &completion, &at); err != nil {
			t.Fatal(err)
		}
		ids[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) != 2 || !ids["boundary"] || !rangeInfo.Truncated {
		t.Fatal("detail retention boundary differs from aggregate")
	}
	if scalar(t, s, "SELECT count(*) FROM earnings") != 3 {
		t.Fatal("query unexpectedly performed physical deletion")
	}
	// Explicitly older ranges return no data plus the available boundary.
	count, _, window, err = s.RetainedTotals(ctx, "a", epoch, from, epoch.Add(-config.Retention-time.Microsecond))
	if err != nil || count != 0 || !window.Truncated {
		t.Fatal("expired request returned data or hid range truncation")
	}
}
func TestBatchedCleanupFailureRestartAndGapClipping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	s := open(t, path)
	populate(t, s, 2500)
	cutoff := epoch.Add(-config.Retention)
	exec(t, s, `INSERT INTO coverage_gaps(account_id,discovered_at_us,interruption_start_us,interruption_end_us,reason) VALUES('a',?,?,?,'expired'),('a',?,?,?,'crossing')`, epoch.UnixMicro(), cutoff.Add(-2*time.Hour).UnixMicro(), cutoff.Add(-time.Hour).UnixMicro(), epoch.UnixMicro(), cutoff.Add(-time.Hour).UnixMicro(), epoch.UnixMicro())
	now := epoch
	runtime := health.New()
	cleaner := Cleaner{Store: s, Now: func() time.Time { return now }, Runtime: runtime}
	result, err := cleaner.Step(context.Background())
	if err != nil || !result.More {
		t.Fatalf("first batch: %+v %v", result, err)
	}
	if scalar(t, s, "SELECT count(*) FROM earnings") != 1502 || !state(t, s).LastCleanup.IsZero() {
		t.Fatal("first batch advanced success or deleted too much")
	}
	exec(t, s, `CREATE TRIGGER fail_delete BEFORE DELETE ON earnings WHEN OLD.record_id='old-1000' BEGIN SELECT RAISE(FAIL,'injected delete failure'); END`)
	if _, err = cleaner.Step(context.Background()); err == nil {
		t.Fatal("delete failure ignored")
	}
	if scalar(t, s, "SELECT count(*) FROM earnings") != 1502 || !state(t, s).LastCleanup.IsZero() || runtime.Snapshot().CleanupState != "failed" {
		t.Fatal("partial failed batch committed")
	}
	// A failed task does not spin on the same scheduling instant.
	if result, err = cleaner.Step(context.Background()); err != nil || !result.NextAt.Equal(epoch.Add(time.Minute)) {
		t.Fatal("cleanup failed without retry spacing")
	}
	exec(t, s, "DROP TRIGGER fail_delete")
	s.Close()
	s = open(t, path)
	now = epoch.Add(time.Minute)
	cleaner.Store = s
	batches := 0
	for {
		batches++
		result, err = cleaner.Step(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !result.More {
			break
		}
		if batches > 3 {
			t.Fatal("cleanup did not finish")
		}
	}
	if batches != 2 || scalar(t, s, "SELECT count(*) FROM earnings") != 2 || !state(t, s).LastCleanup.Equal(now) {
		t.Fatal("restart did not resume one frozen cleanup task")
	}
	if scalar(t, s, "SELECT count(*) FROM coverage_gaps") != 1 || scalar(t, s, "SELECT interruption_start_us FROM coverage_gaps") != cutoff.UnixMicro() {
		t.Fatal("gaps were not trimmed to retention")
	}
	if scalar(t, s, "SELECT total_micro_usd FROM account_summary_current") != 123456789 {
		t.Fatal("official lifetime total was truncated")
	}
	if state(t, s).LastSuccess != epoch.Add(-time.Minute) {
		t.Fatal("cleanup advanced successful sync")
	}
}
func TestMissedWeeksRunOnceAndClockRollbackNarrowsCutoff(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "agent.db"))
	populate(t, s, 1)
	now := epoch.Add(21 * 24 * time.Hour)
	exec(t, s, "UPDATE maintenance_state SET last_cleanup_success_at_us=?", epoch.UnixMicro())
	c := Cleaner{Store: s, Now: func() time.Time { return now }, Runtime: health.New()}
	result, err := c.Step(context.Background())
	if err != nil || result.More || !state(t, s).LastCleanup.Equal(now) {
		t.Fatal("missed weeks were replayed instead of one catch-up")
	}
	last := state(t, s).LastCleanup
	if result, err = c.Step(context.Background()); err != nil || !result.NextAt.Equal(last.Add(config.CleanupInterval)) || state(t, s).LastCleanup != last {
		t.Fatal("cleanup ran before seven days")
	}
	// A persisted cutoff from a future clock must never delete currently retained rows.
	now = epoch
	exec(t, s, "UPDATE maintenance_state SET cleanup_cutoff_us=?", epoch.Add(-config.Retention+time.Hour).UnixMicro())
	exec(t, s, "INSERT INTO earnings VALUES('a','clock-kept','p','pk','j','m',1,0,0,?)", epoch.Add(-config.Retention+30*time.Minute).UnixMicro())
	if _, err = c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, "SELECT count(*) FROM earnings WHERE record_id='clock-kept'") != 1 {
		t.Fatal("clock rollback used an unsafe future cutoff")
	}
}
