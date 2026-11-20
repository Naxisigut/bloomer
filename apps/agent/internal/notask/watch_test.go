package notask

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bloomer/agent/internal/control"
	"bloomer/agent/internal/telemetry"
)

type fakeRestart struct {
	busy  bool
	err   error
	calls int
}

func (f *fakeRestart) Busy() bool { return f.busy }
func (f *fakeRestart) IdleRestart() (control.Job, error) {
	f.calls++
	if f.err != nil {
		return control.Job{}, f.err
	}
	return control.Job{ID: "job-1"}, nil
}

func idleSample(at time.Time, pid int, start int64, served uint64, active, pending bool) telemetry.Sample {
	sample := telemetry.NewSample(at)
	sample.Quality["inference_active"] = telemetry.OK
	sample.Quality["request_work_pending"] = telemetry.OK
	sample.InferenceActive = telemetry.Ptr(active)
	sample.RequestWorkPending = telemetry.Ptr(pending)
	sample.ProviderPID = &pid
	sample.ProviderRunning = telemetry.Ptr(true)
	sample.ProcessStartMicros = &start
	sample.RequestsServed = &served
	return sample
}

func stepIdle(t *testing.T, watch *Watch, from, to time.Time, pid int, startMicros int64, served uint64) {
	t.Helper()
	for at := from; !at.After(to); at = at.Add(time.Minute) {
		if err := watch.Observe(context.Background(), idleSample(at, pid, startMicros, served, false, false)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRestartsOnlyAfterTenMinutesWithoutWork(t *testing.T) {
	restart := &fakeRestart{}
	watch := New(t.TempDir(), true, restart, nil)
	start := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	stepIdle(t, watch, start, start.Add(10*time.Minute), 42, 99, 3)
	if restart.calls != 0 {
		t.Fatal("restarted at exactly 10 minutes")
	}
	if err := watch.Observe(context.Background(), idleSample(start.Add(10*time.Minute+time.Second), 42, 99, 3, false, false)); err != nil {
		t.Fatal(err)
	}
	if restart.calls != 1 {
		t.Fatalf("calls %d", restart.calls)
	}
	stepIdle(t, watch, start.Add(11*time.Minute), start.Add(20*time.Minute), 42, 99, 3)
	if restart.calls != 1 {
		t.Fatal("restarted again before the idle clock elapsed")
	}
	if err := watch.Observe(context.Background(), idleSample(start.Add(20*time.Minute+2*time.Second), 42, 99, 3, false, false)); err != nil {
		t.Fatal(err)
	}
	if restart.calls != 2 {
		t.Fatalf("second idle window did not restart: %d", restart.calls)
	}
}

func TestNewWorkResetsTheIdleClock(t *testing.T) {
	restart := &fakeRestart{}
	watch := New(t.TempDir(), true, restart, nil)
	start := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	stepIdle(t, watch, start, start.Add(9*time.Minute), 42, 99, 3)
	if err := watch.Observe(context.Background(), idleSample(start.Add(10*time.Minute), 42, 99, 4, false, false)); err != nil {
		t.Fatal(err)
	}
	stepIdle(t, watch, start.Add(11*time.Minute), start.Add(20*time.Minute), 42, 99, 4)
	if restart.calls != 0 {
		t.Fatal("a new request reused the previous idle clock")
	}
	active := idleSample(start.Add(21*time.Minute), 42, 99, 4, true, false)
	if err := watch.Observe(context.Background(), active); err != nil {
		t.Fatal(err)
	}
	pending := idleSample(start.Add(22*time.Minute), 42, 99, 4, false, true)
	if err := watch.Observe(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	stepIdle(t, watch, start.Add(23*time.Minute), start.Add(32*time.Minute), 42, 99, 4)
	if restart.calls != 0 {
		t.Fatal("current work did not reset the idle clock")
	}
}

func TestProcessChangeGapAndStopResetTheClock(t *testing.T) {
	restart := &fakeRestart{}
	watch := New(t.TempDir(), true, restart, nil)
	start := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	stepIdle(t, watch, start, start.Add(9*time.Minute), 42, 99, 3)
	if err := watch.Observe(context.Background(), idleSample(start.Add(16*time.Minute), 42, 99, 3, false, false)); err != nil {
		t.Fatal(err)
	}
	stepIdle(t, watch, start.Add(17*time.Minute), start.Add(26*time.Minute), 42, 99, 3)
	if restart.calls != 0 {
		t.Fatal("a sampling gap still counted as one idle stretch")
	}
	stopped := idleSample(start.Add(27*time.Minute), 42, 99, 3, false, false)
	stopped.ProviderRunning = telemetry.Ptr(false)
	if err := watch.Observe(context.Background(), stopped); err != nil {
		t.Fatal(err)
	}
	stepIdle(t, watch, start.Add(28*time.Minute), start.Add(37*time.Minute), 7, 100, 0)
	if restart.calls != 0 {
		t.Fatal("a new process reused the previous idle clock")
	}
}

func TestMissingRequestCounterNeverRestarts(t *testing.T) {
	restart := &fakeRestart{}
	watch := New(t.TempDir(), true, restart, nil)
	start := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	for at := start; !at.After(start.Add(30 * time.Minute)); at = at.Add(time.Minute) {
		sample := idleSample(at, 42, 99, 0, false, false)
		sample.RequestsServed = nil
		if err := watch.Observe(context.Background(), sample); err != nil {
			t.Fatal(err)
		}
	}
	if restart.calls != 0 {
		t.Fatalf("restarted without a request counter: %d", restart.calls)
	}
}

func TestDisabledSwitchDoesNotRestart(t *testing.T) {
	restart := &fakeRestart{}
	dir := t.TempDir()
	watch := New(dir, false, restart, nil)
	start := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	watch.Observe(context.Background(), idleSample(start, 42, 99, 3, false, false))
	watch.Observe(context.Background(), idleSample(start.Add(11*time.Minute), 42, 99, 3, false, false))
	if restart.calls != 0 || watch.Enabled() {
		t.Fatal("default off still restarted")
	}
	if err := watch.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	reloaded := New(dir, false, restart, nil)
	reloaded.Load(start.Add(11 * time.Minute))
	if !reloaded.Enabled() {
		t.Fatal("enabled switch was not saved")
	}
	stepIdle(t, reloaded, start.Add(11*time.Minute), start.Add(22*time.Minute), 42, 99, 3)
	if restart.calls != 1 {
		t.Fatalf("enabling did not start a fresh window: %d", restart.calls)
	}
	info, err := os.Stat(filepath.Join(dir, "no-task-restart.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
}

func TestReloadContinuesTheSameIdleWindow(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	first := &fakeRestart{}
	watch := New(dir, false, first, nil)
	if err := watch.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	stepIdle(t, watch, start, start.Add(9*time.Minute), 42, 99, 3)
	second := &fakeRestart{}
	reloaded := New(dir, false, second, nil)
	reloaded.Load(start.Add(9*time.Minute + 30*time.Second))
	reloaded.Observe(context.Background(), idleSample(start.Add(10*time.Minute+time.Second), 42, 99, 3, false, false))
	if second.calls != 1 {
		t.Fatalf("reload lost the idle clock: %d", second.calls)
	}
}

func TestBusyAndFailedRestartDoNotResetTheClock(t *testing.T) {
	restart := &fakeRestart{busy: true, err: errors.New("unused")}
	watch := New(t.TempDir(), true, restart, nil)
	start := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	stepIdle(t, watch, start, start.Add(10*time.Minute), 42, 99, 3)
	stepIdle(t, watch, start.Add(11*time.Minute), start.Add(11*time.Minute), 42, 99, 3)
	if restart.calls != 0 {
		t.Fatal("busy control was restarted")
	}
	restart.busy = false
	restart.err = &control.Error{Code: "control_disabled", Status: 403}
	stepIdle(t, watch, start.Add(12*time.Minute), start.Add(12*time.Minute), 42, 99, 3)
	restart.err = nil
	stepIdle(t, watch, start.Add(13*time.Minute), start.Add(13*time.Minute), 42, 99, 3)
	if restart.calls != 2 {
		t.Fatalf("failed restart blocked the next sample: %d", restart.calls)
	}
	stepIdle(t, watch, start.Add(14*time.Minute), start.Add(23*time.Minute), 42, 99, 3)
	if restart.calls != 2 {
		t.Fatal("successful restart did not reset the idle clock")
	}
}
