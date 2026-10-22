package netrates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/control"
)

type advClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *advClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *advClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type memStore struct {
	mu          sync.Mutex
	baseline    Snapshot
	hasBaseline bool
	intervals   []Interval
	switches    []SwitchRecord
	nextID      int64
}

func (m *memStore) LoadNetRateBaseline(_ context.Context) (Snapshot, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasBaseline {
		return Snapshot{}, false, nil
	}
	return m.baseline, true, nil
}

func (m *memStore) SaveNetRateRound(_ context.Context, snap Snapshot, intervals []Interval, retainAfter time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.baseline = snap
	m.hasBaseline = true
	m.intervals = append(m.intervals, intervals...)
	var kept []Interval
	for _, iv := range m.intervals {
		if !iv.EndedAt.Before(retainAfter) {
			kept = append(kept, iv)
		}
	}
	m.intervals = kept
	return nil
}

func (m *memStore) NetRateIntervals(_ context.Context, since time.Time) ([]Interval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Interval
	for _, iv := range m.intervals {
		if !iv.EndedAt.Before(since) {
			out = append(out, iv)
		}
	}
	return out, nil
}

func (m *memStore) InsertNetRateSwitch(_ context.Context, r SwitchRecord) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	r.ID = m.nextID
	m.switches = append([]SwitchRecord{r}, m.switches...)
	if len(m.switches) > 200 {
		m.switches = m.switches[:200]
	}
	return r.ID, nil
}

func (m *memStore) UpdateNetRateSwitch(_ context.Context, id int64, status, errCode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.switches {
		if m.switches[i].ID == id {
			m.switches[i].Status = status
			m.switches[i].Error = errCode
			return nil
		}
	}
	return errors.New("missing switch")
}

func (m *memStore) UpdateNetRateSwitchJob(_ context.Context, id int64, jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.switches {
		if m.switches[i].ID == id {
			m.switches[i].JobID = jobID
			return nil
		}
	}
	return errors.New("missing switch")
}

func (m *memStore) NetRateSwitches(_ context.Context, limit int) ([]SwitchRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit > len(m.switches) {
		limit = len(m.switches)
	}
	out := append([]SwitchRecord(nil), m.switches[:limit]...)
	return out, nil
}

func (m *memStore) baselineSnap() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.baseline
}

type seqStats struct {
	mu       sync.Mutex
	snaps    []Snapshot
	idx      int
	statsErr error
	priceErr error
	prices   map[string]int64
}

func (s *seqStats) Stats(context.Context, Cohort) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statsErr != nil {
		return Snapshot{}, s.statsErr
	}
	if len(s.snaps) == 0 {
		return Snapshot{}, errors.New("no snaps")
	}
	i := s.idx
	if i >= len(s.snaps) {
		i = len(s.snaps) - 1
	} else {
		s.idx++
	}
	return s.snaps[i], nil
}

func (s *seqStats) Pricing(context.Context) (map[string]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.priceErr != nil {
		return nil, s.priceErr
	}
	return s.prices, nil
}

type fakeControl struct {
	mu          sync.Mutex
	local       []string
	submits     []control.Request
	jobs        map[string]control.Job
	lastChange  time.Time
	busy        bool
	submitErr   error
	nextJobNum  int
}

func (f *fakeControl) Models(context.Context) (control.Models, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy {
		return control.Models{}, &control.Error{Code: "control_busy", Status: 409}
	}
	out := make([]control.Model, len(f.local))
	for i, id := range f.local {
		out[i] = control.Model{ID: id}
	}
	return control.Models{Local: out}, nil
}

func (f *fakeControl) Submit(r control.Request) (control.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return control.Job{}, f.submitErr
	}
	f.nextJobNum++
	id := fmt.Sprintf("job-%d", f.nextJobNum)
	job := control.Job{Request: r, ID: id, Status: "running", CreatedAt: time.Now().UTC()}
	f.submits = append(f.submits, r)
	if f.jobs == nil {
		f.jobs = map[string]control.Job{}
	}
	f.jobs[job.ID] = job
	return job, nil
}

func (f *fakeControl) LastModelChange() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastChange
}

func (f *fakeControl) Job(id string) (control.Job, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	return j, ok
}

func (f *fakeControl) Busy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy
}

func (f *fakeControl) setJob(id, status, errCode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[id]
	j.Status = status
	j.Error = errCode
	f.jobs[id] = j
}

func (f *fakeControl) submitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.submits)
}

func (s *seqStats) statsCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx
}

func testCfg() config.NetworkRates {
	c := config.DefaultNetworkRates()
	c.AutoSwitch = false
	return c
}

func writeSettingsFile(t *testing.T, dir string, autoSwitch bool, since time.Time, lastSwitch *time.Time) {
	t.Helper()
	p := settingsPayload{AutoSwitch: autoSwitch, Failures: 0}
	if autoSwitch {
		s := since.UTC()
		p.AutoSwitchSince = &s
	}
	if lastSwitch != nil {
		ls := lastSwitch.UTC()
		p.LastSwitchAt = &ls
	}
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "network-rates.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
}

func providers(gen int, model string, n int) []ProviderSnap {
	out := make([]ProviderSnap, n)
	for i := 0; i < n; i++ {
		out[i] = ProviderSnap{
			ID: model + "-p" + string(rune('a'+i)), Model: model,
			Requests: int64(gen*50 + i + 1), Tokens: int64(gen*100000 + (i+1)*1000),
		}
	}
	return out
}

func buildSnapSeries(base time.Time, n int, model string) []Snapshot {
	out := make([]Snapshot, n)
	for i := 0; i < n; i++ {
		out[i] = Snapshot{At: base.Add(time.Duration(i) * 5 * time.Minute), Providers: providers(i, model, 5)}
	}
	return out
}

func newTestRunner(t *testing.T, dir string, store *memStore, stats *seqStats, ctl *fakeControl, clk *advClock, currentModel string) (*Runner, *Settings) {
	t.Helper()
	settings := NewSettings(dir, false, nil)
	settings.Load(clk.Now())
	cm := func(context.Context) (string, bool) { return currentModel, currentModel != "" }
	return NewRunner(testCfg(), settings, store, stats, ctl, cm, clk.Now, nil), settings
}

func seedDualIntervals(store *memStore, start time.Time, alphaTokens, betaTokens int64) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.baseline = Snapshot{At: start, Providers: providers(0, "model-alpha", 5)}
	store.hasBaseline = true
	for i := 0; i < 6; i++ {
		from := start.Add(time.Duration(i) * 5 * time.Minute)
		to := from.Add(5 * time.Minute)
		store.intervals = append(store.intervals,
			Interval{StartedAt: from, EndedAt: to, Model: "model-alpha", Providers: 5, DeltaRequests: 100, DeltaTokens: alphaTokens},
			Interval{StartedAt: from, EndedAt: to, Model: "model-beta", Providers: 5, DeltaRequests: 100, DeltaTokens: betaTokens},
		)
	}
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)

func TestRunnerFreshStartSamplesAndNoSubmit(t *testing.T) {
	base := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	clk := &advClock{t: base}
	store := &memStore{}
	stats := &seqStats{snaps: buildSnapSeries(base, 7, "model-m1"), prices: map[string]int64{"model-m1": 2200000}}
	ctl := &fakeControl{local: []string{"model-m1"}}
	dir := t.TempDir()
	r, _ := newTestRunner(t, dir, store, stats, ctl, clk, "model-m1")
	ctx := context.Background()

	for round := 1; round <= 7; round++ {
		r.runRound(ctx)
		st := r.Status()
		if st.Candidates == nil {
			t.Fatalf("round %d: candidates must encode as an array", round)
		}
		wantSamples := round - 1
		if round > 6 {
			wantSamples = 6
		}
		if st.Samples != wantSamples {
			t.Fatalf("round %d: samples=%d want %d", round, st.Samples, wantSamples)
		}
		if wantSamples < 6 && st.Recommendation != nil {
			t.Fatalf("round %d: expected nil recommendation", round)
		}
		if round < 7 {
			clk.Advance(5 * time.Minute)
		}
	}
	if st := r.Status(); st.Recommendation == nil {
		t.Fatal("expected recommendation after 7 snapshots")
	}
	if len(ctl.submits) != 0 {
		t.Fatalf("unexpected submits: %+v", ctl.submits)
	}
}

func TestRunnerAutoSwitchBetterModelOnce(t *testing.T) {
	base := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	clk := &advClock{t: base.Add(2 * time.Hour)}
	store := &memStore{}
	seedDualIntervals(store, base, 100_000, 800_000)
	stats := &seqStats{
		snaps:  []Snapshot{store.baselineSnap()},
		prices: map[string]int64{"model-alpha": 2200000, "model-beta": 2200000},
	}
	ctl := &fakeControl{local: []string{"model-alpha", "model-beta"}}
	dir := t.TempDir()
	writeSettingsFile(t, dir, true, base.Add(-2*time.Hour), nil)
	r, _ := newTestRunner(t, dir, store, stats, ctl, clk, "model-alpha")
	r.cfg.AutoSwitch = true

	r.runRound(context.Background())
	if len(ctl.submits) != 1 {
		t.Fatalf("submits=%d", len(ctl.submits))
	}
	req := ctl.submits[0]
	if req.Action != "switch" || len(req.Models) != 1 || req.Models[0] != "model-beta" {
		t.Fatalf("submit: %+v", req)
	}
	if !requestIDPattern.MatchString(req.RequestID) {
		t.Fatalf("request_id %q", req.RequestID)
	}
	rows, _ := store.NetRateSwitches(context.Background(), 5)
	if len(rows) != 1 || rows[0].Status != "running" || rows[0].JobID == "" || len(rows[0].Summary) == 0 {
		t.Fatalf("switch record: %+v", rows)
	}
	if rows[0].Reason != "better_model" {
		t.Fatalf("reason %q", rows[0].Reason)
	}

	clk.Advance(10 * time.Minute)
	r.runRound(context.Background())
	if len(ctl.submits) != 1 {
		t.Fatalf("second round submitted again: %d", len(ctl.submits))
	}
	if st := r.Status(); st.Switch.BlockedReason == nil || *st.Switch.BlockedReason != "min_interval" {
		t.Fatalf("blocked=%v", st.Switch.BlockedReason)
	}
}

func TestRunnerRestartDoesNotResubmit(t *testing.T) {
	base := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	clk := &advClock{t: base.Add(2 * time.Hour)}
	store := &memStore{}
	seedDualIntervals(store, base, 100_000, 800_000)
	stats := &seqStats{
		snaps:  []Snapshot{store.baselineSnap()},
		prices: map[string]int64{"model-alpha": 2200000, "model-beta": 2200000},
	}
	ctl := &fakeControl{local: []string{"model-alpha", "model-beta"}}
	dir := t.TempDir()
	writeSettingsFile(t, dir, true, base.Add(-2*time.Hour), nil)
	r1, _ := newTestRunner(t, dir, store, stats, ctl, clk, "model-alpha")
	r1.cfg.AutoSwitch = true
	r1.runRound(context.Background())
	if len(ctl.submits) != 1 {
		t.Fatal(len(ctl.submits))
	}

	clk.Advance(10 * time.Minute)
	settings2 := NewSettings(dir, false, nil)
	settings2.Load(clk.Now())
	r2 := NewRunner(testCfg(), settings2, store, stats, ctl, func(context.Context) (string, bool) {
		return "model-alpha", true
	}, clk.Now, nil)
	r2.cfg.AutoSwitch = true
	r2.runRound(context.Background())
	if len(ctl.submits) != 1 {
		t.Fatalf("restart resubmitted: %d", len(ctl.submits))
	}
}

func TestRunnerManualSwitchBlocksMinInterval(t *testing.T) {
	base := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	clk := &advClock{t: base}
	store := &memStore{}
	seedDualIntervals(store, base.Add(-time.Hour), 100_000, 800_000)
	stats := &seqStats{snaps: []Snapshot{store.baselineSnap()}, prices: map[string]int64{"model-alpha": 2200000, "model-beta": 2200000}}
	ctl := &fakeControl{local: []string{"model-alpha", "model-beta"}, lastChange: base.Add(-5 * time.Minute)}
	dir := t.TempDir()
	writeSettingsFile(t, dir, true, base.Add(-2*time.Hour), nil)
	r, _ := newTestRunner(t, dir, store, stats, ctl, clk, "model-alpha")
	r.cfg.AutoSwitch = true
	r.runRound(context.Background())
	if len(ctl.submits) != 0 {
		t.Fatalf("submit=%+v", ctl.submits)
	}
	if st := r.Status(); st.Switch.BlockedReason == nil || *st.Switch.BlockedReason != "min_interval" {
		t.Fatalf("blocked=%v", st.Switch.BlockedReason)
	}
}

func TestRunnerJobReconciliationAndBackoff(t *testing.T) {
	base := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	clk := &advClock{t: base}
	store := &memStore{}
	seedDualIntervals(store, base.Add(-time.Hour), 100_000, 800_000)
	stats := &seqStats{snaps: []Snapshot{store.baselineSnap()}, prices: map[string]int64{"model-alpha": 2200000, "model-beta": 2200000}}
	ctl := &fakeControl{local: []string{"model-alpha", "model-beta"}, jobs: map[string]control.Job{}}
	dir := t.TempDir()
	writeSettingsFile(t, dir, true, base.Add(-3*time.Hour), nil)
	r, settings := newTestRunner(t, dir, store, stats, ctl, clk, "model-alpha")
	r.cfg.AutoSwitch = true
	ctx := context.Background()

	idOK, _ := store.InsertNetRateSwitch(ctx, SwitchRecord{
		At: base.Add(-time.Minute), From: "model-alpha", To: "model-beta", Reason: "better_model",
		Status: "running", JobID: "job-ok", Summary: []SummaryItem{{Model: "model-beta", USDPerHourMicro: 1, AvgProviders: 5}},
	})
	ctl.jobs["job-ok"] = control.Job{ID: "job-ok", Status: "succeeded"}
	r.runRound(ctx)
	rows, _ := store.NetRateSwitches(ctx, 5)
	var okRow SwitchRecord
	for _, row := range rows {
		if row.ID == idOK {
			okRow = row
		}
	}
	if okRow.Status != "succeeded" {
		t.Fatalf("succeeded: %+v", okRow)
	}
	_, _, _, backoff, failures := settings.Snapshot()
	if failures != 0 || backoff != nil {
		t.Fatalf("failures=%d backoff=%v", failures, backoff)
	}

	idFail, _ := store.InsertNetRateSwitch(ctx, SwitchRecord{
		At: base, From: "model-alpha", To: "model-beta", Reason: "better_model",
		Status: "running", JobID: "job-bad", Summary: []SummaryItem{{Model: "model-beta", USDPerHourMicro: 1, AvgProviders: 5}},
	})
	ctl.jobs["job-bad"] = control.Job{ID: "job-bad", Status: "failed", Error: "command_failed"}
	clk.Advance(time.Minute)
	r.runRound(ctx)
	_, _, _, backoff, failures = settings.Snapshot()
	if failures != 1 || backoff == nil || !backoff.Equal(base.Add(time.Minute).Add(30*time.Minute)) {
		t.Fatalf("first failure backoff=%v failures=%d now=%v", backoff, failures, clk.Now())
	}
	rows, _ = store.NetRateSwitches(ctx, 5)
	var failed SwitchRecord
	for _, row := range rows {
		if row.ID == idFail {
			failed = row
		}
	}
	if failed.Status != "failed" || failed.Error != "command_failed" {
		t.Fatalf("failed row: %+v", failed)
	}

	idFail2, _ := store.InsertNetRateSwitch(ctx, SwitchRecord{
		At: clk.Now(), From: "model-alpha", To: "model-beta", Reason: "better_model",
		Status: "running", JobID: "job-bad2", Summary: []SummaryItem{{Model: "model-beta", USDPerHourMicro: 1, AvgProviders: 5}},
	})
	ctl.jobs["job-bad2"] = control.Job{ID: "job-bad2", Status: "unconfirmed", Error: "command_timeout"}
	clk.Advance(time.Minute)
	r.runRound(ctx)
	_, _, _, backoff, failures = settings.Snapshot()
	if failures != 2 || backoff == nil || !backoff.Equal(clk.Now().Add(60*time.Minute)) {
		t.Fatalf("second failure backoff=%v failures=%d", backoff, failures)
	}
	_ = idFail2

	ctl.submitErr = &control.Error{Code: "control_busy", Status: 409}
	ctl.lastChange = base.Add(-3 * time.Hour)
	writeSettingsFile(t, dir, true, base.Add(-3*time.Hour), nil)
	settings.Load(clk.Now())
	r.runRound(ctx)
	rows, _ = store.NetRateSwitches(ctx, 10)
	var busyRec SwitchRecord
	for _, row := range rows {
		if row.Error == "control_busy" {
			busyRec = row
		}
	}
	if busyRec.Status != "failed" {
		t.Fatalf("control_busy record: %+v", busyRec)
	}
	_, _, _, backoff, failures = settings.Snapshot()
	if failures != 1 || backoff == nil {
		t.Fatalf("backoff after control_busy failures=%d backoff=%v", failures, backoff)
	}
}

func TestRunnerFallbackGemma(t *testing.T) {
	fallback := "gemma-4-26b-qat-4bit"
	base := time.Date(2026, 10, 22, 16, 0, 0, 0, time.UTC)
	clk := &advClock{t: base.Add(2 * time.Hour)}
	store := &memStore{}
	seedDualIntervals(store, base, 50, 60)
	stats := &seqStats{snaps: []Snapshot{store.baselineSnap()}, prices: map[string]int64{"model-alpha": 2200000, "model-beta": 2200000}}
	ctl := &fakeControl{local: []string{"model-alpha", fallback}}
	dir := t.TempDir()
	writeSettingsFile(t, dir, true, base.Add(-2*time.Hour), nil)
	r, _ := newTestRunner(t, dir, store, stats, ctl, clk, "model-alpha")
	r.cfg.AutoSwitch = true
	r.cfg.FallbackModel = fallback
	r.cfg.NearZeroMicroUSDPerHour = 1000
	r.runRound(context.Background())
	if len(ctl.submits) != 1 || ctl.submits[0].Models[0] != fallback {
		t.Fatalf("fallback submit: %+v", ctl.submits)
	}
	rows, _ := store.NetRateSwitches(context.Background(), 1)
	if rows[0].Reason != "fallback" {
		t.Fatal(rows[0].Reason)
	}

	ctl.submits = nil
	clk.Advance(time.Hour)
	writeSettingsFile(t, dir, true, base.Add(-3*time.Hour), ptrTime(clk.Now().Add(-time.Hour)))
	settings := NewSettings(dir, false, nil)
	settings.Load(clk.Now())
	r2 := NewRunner(testCfg(), settings, store, stats, ctl, func(context.Context) (string, bool) {
		return fallback, true
	}, clk.Now, nil)
	r2.cfg.AutoSwitch = true
	r2.cfg.FallbackModel = fallback
	r2.cfg.NearZeroMicroUSDPerHour = 1000
	r2.runRound(context.Background())
	if len(ctl.submits) != 0 {
		t.Fatalf("should not switch when already on gemma: %+v", ctl.submits)
	}
}

func TestRunnerStaleRunningWithoutJobID(t *testing.T) {
	base := time.Date(2026, 10, 22, 18, 0, 0, 0, time.UTC)
	clk := &advClock{t: base}
	store := &memStore{}
	stats := &seqStats{snaps: []Snapshot{{At: base}}, prices: map[string]int64{}}
	ctl := &fakeControl{}
	dir := t.TempDir()
	r, _ := newTestRunner(t, dir, store, stats, ctl, clk, "")
	id, _ := store.InsertNetRateSwitch(context.Background(), SwitchRecord{
		At: base.Add(-21 * time.Minute), From: "a", To: "b", Reason: "better_model", Status: "running", Summary: []SummaryItem{},
	})
	r.runRound(context.Background())
	rows, _ := store.NetRateSwitches(context.Background(), 5)
	var row SwitchRecord
	for _, sw := range rows {
		if sw.ID == id {
			row = sw
		}
	}
	if row.Status != "unconfirmed" || row.Error != "job_missing" {
		t.Fatalf("stale row: %+v", row)
	}
}

func TestRunnerStatsAndPricingErrors(t *testing.T) {
	base := time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC)
	clk := &advClock{t: base}
	store := &memStore{}
	seedDualIntervals(store, base.Add(-time.Hour), 100_000, 800_000)
	before := store.baselineSnap()
	stats := &seqStats{statsErr: errors.New("down"), prices: map[string]int64{"model-alpha": 2200000}}
	ctl := &fakeControl{local: []string{"model-alpha", "model-beta"}}
	dir := t.TempDir()
	r, _ := newTestRunner(t, dir, store, stats, ctl, clk, "model-alpha")
	r.runRound(context.Background())
	st := r.Status()
	if st.LastError == nil || *st.LastError != "stats_unavailable" {
		t.Fatalf("last_error=%v", st.LastError)
	}
	if len(ctl.submits) != 0 {
		t.Fatal("submit on stats error")
	}
	after := store.baselineSnap()
	if !after.At.Equal(before.At) {
		t.Fatal("baseline changed on stats error")
	}

	stats.statsErr = nil
	stats.snaps = []Snapshot{before}
	stats.priceErr = errors.New("price down")
	r.prices = map[string]int64{"model-alpha": 999, "model-beta": 888}
	r.runRound(context.Background())
	st = r.Status()
	if st.LastError == nil || *st.LastError != "pricing_unavailable" {
		t.Fatalf("pricing last_error=%v", st.LastError)
	}
	if r.prices["model-alpha"] != 999 {
		t.Fatalf("prices overwritten: %+v", r.prices)
	}
}

func TestRunnerStatsErrorStillAutoSwitches(t *testing.T) {
	base := time.Date(2026, 10, 22, 21, 0, 0, 0, time.UTC)
	clk := &advClock{t: base}
	store := &memStore{}
	seedDualIntervals(store, base.Add(-time.Hour), 100_000, 800_000)
	stats := &seqStats{statsErr: errors.New("down"), prices: map[string]int64{"model-alpha": 2200000, "model-beta": 2200000}}
	ctl := &fakeControl{local: []string{"model-alpha", "model-beta"}}
	dir := t.TempDir()
	writeSettingsFile(t, dir, true, base.Add(-2*time.Hour), nil)
	r, _ := newTestRunner(t, dir, store, stats, ctl, clk, "model-alpha")
	r.cfg.AutoSwitch = true
	r.prices = map[string]int64{"model-alpha": 2200000, "model-beta": 2200000}
	r.runRound(context.Background())
	if ctl.submitCount() != 1 {
		t.Fatalf("expected switch despite stats error, submits=%d", ctl.submitCount())
	}
	st := r.Status()
	if st.LastError == nil || *st.LastError != "stats_unavailable" {
		t.Fatalf("last_error=%v", st.LastError)
	}
}

func TestRunnerSwitchRefreshesNextEligibleAt(t *testing.T) {
	base := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	clk := &advClock{t: base.Add(2 * time.Hour)}
	store := &memStore{}
	seedDualIntervals(store, base, 100_000, 800_000)
	stats := &seqStats{
		snaps:  []Snapshot{store.baselineSnap()},
		prices: map[string]int64{"model-alpha": 2200000, "model-beta": 2200000},
	}
	ctl := &fakeControl{local: []string{"model-alpha", "model-beta"}}
	dir := t.TempDir()
	writeSettingsFile(t, dir, true, base.Add(-2*time.Hour), nil)
	r, _ := newTestRunner(t, dir, store, stats, ctl, clk, "model-alpha")
	r.cfg.AutoSwitch = true
	r.runRound(context.Background())
	if ctl.submitCount() != 1 {
		t.Fatalf("submits=%d", ctl.submitCount())
	}
	st := r.Status()
	if st.Switch.NextEligibleAt == nil {
		t.Fatal("missing next_eligible_at after switch")
	}
	got, err := time.Parse(time.RFC3339Nano, *st.Switch.NextEligibleAt)
	if err != nil {
		t.Fatal(err)
	}
	want := clk.Now().UTC().Add(30 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("next_eligible_at=%s want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

func TestRunnerCollectDoesNotSubmitSwitch(t *testing.T) {
	base := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	clk := &advClock{t: base.Add(2 * time.Hour)}
	store := &memStore{}
	seedDualIntervals(store, base, 100_000, 800_000)
	stats := &seqStats{
		snaps:  []Snapshot{store.baselineSnap()},
		prices: map[string]int64{"model-alpha": 2200000, "model-beta": 2200000},
	}
	ctl := &fakeControl{local: []string{"model-alpha", "model-beta"}}
	dir := t.TempDir()
	writeSettingsFile(t, dir, true, base.Add(-2*time.Hour), nil)
	r, _ := newTestRunner(t, dir, store, stats, ctl, clk, "model-alpha")
	r.cfg.AutoSwitch = true
	r.runCollect(context.Background())
	if ctl.submitCount() != 0 {
		t.Fatalf("collect submitted switch: %d", ctl.submitCount())
	}
	r.runSwitch(context.Background())
	if ctl.submitCount() != 1 {
		t.Fatalf("switch submits=%d", ctl.submitCount())
	}
}

type blockStats struct {
	mu        sync.Mutex
	started   chan struct{}
	release   chan struct{}
	snap      Snapshot
	prices    map[string]int64
	statCalls int
}

func (s *blockStats) Stats(ctx context.Context, _ Cohort) (Snapshot, error) {
	s.mu.Lock()
	s.statCalls++
	started := s.started
	release := s.release
	snap := s.snap
	s.mu.Unlock()
	select {
	case started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-release:
		return snap, nil
	}
}

func (s *blockStats) Pricing(context.Context) (map[string]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prices, nil
}

func waitSubmits(t *testing.T, ctl *fakeControl, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctl.submitCount() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("submits=%d want %d", ctl.submitCount(), want)
}

func TestRunSwitchesWhileCollectBlocked(t *testing.T) {
	now := time.Now().UTC()
	store := &memStore{}
	seedDualIntervals(store, now.Add(-time.Hour), 100_000, 800_000)
	stats := &blockStats{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		snap:    store.baselineSnap(),
		prices:  map[string]int64{"model-alpha": 2200000, "model-beta": 2200000},
	}
	ctl := &fakeControl{local: []string{"model-alpha", "model-beta"}}
	dir := t.TempDir()
	writeSettingsFile(t, dir, true, now.Add(-2*time.Hour), nil)
	settings := NewSettings(dir, false, nil)
	settings.Load(now)
	r := NewRunner(testCfg(), settings, store, stats, ctl, func(context.Context) (string, bool) {
		return "model-alpha", true
	}, time.Now, nil)
	r.cfg.AutoSwitch = true
	r.interval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx)
	}()

	select {
	case <-stats.started:
	case <-time.After(2 * time.Second):
		t.Fatal("collect did not start")
	}
	waitSubmits(t, ctl, 1, 2*time.Second)
	cancel()
	<-done
}

func TestRunSwitchesWhenCooldownExpiresWithoutCollectTick(t *testing.T) {
	now := time.Now().UTC()
	store := &memStore{}
	seedDualIntervals(store, now.Add(-time.Hour), 100_000, 800_000)
	stats := &seqStats{snaps: []Snapshot{store.baselineSnap()}, prices: map[string]int64{"model-alpha": 2200000, "model-beta": 2200000}}
	ctl := &fakeControl{local: []string{"model-alpha", "model-beta"}}
	dir := t.TempDir()
	writeSettingsFile(t, dir, true, now.Add(-2*time.Hour), nil)
	settings := NewSettings(dir, false, nil)
	settings.Load(now)
	r := NewRunner(testCfg(), settings, store, stats, ctl, func(context.Context) (string, bool) {
		return "model-alpha", true
	}, time.Now, nil)
	r.cfg.AutoSwitch = true
	r.interval = time.Hour
	r.minSwitch = 300 * time.Millisecond
	if err := settings.SetLastSwitchAt(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx)
	}()

	time.Sleep(80 * time.Millisecond)
	if ctl.submitCount() != 0 {
		t.Fatalf("switched before cooldown: %d", ctl.submitCount())
	}
	waitSubmits(t, ctl, 1, 2*time.Second)
	if stats.statsCalls() > 1 {
		t.Fatalf("extra collect ticks: %d", stats.statsCalls())
	}
	cancel()
	<-done
}

func TestRunnerAutoSwitchOffNeverSubmits(t *testing.T) {
	base := time.Date(2026, 10, 22, 22, 0, 0, 0, time.UTC)
	clk := &advClock{t: base.Add(40 * time.Minute)}
	store := &memStore{}
	seedDualIntervals(store, base, 100_000, 800_000)
	stats := &seqStats{snaps: []Snapshot{store.baselineSnap()}, prices: map[string]int64{"model-alpha": 2200000, "model-beta": 2200000}}
	ctl := &fakeControl{local: []string{"model-alpha", "model-beta"}}
	dir := t.TempDir()
	r, _ := newTestRunner(t, dir, store, stats, ctl, clk, "model-alpha")
	r.runRound(context.Background())
	st := r.Status()
	if st.Recommendation == nil {
		t.Fatal("expected recommendation")
	}
	if len(ctl.submits) != 0 {
		t.Fatal("auto_switch off should not submit")
	}
	if st.Switch.BlockedReason == nil || *st.Switch.BlockedReason != "auto_switch_off" {
		t.Fatalf("blocked=%v", st.Switch.BlockedReason)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
