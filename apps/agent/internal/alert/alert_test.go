package alert

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/store"
	"bloomer/agent/internal/telemetry"
)

var base = time.Date(2026, 10, 21, 2, 0, 0, 0, time.UTC) // 10:00 Shanghai
func setup(t *testing.T) (*Manager, *time.Time, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := base
	p := config.DefaultAlert(filepath.Join(t.TempDir(), "bark.key"))
	p.Enabled = true
	p.DeviceName = "测试 Mac"
	m := New(db, p, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	m.now = func() time.Time { return now }
	m.sender = &fakeSender{}
	return m, &now, path
}

type fakeSender struct {
	calls []Record
	err   error
}

func (f *fakeSender) Send(_ context.Context, r Record) error {
	f.calls = append(f.calls, r)
	return f.err
}
func sample(at time.Time) telemetry.Sample {
	s := telemetry.NewSample(at)
	s.ProviderStateAt = telemetry.Ptr(at)
	s.ProviderRunning = telemetry.Ptr(true)
	s.ModelReady = telemetry.Ptr(true)
	s.TrustReceivedAt = telemetry.Ptr(float64(at.Unix()) - 20)
	s.SessionID = telemetry.Ptr("session-a")
	s.ProviderPID = telemetry.Ptr(4242)
	s.ProcessStartMicros = telemetry.Ptr(int64(1_700_000_000_000_000))
	s.LastDeviceEarningAt = telemetry.Ptr(at.Add(-time.Minute))
	for _, key := range []string{"provider_running", "model_ready", "trust_received_at", "session_id"} {
		s.Quality[key] = telemetry.OK
	}
	return s
}
func evaluate(t *testing.T, m *Manager, s telemetry.Sample) {
	t.Helper()
	if err := m.Evaluate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}
func history(t *testing.T, m *Manager) []Record {
	t.Helper()
	r, err := m.Records(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestRulesConsecutiveTriggerAndRecovery(t *testing.T) {
	cases := []struct {
		name  string
		bad   func(*telemetry.Sample)
		count int
		level string
	}{
		{"coordinator_disconnected", func(s *telemetry.Sample) {
			s.TrustReceivedAt = telemetry.Ptr(float64(s.CollectedAt.Add(-5 * time.Minute).Unix()))
		}, 2, "critical"},
		{"provider_not_running", func(s *telemetry.Sample) { s.ProviderRunning = telemetry.Ptr(false) }, 2, "critical"},
		{"model_not_ready", func(s *telemetry.Sample) { s.ModelReady = telemetry.Ptr(false) }, 2, "critical"},
		{"no_earnings", func(s *telemetry.Sample) { s.LastDeviceEarningAt = telemetry.Ptr(s.CollectedAt.Add(-31 * time.Minute)) }, 1, "active"},
		{"provider_state_unknown", func(s *telemetry.Sample) {
			s.ProviderStateAt = telemetry.Ptr(s.CollectedAt.Add(-91 * time.Second))
			s.Quality["trust_received_at"] = telemetry.Stale
		}, 2, "critical"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := setup(t)
			for i := 0; i < tc.count; i++ {
				s := sample(base.Add(time.Duration(i) * time.Minute))
				tc.bad(&s)
				evaluate(t, m, s)
				if i < tc.count-1 && len(history(t, m)) != 0 {
					t.Fatal("triggered on first observation")
				}
			}
			rows := history(t, m)
			if len(rows) != 1 || rows[0].Type != tc.name || rows[0].Level != tc.level || rows[0].Event != "triggered" {
				t.Fatalf("trigger: %+v", rows)
			}
			evaluate(t, m, sample(base.Add(2*time.Minute)))
			rows = history(t, m)
			if len(rows) != 2 || rows[0].Event != "recovered" || !rows[0].Recovered || rows[0].Level != "active" || !strings.Contains(rows[0].Body, "已恢复，持续") {
				t.Fatalf("recovery: %+v", rows)
			}
		})
	}
}
func TestConsecutiveUnknownAndQualityReset(t *testing.T) {
	m, _, _ := setup(t)
	bad := func(at time.Time) telemetry.Sample { s := sample(at); s.ModelReady = telemetry.Ptr(false); return s }
	evaluate(t, m, bad(base))
	evaluate(t, m, sample(base.Add(time.Minute)))
	evaluate(t, m, bad(base.Add(2*time.Minute)))
	unknown := bad(base.Add(3 * time.Minute))
	unknown.Quality["model_ready"] = telemetry.Stale
	evaluate(t, m, unknown)
	evaluate(t, m, bad(base.Add(4*time.Minute)))
	if len(history(t, m)) != 0 {
		t.Fatal("nonconsecutive/unknown quality counted")
	}
	evaluate(t, m, bad(base.Add(4*time.Minute)))
	if len(history(t, m)) != 0 {
		t.Fatal("duplicate sample counted")
	}
	evaluate(t, m, bad(base.Add(5*time.Minute)))
	if len(history(t, m)) != 1 {
		t.Fatal("second consecutive sample missing")
	}
	unknown = sample(base.Add(6 * time.Minute))
	unknown.ModelReady = nil
	evaluate(t, m, unknown)
	if len(history(t, m)) != 1 {
		t.Fatal("unknown evidence falsely recovered")
	}
	evaluate(t, m, sample(base.Add(7*time.Minute)))
	if len(history(t, m)) != 2 {
		t.Fatal("known good did not recover")
	}
}
func TestStaleStateDoesNotProveDisconnectionOrRecovery(t *testing.T) {
	m, _, _ := setup(t)
	for i := 0; i < 2; i++ {
		s := sample(base.Add(time.Duration(i) * time.Minute))
		s.ProviderStateAt = telemetry.Ptr(s.CollectedAt.Add(-91 * time.Second))
		s.TrustReceivedAt = telemetry.Ptr(float64(base.Add(-time.Hour).Unix()))
		s.ModelReady = telemetry.Ptr(false)
		evaluate(t, m, s)
	}
	rows := history(t, m)
	if len(rows) != 1 || rows[0].Type != "provider_state_unknown" {
		t.Fatalf("stale misreported: %+v", rows)
	}
	m, _, _ = setup(t)
	for i := 0; i < 4; i++ {
		s := sample(base.Add(time.Duration(i) * time.Minute))
		s.TrustReceivedAt = telemetry.Ptr(float64(base.Add(-time.Hour).Unix()))
		if i >= 2 {
			s.ProviderStateAt = telemetry.Ptr(base.Add(-time.Hour))
		}
		evaluate(t, m, s)
	}
	for _, r := range history(t, m) {
		if r.Type == "coordinator_disconnected" && r.Event == "recovered" {
			t.Fatal("stale recovered connection")
		}
	}
}
func TestConnectionFlappingWindowSurvivesRestart(t *testing.T) {
	m, _, path := setup(t)
	for i := 0; i < 5; i++ {
		s := sample(base.Add(time.Duration(i) * time.Minute))
		s.SessionID = telemetry.Ptr(fmt.Sprint(i))
		evaluate(t, m, s)
	}
	if len(history(t, m)) != 0 {
		t.Fatal("initial session counted as reconnect")
	}
	m.db.Close()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m = New(db, m.policy, m.logger)
	s := sample(base.Add(5 * time.Minute))
	s.SessionID = telemetry.Ptr("5")
	evaluate(t, m, s)
	rows := history(t, m)
	if len(rows) != 1 || rows[0].Type != "connection_flapping" || rows[0].Level != "active" || !strings.Contains(rows[0].Title, "连接频繁重连") || !strings.Contains(rows[0].Body, "过去 60 分钟断线重连 5 次（进程重启导致的会话变化不计入）") {
		t.Fatalf("flapping: %+v", rows)
	}
	s = sample(base.Add(65 * time.Minute))
	s.SessionID = telemetry.Ptr("5")
	evaluate(t, m, s)
	rows = history(t, m)
	if len(rows) != 2 || rows[0].Event != "recovered" {
		t.Fatalf("window did not expire: %+v", rows)
	}
}
func setProcess(s *telemetry.Sample, pid int, start int64) {
	s.ProviderPID = telemetry.Ptr(pid)
	s.ProcessStartMicros = telemetry.Ptr(start)
}
func flapState(t *testing.T, m *Manager) (session, process string, changes int) {
	t.Helper()
	var body string
	if err := m.db.ReadDB().QueryRow("SELECT state_json FROM alert_state WHERE id=1").Scan(&body); err != nil {
		t.Fatal(err)
	}
	var state struct {
		LastSession string      `json:"last_session"`
		LastProcess string      `json:"last_process"`
		Changes     []time.Time `json:"changes"`
	}
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatal(err)
	}
	return state.LastSession, state.LastProcess, len(state.Changes)
}
func TestProcessRestartsDoNotCountAsFlapping(t *testing.T) {
	m, _, _ := setup(t)
	pid, start := 100, int64(1000)
	for i := 0; i <= 8; i++ {
		s := sample(base.Add(time.Duration(i) * time.Minute))
		if i > 0 {
			if i%2 == 1 {
				pid++
			} else {
				start++
			}
		}
		setProcess(&s, pid, start)
		s.SessionID = telemetry.Ptr(fmt.Sprintf("restart-%d", i))
		evaluate(t, m, s)
	}
	if len(history(t, m)) != 0 {
		t.Fatal("process restarts counted as reconnects")
	}
	if _, process, changes := flapState(t, m); process != fmt.Sprintf("%d:%d", pid, start) || changes != 0 {
		t.Fatalf("restart state: process=%s changes=%d", process, changes)
	}
}
func TestMixedReconnectsAndRestartsCountOnlyReconnects(t *testing.T) {
	m, _, _ := setup(t)
	type step struct {
		pid   int
		start int64
	}
	// Three in-process reconnects and three restarts. Only reconnects count.
	steps := []step{{1, 1000}, {1, 1000}, {2, 1000}, {2, 1000}, {2, 2000}, {2, 2000}, {3, 2000}}
	for i, step := range steps {
		s := sample(base.Add(time.Duration(i) * time.Minute))
		setProcess(&s, step.pid, step.start)
		s.SessionID = telemetry.Ptr(fmt.Sprintf("mix-%d", i))
		evaluate(t, m, s)
	}
	if len(history(t, m)) != 0 {
		t.Fatal("three reconnects and three restarts triggered flapping")
	}
	if _, _, changes := flapState(t, m); changes != 3 {
		t.Fatalf("counted %d, want 3 reconnects", changes)
	}
	for i := 7; i <= 8; i++ {
		s := sample(base.Add(time.Duration(i) * time.Minute))
		setProcess(&s, 3, 2000)
		s.SessionID = telemetry.Ptr(fmt.Sprintf("mix-%d", i))
		evaluate(t, m, s)
	}
	rows := history(t, m)
	if len(rows) != 1 || rows[0].Type != "connection_flapping" || !strings.Contains(rows[0].Body, "过去 60 分钟断线重连 5 次（进程重启导致的会话变化不计入）") || strings.Contains(rows[0].Body, "8 次") {
		t.Fatalf("detail counted restarts: %+v", rows)
	}
}
func TestMissingProcessIdentityDoesNotCount(t *testing.T) {
	m, _, _ := setup(t)
	first := sample(base)
	setProcess(&first, 4, 400)
	first.SessionID = telemetry.Ptr("known")
	evaluate(t, m, first)
	for i := 1; i <= 6; i++ {
		s := sample(base.Add(time.Duration(i) * time.Minute))
		s.SessionID = telemetry.Ptr(fmt.Sprintf("missing-%d", i))
		switch i % 3 {
		case 1:
			s.ProviderPID = nil
			s.ProcessStartMicros = telemetry.Ptr(int64(400))
		case 2:
			s.ProviderPID = telemetry.Ptr(4)
			s.ProcessStartMicros = nil
		default:
			s.ProviderPID = nil
			s.ProcessStartMicros = nil
		}
		evaluate(t, m, s)
		session, process, changes := flapState(t, m)
		if session != *s.SessionID || process != "4:400" || changes != 0 {
			t.Fatalf("missing identity counted: session=%s process=%s changes=%d", session, process, changes)
		}
	}
	if len(history(t, m)) != 0 {
		t.Fatal("missing process identity triggered flapping")
	}
}
func TestLegacyStateWithoutProcessIdentityLoads(t *testing.T) {
	m, _, _ := setup(t)
	raw := `{"rules":{},"last_sample_at":"2026-10-21T01:59:00Z","last_session":"old-session","changes":[]}`
	if err := m.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), "INSERT INTO alert_state VALUES(1,?) ON CONFLICT(id) DO UPDATE SET state_json=excluded.state_json", raw)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s := sample(base)
	setProcess(&s, 9, 900)
	s.SessionID = telemetry.Ptr("new-session")
	evaluate(t, m, s)
	session, process, changes := flapState(t, m)
	if session != "new-session" || process != "9:900" || changes != 0 || len(history(t, m)) != 0 {
		t.Fatalf("legacy first change counted: session=%s process=%s changes=%d history=%d", session, process, changes, len(history(t, m)))
	}
	s = sample(base.Add(time.Minute))
	setProcess(&s, 9, 900)
	s.SessionID = telemetry.Ptr("reconnected")
	evaluate(t, m, s)
	if _, _, changes = flapState(t, m); changes != 1 || len(history(t, m)) != 0 {
		t.Fatalf("later reconnect not counted once: changes=%d", changes)
	}
}
func TestEarningsNightThresholdAndPrerequisites(t *testing.T) {
	for _, tc := range []struct {
		hour, minute, age  int
		running, connected bool
		want               bool
	}{
		{0, 0, 31, true, true, false}, {7, 59, 60, true, true, false}, {7, 59, 61, true, true, true}, {8, 0, 31, true, true, true}, {23, 59, 31, true, true, true}, {12, 0, 30, true, true, false}, {12, 0, 31, false, true, false}, {12, 0, 31, true, false, false},
	} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			m, _, _ := setup(t)
			at := time.Date(2026, 10, 21, tc.hour, tc.minute, 0, 0, shanghai)
			s := sample(at)
			s.ProviderRunning = telemetry.Ptr(tc.running)
			if !tc.connected {
				s.TrustReceivedAt = telemetry.Ptr(float64(at.Add(-5 * time.Minute).Unix()))
			}
			s.LastDeviceEarningAt = telemetry.Ptr(at.Add(-time.Duration(tc.age) * time.Minute))
			evaluate(t, m, s)
			if (len(history(t, m)) > 0) != tc.want {
				t.Fatal(history(t, m))
			}
		})
	}
}
func TestRepeatRestartAndRecoveryDuration(t *testing.T) {
	m, now, path := setup(t)
	bad := func(at time.Time) telemetry.Sample {
		s := sample(at)
		s.TrustReceivedAt = telemetry.Ptr(float64(at.Add(-time.Hour).Unix()))
		return s
	}
	evaluate(t, m, bad(base))
	evaluate(t, m, bad(base.Add(time.Minute)))
	*now = base.Add(time.Minute)
	if _, err := m.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if history(t, m)[0].DeliveryStatus != "sent" {
		t.Fatal("not delivered")
	}
	m.db.Close()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m = New(db, m.policy, m.logger)
	m.now = func() time.Time { return *now }
	sender := &fakeSender{}
	m.sender = sender
	evaluate(t, m, bad(base.Add(2*time.Minute)))
	m.dispatch(context.Background())
	if len(history(t, m)) != 1 || len(sender.calls) != 0 {
		t.Fatal("restart duplicated sent push")
	}
	evaluate(t, m, bad(base.Add(30*time.Minute)))
	if len(history(t, m)) != 1 {
		t.Fatal("reminder too early")
	}
	evaluate(t, m, bad(base.Add(31*time.Minute)))
	if history(t, m)[0].Event != "reminder" {
		t.Fatal("repeat missing")
	}
	evaluate(t, m, sample(base.Add(4*time.Hour+22*time.Minute)))
	r := history(t, m)[0]
	if r.Event != "recovered" || r.DurationSeconds != int64((4*time.Hour+21*time.Minute)/time.Second) || !strings.Contains(r.Body, "4 小时 21 分钟") {
		t.Fatalf("lost recovery: %+v", r)
	}
}
func TestRetryBackoffPersistenceAndSecretSafeLogging(t *testing.T) {
	m, now, path := setup(t)
	var log bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&log, nil))
	sender := &fakeSender{err: errors.New("secret-bark-key")}
	m.sender = sender
	if _, err := m.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, delay := range []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 0} {
		processed, err := m.dispatch(context.Background())
		if err != nil || !processed {
			t.Fatal(processed, err)
		}
		r := history(t, m)[0]
		if r.Attempts != i+1 || r.ErrorCode != "bark_send_failed" {
			t.Fatal(r)
		}
		if delay != 0 {
			if r.NextAttemptAt == nil || !r.NextAttemptAt.Equal(now.Add(delay)) {
				t.Fatalf("backoff: %+v", r)
			}
			if processed, err = m.dispatch(context.Background()); err != nil || processed {
				t.Fatal("retried before due")
			}
			*now = now.Add(delay)
			if i == 0 {
				m.db.Close()
				db, err := store.Open(context.Background(), path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				next := New(db, m.policy, m.logger)
				next.now = m.now
				next.sender = sender
				m = next
			}
		} else if r.DeliveryStatus != "failed" || r.NextAttemptAt != nil {
			t.Fatal(r)
		}
	}
	if processed, _ := m.dispatch(context.Background()); processed {
		t.Fatal("exceeded three retries")
	}
	encoded, _ := json.Marshal(history(t, m))
	if strings.Contains(log.String(), "secret-bark-key") || strings.Contains(string(encoded), "secret-bark-key") {
		t.Fatal("key leaked")
	}
}
func TestHistoryPruningAndStateIndependentOfHistory(t *testing.T) {
	m, now, _ := setup(t)
	s := sample(base)
	s.ProviderRunning = telemetry.Ptr(false)
	evaluate(t, m, s)
	s.CollectedAt = base.Add(time.Minute)
	evaluate(t, m, s)
	for i := 0; i < 55; i++ {
		*now = base.Add(time.Duration(i+2) * time.Minute)
		if _, err := m.Test(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(history(t, m)) != 50 {
		t.Fatal("history not capped")
	}
	evaluate(t, m, sample(base.Add(time.Hour)))
	if r := history(t, m)[0]; r.Type != "provider_not_running" || r.Event != "recovered" {
		t.Fatalf("pruning erased state: %+v", r)
	}
}
func TestDisabledAlertsDoNotEvaluateOrSend(t *testing.T) {
	m, _, _ := setup(t)
	m.policy.Enabled = false
	evaluate(t, m, sample(base))
	if _, err := m.Test(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if len(history(t, m)) != 0 {
		t.Fatal("disabled created record")
	}
}

func TestRecoveryDeliverySurvivesRestartAndReplacesOldRetries(t *testing.T) {
	m, now, path := setup(t)
	sender := &fakeSender{err: errors.New("network down")}
	m.sender = sender
	for i := 0; i < 2; i++ {
		s := sample(base.Add(time.Duration(i) * time.Minute))
		s.ProviderRunning = telemetry.Ptr(false)
		evaluate(t, m, s)
	}
	*now = base.Add(time.Minute)
	if _, err := m.dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	evaluate(t, m, sample(base.Add(2*time.Minute)))
	rows := history(t, m)
	if len(rows) != 2 || rows[1].DeliveryStatus != "superseded" {
		t.Fatal(rows)
	}
	m.db.Close()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m = New(db, m.policy, m.logger)
	m.now = func() time.Time { return base.Add(30 * time.Minute) }
	sender = &fakeSender{}
	m.sender = sender
	if ok, err := m.dispatch(context.Background()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if len(sender.calls) != 1 || sender.calls[0].Event != "recovered" {
		t.Fatal("lost or reordered recovery", sender.calls)
	}
	if ok, err := m.dispatch(context.Background()); err != nil || ok {
		t.Fatal("old outage retry remained", ok, err)
	}
}
func TestConfigurableRuleBoundaries(t *testing.T) {
	m, _, _ := setup(t)
	m.policy.DisconnectSeconds = 60
	m.policy.ConsecutiveSamples = 3
	m.policy.FlapWindowMinutes = 2
	m.policy.FlapChanges = 2
	for i := 0; i < 3; i++ {
		s := sample(base.Add(time.Duration(i) * time.Minute))
		s.TrustReceivedAt = telemetry.Ptr(float64(s.CollectedAt.Add(-60 * time.Second).Unix()))
		evaluate(t, m, s)
	}
	if len(history(t, m)) != 0 {
		t.Fatal("strict boundary triggered")
	}
	for i := 3; i < 6; i++ {
		s := sample(base.Add(time.Duration(i) * time.Minute))
		s.TrustReceivedAt = telemetry.Ptr(float64(s.CollectedAt.Add(-61 * time.Second).Unix()))
		evaluate(t, m, s)
		if i < 5 && len(history(t, m)) != 0 {
			t.Fatal("configured consecutive count ignored")
		}
	}
	if len(history(t, m)) != 1 {
		t.Fatal("configured disconnect threshold ignored")
	}
	m, _, _ = setup(t)
	m.policy.FlapWindowMinutes = 2
	m.policy.FlapChanges = 2
	for i := 0; i < 3; i++ {
		s := sample(base.Add(time.Duration(i) * time.Minute))
		s.SessionID = telemetry.Ptr(fmt.Sprint(i))
		evaluate(t, m, s)
	}
	if len(history(t, m)) != 1 {
		t.Fatal("configured flap threshold ignored")
	}
	s := sample(base.Add(3 * time.Minute))
	s.SessionID = telemetry.Ptr("2")
	evaluate(t, m, s)
	if history(t, m)[0].Event != "recovered" {
		t.Fatal("flap window lower boundary retained")
	}
}

type waitingSender struct{ started chan struct{} }

func (s *waitingSender) Send(ctx context.Context, _ Record) error {
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}
func TestDeliveryDoesNotBlockTelemetryAndShutdownKeepsOutbox(t *testing.T) {
	m, _, _ := setup(t)
	sender := &waitingSender{started: make(chan struct{})}
	m.sender = sender
	if _, err := m.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	select {
	case <-sender.started:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery did not start")
	}
	check, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := m.Evaluate(check, sample(base)); err != nil {
		t.Fatal("network blocked evaluation", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel sender")
	}
	var n int
	if err := m.db.ReadDB().QueryRow("SELECT count(*) FROM alert_outbox").Scan(&n); err != nil || n != 1 {
		t.Fatal("shutdown lost outbox", n, err)
	}
}
