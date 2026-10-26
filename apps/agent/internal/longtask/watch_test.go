package longtask

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
func (f *fakeRestart) ForceRestart() (control.Job, error) {
	f.calls++
	if f.err != nil {
		return control.Job{}, f.err
	}
	return control.Job{ID: "job-1"}, nil
}

func qwenSample(at time.Time, model string, pid int, start int64, served uint64, active bool) telemetry.Sample {
	sample := telemetry.NewSample(at)
	sample.Quality["inference_active"] = telemetry.OK
	sample.Quality["current_model"] = telemetry.OK
	sample.InferenceActive = telemetry.Ptr(active)
	sample.CurrentModel = &model
	sample.ProviderPID = &pid
	sample.ProviderRunning = telemetry.Ptr(true)
	sample.ProcessStartMicros = &start
	sample.RequestsServed = &served
	return sample
}

func TestQwen359BNames(t *testing.T) {
	for _, model := range []string{"Qwen3.5-9B", "qwen3.5-9b", "Qwen3.5 9B", "org/Qwen3.5-9B", " Qwen3.5_9B "} {
		if !isQwen359B(model) {
			t.Fatalf("expected match: %s", model)
		}
	}
	for _, model := range []string{"Qwen3-9B", "Qwen3.5-27B", "Qwen3.5-9B-extra", ""} {
		if isQwen359B(model) {
			t.Fatalf("unexpected match: %s", model)
		}
	}
}

func step(t *testing.T, watch *Watch, from, to time.Time, model string, pid int, startMicros int64, served uint64) {
	t.Helper()
	for at := from; !at.After(to); at = at.Add(time.Minute) {
		if err := watch.Observe(context.Background(), qwenSample(at, model, pid, startMicros, served, true)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRestartsOnlyAfterTenMinutesOfTheSameTask(t *testing.T) {
	restart := &fakeRestart{}
	watch := New(t.TempDir(), true, restart, nil)
	start := time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)
	step(t, watch, start, start.Add(10*time.Minute), "Qwen3.5-9B", 42, 99, 3)
	if restart.calls != 0 {
		t.Fatal("restarted at exactly 10 minutes")
	}
	if err := watch.Observe(context.Background(), qwenSample(start.Add(10*time.Minute+time.Second), "Qwen3.5-9B", 42, 99, 3, true)); err != nil {
		t.Fatal(err)
	}
	if restart.calls != 1 {
		t.Fatalf("calls %d", restart.calls)
	}
	step(t, watch, start.Add(11*time.Minute), start.Add(25*time.Minute), "Qwen3.5-9B", 42, 99, 3)
	if restart.calls != 1 {
		t.Fatal("restarted again inside the cooldown")
	}
	step(t, watch, start.Add(26*time.Minute), start.Add(26*time.Minute), "Qwen3.5-9B", 42, 99, 3)
	if restart.calls != 2 {
		t.Fatalf("cooldown did not elapse: %d", restart.calls)
	}
}

func TestNewRequestModelGapAndIdleResetTheClock(t *testing.T) {
	restart := &fakeRestart{}
	watch := New(t.TempDir(), true, restart, nil)
	start := time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)
	step(t, watch, start, start.Add(9*time.Minute), "Qwen3.5-9B", 42, 99, 3)
	step(t, watch, start.Add(10*time.Minute), start.Add(20*time.Minute), "Qwen3.5-9B", 42, 99, 4)
	if restart.calls != 0 {
		t.Fatal("a new request reused the previous task clock")
	}
	other := qwenSample(start.Add(21*time.Minute), "Qwen3-32B", 42, 99, 4, true)
	if err := watch.Observe(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	idle := qwenSample(start.Add(22*time.Minute), "Qwen3.5-9B", 42, 99, 4, false)
	if err := watch.Observe(context.Background(), idle); err != nil {
		t.Fatal(err)
	}
	step(t, watch, start.Add(23*time.Minute), start.Add(33*time.Minute), "Qwen3.5-9B", 42, 99, 4)
	if restart.calls != 0 {
		t.Fatal("model change or idle did not reset the task clock")
	}
	if err := watch.Observe(context.Background(), qwenSample(start.Add(40*time.Minute), "Qwen3.5-9B", 42, 99, 4, true)); err != nil {
		t.Fatal(err)
	}
	step(t, watch, start.Add(41*time.Minute), start.Add(50*time.Minute), "Qwen3.5-9B", 42, 99, 4)
	if restart.calls != 0 {
		t.Fatal("a sampling gap still counted as one task")
	}
	step(t, watch, start.Add(51*time.Minute), start.Add(61*time.Minute), "Qwen3.5-9B", 7, 100, 4)
	if restart.calls != 0 {
		t.Fatal("a new process reused the previous task clock")
	}
}

func TestMissingRequestCounterNeverRestarts(t *testing.T) {
	restart := &fakeRestart{}
	watch := New(t.TempDir(), true, restart, nil)
	start := time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)
	for at := start; !at.After(start.Add(30 * time.Minute)); at = at.Add(time.Minute) {
		sample := qwenSample(at, "Qwen3.5-9B", 42, 99, 0, true)
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
	start := time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)
	watch.Observe(context.Background(), qwenSample(start, "Qwen3.5-9B", 42, 99, 3, true))
	watch.Observe(context.Background(), qwenSample(start.Add(11*time.Minute), "Qwen3.5-9B", 42, 99, 3, true))
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
	step(t, reloaded, start.Add(11*time.Minute), start.Add(22*time.Minute), "Qwen3.5-9B", 42, 99, 3)
	if restart.calls != 1 {
		t.Fatalf("enabling did not start a fresh window: %d", restart.calls)
	}
	info, err := os.Stat(filepath.Join(dir, "long-task-restart.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
}

func TestReloadContinuesTheSameTask(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)
	first := &fakeRestart{}
	watch := New(dir, false, first, nil)
	if err := watch.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	step(t, watch, start, start.Add(9*time.Minute), "Qwen3.5-9B", 42, 99, 3)
	second := &fakeRestart{}
	reloaded := New(dir, false, second, nil)
	reloaded.Load(start.Add(9*time.Minute + 30*time.Second))
	reloaded.Observe(context.Background(), qwenSample(start.Add(10*time.Minute+time.Second), "Qwen3.5-9B", 42, 99, 3, true))
	if second.calls != 1 {
		t.Fatalf("reload lost the task clock: %d", second.calls)
	}
}

func TestBusyAndFailedRestartDoNotStampTheCooldown(t *testing.T) {
	restart := &fakeRestart{busy: true, err: errors.New("unused")}
	watch := New(t.TempDir(), true, restart, nil)
	start := time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)
	step(t, watch, start, start.Add(10*time.Minute), "Qwen3.5-9B", 42, 99, 3)
	step(t, watch, start.Add(11*time.Minute), start.Add(11*time.Minute), "Qwen3.5-9B", 42, 99, 3)
	if restart.calls != 0 {
		t.Fatal("busy control was restarted")
	}
	restart.busy = false
	restart.err = &control.Error{Code: "control_disabled", Status: 403}
	step(t, watch, start.Add(12*time.Minute), start.Add(12*time.Minute), "Qwen3.5-9B", 42, 99, 3)
	restart.err = nil
	step(t, watch, start.Add(13*time.Minute), start.Add(13*time.Minute), "Qwen3.5-9B", 42, 99, 3)
	if restart.calls != 2 {
		t.Fatalf("failed restart blocked the next sample: %d", restart.calls)
	}
}
