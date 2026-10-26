// Package longtask restarts Darkbloom when one Qwen3.5 9B task stays active
// past the limit. The provider state file has no per-request start time, so a
// task is the stretch where inference stays active and requests_served does
// not move.
package longtask

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bloomer/agent/internal/control"
	"bloomer/agent/internal/localfs"
	"bloomer/agent/internal/telemetry"
)

const (
	limit           = 10 * time.Minute
	maxGap          = 3 * time.Minute
	restartCooldown = 15 * time.Minute
)

// Restarter is the narrow control surface used to interrupt a stuck task.
type Restarter interface {
	ForceRestart() (control.Job, error)
	Busy() bool
}

type episode struct {
	Since       time.Time
	LastSeen    time.Time
	PID         int
	Start       int64
	Model       string
	HasRequests bool
	Requests    uint64
	RestartAt   time.Time
	noted       bool
}

type fileState struct {
	Enabled     bool       `json:"enabled"`
	Since       *time.Time `json:"active_since,omitempty"`
	LastSeen    *time.Time `json:"last_seen,omitempty"`
	PID         int        `json:"pid,omitempty"`
	Start       int64      `json:"process_start_us,omitempty"`
	Model       string     `json:"model,omitempty"`
	HasRequests bool       `json:"has_requests_served,omitempty"`
	Requests    uint64     `json:"requests_served,omitempty"`
	RestartAt   *time.Time `json:"restart_requested_at,omitempty"`
}

// Watch remembers one in-flight task and asks Darkbloom to force-restart it.
type Watch struct {
	mu             sync.Mutex
	path           string
	defaultEnabled bool
	enabled        bool
	ep             *episode
	restart        Restarter
	log            *slog.Logger
}

func New(dataDir string, defaultEnabled bool, restart Restarter, log *slog.Logger) *Watch {
	if log == nil {
		log = slog.Default()
	}
	return &Watch{
		path:           filepath.Join(dataDir, "long-task-restart.json"),
		defaultEnabled: defaultEnabled,
		enabled:        defaultEnabled,
		restart:        restart,
		log:            log,
	}
}

func (w *Watch) Load(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.enabled = w.defaultEnabled
	w.ep = nil
	f, err := localfs.OpenPrivate(w.path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		w.log.Warn("long task restart settings unavailable", "error", "unreadable")
		return
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, 8193))
	var saved fileState
	if err != nil || len(body) > 8192 || json.Unmarshal(body, &saved) != nil || saved.PID < 0 || len(saved.Model) > 1024 {
		w.log.Warn("long task restart settings unavailable", "error", "corrupt")
		return
	}
	w.enabled = saved.Enabled
	w.ep = episodeFrom(saved, now.UTC())
}

func episodeFrom(saved fileState, now time.Time) *episode {
	if !saved.Enabled || saved.Since == nil || saved.LastSeen == nil || saved.PID <= 0 || saved.Model == "" {
		return nil
	}
	since, seen := saved.Since.UTC(), saved.LastSeen.UTC()
	if seen.Before(since) || now.Before(seen) || now.Sub(seen) > maxGap {
		return nil
	}
	ep := &episode{Since: since, LastSeen: seen, PID: saved.PID, Start: saved.Start, Model: saved.Model, HasRequests: saved.HasRequests, Requests: saved.Requests}
	if saved.RestartAt != nil {
		ep.RestartAt = saved.RestartAt.UTC()
	}
	return ep
}

func (w *Watch) Enabled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enabled
}

func (w *Watch) SetEnabled(on bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	prev, prevEp := w.enabled, w.ep
	w.enabled = on
	w.ep = nil
	if err := w.persistLocked(); err != nil {
		w.enabled, w.ep = prev, prevEp
		return err
	}
	return nil
}

// Observe updates the current task from one fresh device sample. It never
// returns an error: a restart decision must not fail device collection.
func (w *Watch) Observe(_ context.Context, sample telemetry.Sample) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.enabled {
		return nil
	}
	at := sample.CollectedAt.UTC()
	if !activeQwen(sample) || at.IsZero() {
		if w.ep != nil {
			w.ep = nil
			if err := w.persistLocked(); err != nil {
				w.log.Warn("long task restart checkpoint failed", "error", "persist")
			}
		}
		return nil
	}
	next := episodeFromSample(sample, at)
	if w.ep == nil || !sameTask(*w.ep, next) || at.Before(w.ep.LastSeen) || at.Sub(w.ep.LastSeen) > maxGap {
		w.ep = &next
		if err := w.persistLocked(); err != nil {
			w.log.Warn("long task restart checkpoint failed", "error", "persist")
		}
		return nil
	}
	w.ep.LastSeen = at
	if err := w.persistLocked(); err != nil {
		w.log.Warn("long task restart checkpoint failed", "error", "persist")
	}
	if at.Sub(w.ep.Since) <= limit {
		return nil
	}
	if !w.ep.RestartAt.IsZero() && at.Sub(w.ep.RestartAt) < restartCooldown {
		return nil
	}
	if w.restart == nil || w.restart.Busy() {
		if w.restart == nil && !w.ep.noted {
			w.ep.noted = true
			w.log.Warn("qwen long task restart skipped", "error", "control_unavailable")
		}
		return nil
	}
	job, err := w.restart.ForceRestart()
	if err != nil {
		if !w.ep.noted {
			w.ep.noted = true
			code := "control_unavailable"
			var ce *control.Error
			if errors.As(err, &ce) {
				code = ce.Code
			}
			w.log.Warn("qwen long task restart skipped", "error", code)
		}
		return nil
	}
	w.ep.RestartAt = at
	w.ep.noted = false
	if err = w.persistLocked(); err != nil {
		w.log.Warn("long task restart checkpoint failed", "error", "persist")
	}
	w.log.Info("qwen long task force restart submitted", "job_id", job.ID)
	return nil
}

func episodeFromSample(sample telemetry.Sample, at time.Time) episode {
	ep := episode{Since: at, LastSeen: at, PID: *sample.ProviderPID, Model: *sample.CurrentModel}
	if sample.ProcessStartMicros != nil {
		ep.Start = *sample.ProcessStartMicros
	}
	if sample.RequestsServed != nil {
		ep.HasRequests = true
		ep.Requests = *sample.RequestsServed
	}
	return ep
}

func sameTask(a, b episode) bool {
	return a.PID == b.PID && a.Start == b.Start && a.Model == b.Model && a.HasRequests == b.HasRequests && (!a.HasRequests || a.Requests == b.Requests)
}

func activeQwen(sample telemetry.Sample) bool {
	if sample.Quality["inference_active"] != telemetry.OK || sample.InferenceActive == nil || !*sample.InferenceActive {
		return false
	}
	if sample.Quality["current_model"] != telemetry.OK || sample.CurrentModel == nil || !isQwen359B(*sample.CurrentModel) {
		return false
	}
	if sample.ProviderRunning != nil && !*sample.ProviderRunning {
		return false
	}
	// Without requests_served, back-to-back short requests look like one task.
	if sample.RequestsServed == nil {
		return false
	}
	return sample.ProviderPID != nil && *sample.ProviderPID > 0
}

func isQwen359B(model string) bool {
	model = strings.TrimSpace(model)
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	model = strings.ToLower(strings.TrimSpace(model))
	model = strings.ReplaceAll(model, " ", "-")
	model = strings.ReplaceAll(model, "_", "-")
	for strings.Contains(model, "--") {
		model = strings.ReplaceAll(model, "--", "-")
	}
	return model == "qwen3.5-9b"
}

func (w *Watch) persistLocked() error {
	saved := fileState{Enabled: w.enabled}
	if w.ep != nil {
		since, seen := w.ep.Since.UTC(), w.ep.LastSeen.UTC()
		saved.Since = &since
		saved.LastSeen = &seen
		saved.PID = w.ep.PID
		saved.Start = w.ep.Start
		saved.Model = w.ep.Model
		saved.HasRequests = w.ep.HasRequests
		saved.Requests = w.ep.Requests
		if !w.ep.RestartAt.IsZero() {
			at := w.ep.RestartAt.UTC()
			saved.RestartAt = &at
		}
	}
	body, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(w.path), ".long-task-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), w.path)
}
