// Package notask restarts Darkbloom when the running provider receives no new
// work for more than 10 consecutive minutes. A submitted restart resets the
// idle clock so the next sample cannot immediately restart again.
package notask

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"bloomer/agent/internal/control"
	"bloomer/agent/internal/localfs"
	"bloomer/agent/internal/telemetry"
)

const (
	limit  = 10 * time.Minute
	maxGap = 3 * time.Minute
)

// Restarter is the narrow control surface used after a stretch of no new work.
type Restarter interface {
	IdleRestart() (control.Job, error)
	Busy() bool
}

type episode struct {
	IdleSince   time.Time
	LastSeen    time.Time
	PID         int
	Start       int64
	HasRequests bool
	Requests    uint64
	noted       bool
}

type fileState struct {
	Enabled     bool       `json:"enabled"`
	IdleSince   *time.Time `json:"idle_since,omitempty"`
	LastSeen    *time.Time `json:"last_seen,omitempty"`
	PID         int        `json:"pid,omitempty"`
	Start       int64      `json:"process_start_us,omitempty"`
	HasRequests bool       `json:"has_requests_served,omitempty"`
	Requests    uint64     `json:"requests_served,omitempty"`
}

// Watch remembers the last time work arrived and asks Darkbloom to restart.
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
		path:           filepath.Join(dataDir, "no-task-restart.json"),
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
		w.log.Warn("no task restart settings unavailable", "error", "unreadable")
		return
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, 8193))
	var saved fileState
	if err != nil || len(body) > 8192 || json.Unmarshal(body, &saved) != nil || saved.PID < 0 {
		w.log.Warn("no task restart settings unavailable", "error", "corrupt")
		return
	}
	w.enabled = saved.Enabled
	w.ep = episodeFrom(saved, now.UTC())
}

func episodeFrom(saved fileState, now time.Time) *episode {
	if !saved.Enabled || saved.IdleSince == nil || saved.LastSeen == nil || saved.PID <= 0 || !saved.HasRequests {
		return nil
	}
	since, seen := saved.IdleSince.UTC(), saved.LastSeen.UTC()
	if seen.Before(since) || now.Before(seen) || now.Sub(seen) > maxGap {
		return nil
	}
	return &episode{IdleSince: since, LastSeen: seen, PID: saved.PID, Start: saved.Start, HasRequests: saved.HasRequests, Requests: saved.Requests}
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

// Observe updates the idle clock from one fresh device sample. It never
// returns an error: a restart decision must not fail device collection.
func (w *Watch) Observe(_ context.Context, sample telemetry.Sample) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.enabled {
		return nil
	}
	at := sample.CollectedAt.UTC()
	if !watchable(sample) || at.IsZero() {
		if w.ep != nil {
			w.ep = nil
			if err := w.persistLocked(); err != nil {
				w.log.Warn("no task restart checkpoint failed", "error", "persist")
			}
		}
		return nil
	}
	next := episodeFromSample(sample, at)
	if w.ep == nil || !sameProcess(*w.ep, next) || at.Before(w.ep.LastSeen) || at.Sub(w.ep.LastSeen) > maxGap || receivedWork(*w.ep, next, sample) {
		w.ep = &next
		if err := w.persistLocked(); err != nil {
			w.log.Warn("no task restart checkpoint failed", "error", "persist")
		}
		return nil
	}
	w.ep.LastSeen = at
	if err := w.persistLocked(); err != nil {
		w.log.Warn("no task restart checkpoint failed", "error", "persist")
	}
	if at.Sub(w.ep.IdleSince) <= limit {
		return nil
	}
	if w.restart == nil || w.restart.Busy() {
		if w.restart == nil && !w.ep.noted {
			w.ep.noted = true
			w.log.Warn("no task restart skipped", "error", "control_unavailable")
		}
		return nil
	}
	job, err := w.restart.IdleRestart()
	if err != nil {
		if !w.ep.noted {
			w.ep.noted = true
			code := "control_unavailable"
			var ce *control.Error
			if errors.As(err, &ce) {
				code = ce.Code
			}
			w.log.Warn("no task restart skipped", "error", code)
		}
		return nil
	}
	// Reset the idle clock at submit time so a still-idle process cannot
	// immediately qualify for another restart on the next sample.
	w.ep.IdleSince = at
	w.ep.LastSeen = at
	w.ep.noted = false
	if err = w.persistLocked(); err != nil {
		w.log.Warn("no task restart checkpoint failed", "error", "persist")
	}
	w.log.Info("no task restart submitted", "job_id", job.ID)
	return nil
}

func episodeFromSample(sample telemetry.Sample, at time.Time) episode {
	ep := episode{IdleSince: at, LastSeen: at, PID: *sample.ProviderPID}
	if sample.ProcessStartMicros != nil {
		ep.Start = *sample.ProcessStartMicros
	}
	if sample.RequestsServed != nil {
		ep.HasRequests = true
		ep.Requests = *sample.RequestsServed
	}
	return ep
}

func sameProcess(a, b episode) bool {
	return a.PID == b.PID && a.Start == b.Start && a.HasRequests == b.HasRequests
}

func receivedWork(prev, next episode, sample telemetry.Sample) bool {
	if hasCurrentTask(sample) {
		return true
	}
	return prev.HasRequests && next.HasRequests && next.Requests != prev.Requests
}

func hasCurrentTask(sample telemetry.Sample) bool {
	if sample.Quality["inference_active"] == telemetry.OK && sample.InferenceActive != nil && *sample.InferenceActive {
		return true
	}
	return sample.Quality["request_work_pending"] == telemetry.OK && sample.RequestWorkPending != nil && *sample.RequestWorkPending
}

func watchable(sample telemetry.Sample) bool {
	if sample.ProviderRunning == nil || !*sample.ProviderRunning {
		return false
	}
	if sample.Quality["inference_active"] != telemetry.OK || sample.InferenceActive == nil {
		return false
	}
	if sample.Quality["request_work_pending"] != telemetry.OK || sample.RequestWorkPending == nil {
		return false
	}
	if sample.RequestsServed == nil {
		return false
	}
	return sample.ProviderPID != nil && *sample.ProviderPID > 0
}

func (w *Watch) persistLocked() error {
	saved := fileState{Enabled: w.enabled}
	if w.ep != nil {
		since, seen := w.ep.IdleSince.UTC(), w.ep.LastSeen.UTC()
		saved.IdleSince = &since
		saved.LastSeen = &seen
		saved.PID = w.ep.PID
		saved.Start = w.ep.Start
		saved.HasRequests = w.ep.HasRequests
		saved.Requests = w.ep.Requests
	}
	body, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(w.path), ".no-task-*")
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
