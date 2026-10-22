// Package control runs an allowlisted set of Darkbloom commands as durable,
// serialized background jobs. Provider output and credentials are never exposed.
package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sync"
	"time"

	"bloomer/agent/internal/localfs"
)

type Error struct {
	Code   string
	Status int
}

func (e *Error) Error() string           { return e.Code }
func fail(code string, status int) error { return &Error{code, status} }

type Request struct {
	RequestID string   `json:"request_id"`
	Action    string   `json:"action"`
	Models    []string `json:"models,omitempty"`
}
type Job struct {
	Request
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}
type Model struct {
	ID     string  `json:"id"`
	Name   string  `json:"display_name,omitempty"`
	SizeGB float64 `json:"size_gb,omitempty"`
}
type Models struct {
	Local        []Model `json:"local"`
	Catalog      []Model `json:"catalog"`
	LocalError   string  `json:"local_error,omitempty"`
	CatalogError string  `json:"catalog_error,omitempty"`
}
type Status struct {
	Enabled   bool  `json:"enabled"`
	Available bool  `json:"available"`
	Jobs      []Job `json:"jobs"`
}

type runner func(context.Context, string, ...string) ([]byte, error)
type Manager struct {
	mu           sync.Mutex
	probe        sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	enabled      bool
	closed       bool
	unhealthy    bool
	binary, path string
	jobs         []Job
	run          runner
}

func New(ctx context.Context, dir, binary string, enabled bool) (*Manager, error) {
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{ctx: ctx, cancel: cancel, enabled: enabled, binary: binary, path: filepath.Join(dir, "control-jobs.json"), jobs: []Job{}, run: command}
	f, err := localfs.OpenPrivate(m.path, os.O_RDONLY)
	if err == nil {
		defer f.Close()
		body, readErr := io.ReadAll(io.LimitReader(f, 262145))
		if readErr != nil || len(body) > 262144 || json.Unmarshal(body, &m.jobs) != nil || len(m.jobs) > 50 || m.jobs == nil {
			cancel()
			return nil, errors.New("invalid control job history")
		}
		for i := range m.jobs {
			if m.jobs[i].Status == "running" {
				now := time.Now().UTC()
				m.jobs[i].Status = "unconfirmed"
				m.jobs[i].Error = "agent_interrupted"
				m.jobs[i].FinishedAt = &now
			}
		}
		err = m.save()
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		cancel()
		return nil, errors.New("control history unavailable")
	}
	return m, nil
}
func (m *Manager) Close() { m.mu.Lock(); m.closed = true; m.cancel(); m.mu.Unlock(); m.wg.Wait() }
func (m *Manager) available() bool {
	st, err := os.Stat(m.binary)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := make([]Job, len(m.jobs))
	copy(jobs, m.jobs)
	for i := range jobs {
		jobs[i].Models = append([]string(nil), jobs[i].Models...)
	}
	return Status{m.enabled, m.enabled && !m.closed && m.ctx.Err() == nil && !m.unhealthy && m.available(), jobs}
}

func (m *Manager) LastModelChange() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest time.Time
	for _, j := range m.jobs {
		if j.Action != "switch" && j.Action != "start" {
			continue
		}
		if j.CreatedAt.After(latest) {
			latest = j.CreatedAt
		}
	}
	return latest
}

func (m *Manager) Job(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ID != id {
			continue
		}
		cp := j
		cp.Models = append([]string(nil), cp.Models...)
		return cp, true
	}
	return Job{}, false
}

func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.Status == "running" {
			return true
		}
	}
	return false
}

var modelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
var traversal = regexp.MustCompile(`(^|/)\.\.?(/|$)`)
var requestID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)

func validModel(id string) bool { return ValidModelID(id) }

// ValidModelID reports whether id satisfies the control allowlist for model identifiers.
func ValidModelID(id string) bool {
	return modelID.MatchString(id) && !traversal.MatchString(id)
}
func arguments(r Request) ([]string, error) {
	if !requestID.MatchString(r.RequestID) || len(r.Models) > 16 {
		return nil, fail("invalid_request", 400)
	}
	seen := map[string]bool{}
	for _, id := range r.Models {
		if !validModel(id) || seen[id] {
			return nil, fail("invalid_request", 400)
		}
		seen[id] = true
	}
	switch r.Action {
	case "start", "switch":
		if len(r.Models) == 0 {
			return nil, fail("invalid_request", 400)
		}
		args := []string{r.Action, "--timeout", "600"}
		for _, id := range r.Models {
			args = append(args, "--model", id)
		}
		return args, nil
	case "download":
		if len(r.Models) != 1 {
			return nil, fail("invalid_request", 400)
		}
		return []string{"models", "download", r.Models[0]}, nil
	case "stop", "restart":
		if len(r.Models) != 0 {
			return nil, fail("invalid_request", 400)
		}
		args := []string{r.Action, "--timeout", "600"}
		if r.Action == "restart" {
			args = append(args, "--startup-timeout", "180")
		}
		return args, nil
	default:
		return nil, fail("invalid_request", 400)
	}
}
func (m *Manager) Submit(r Request) (Job, error) {
	if len(r.Models) == 0 {
		r.Models = nil
	}
	args, err := arguments(r)
	if err != nil {
		return Job{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled {
		return Job{}, fail("control_disabled", 403)
	}
	for _, j := range m.jobs {
		if j.RequestID == r.RequestID {
			if j.Action != r.Action || !reflect.DeepEqual(j.Models, r.Models) {
				return Job{}, fail("request_conflict", 409)
			}
			return j, nil
		}
	}
	if m.closed || m.ctx.Err() != nil || m.unhealthy || !m.available() {
		return Job{}, fail("control_unavailable", 503)
	}
	for _, j := range m.jobs {
		if j.Status == "running" {
			return Job{}, fail("control_busy", 409)
		}
	}
	r.Models = append([]string(nil), r.Models...)
	sum := sha256.Sum256([]byte(r.RequestID))
	j := Job{Request: r, ID: hex.EncodeToString(sum[:12]), Status: "running", CreatedAt: time.Now().UTC()}
	previous := m.jobs
	m.jobs = append([]Job{j}, m.jobs...)
	if len(m.jobs) > 50 {
		m.jobs = m.jobs[:50]
	}
	if m.save() != nil {
		m.jobs = previous
		return Job{}, fail("control_unavailable", 503)
	}
	m.wg.Add(1)
	go func() { defer m.wg.Done(); m.execute(j.ID, r.Action, args) }()
	return j, nil
}
func (m *Manager) execute(id, action string, args []string) {
	timeout := 15 * time.Minute
	if action == "download" {
		timeout = 6 * time.Hour
	}
	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()
	_, err := m.run(ctx, m.binary, args...)
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.jobs {
		if m.jobs[i].ID == id {
			j := &m.jobs[i]
			now := time.Now().UTC()
			j.FinishedAt = &now
			j.Status = "succeeded"
			if err != nil {
				j.Status = "failed"
				j.Error = "command_failed"
			}
			if ctx.Err() != nil {
				j.Status = "unconfirmed"
				j.Error = "command_timeout"
				if m.ctx.Err() != nil {
					j.Error = "agent_interrupted"
				}
			}
			if m.save() != nil {
				j.Status = "unconfirmed"
				j.Error = "history_unavailable"
				m.unhealthy = true
			}
			return
		}
	}
}
func (m *Manager) save() error {
	body, err := json.Marshal(m.jobs)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(m.path), ".control-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), m.path)
}
func (m *Manager) Models(ctx context.Context) (Models, error) {
	m.mu.Lock()
	usable := m.enabled && !m.closed && m.available()
	m.mu.Unlock()
	if !usable {
		return Models{}, fail("control_unavailable", 503)
	}
	if !m.probe.TryLock() {
		return Models{}, fail("control_busy", 409)
	}
	defer m.probe.Unlock()
	result := Models{Local: []Model{}, Catalog: []Model{}}
	// Each list has its own deadline, so catalog failure preserves local choices.
	for _, kind := range []string{"list", "catalog"} {
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		args := []string{"models", kind, "--json"}
		if kind == "list" {
			args = append(args, "--all")
		}
		body, err := m.run(probeCtx, m.binary, args...)
		cancel()
		var models []Model
		if err == nil {
			if kind == "list" {
				var wrapper struct {
					Models *[]Model `json:"models"`
				}
				err = json.Unmarshal(body, &wrapper)
				if err == nil {
					if wrapper.Models == nil {
						err = errors.New("invalid models")
					} else {
						models = *wrapper.Models
					}
				}
			} else {
				err = json.Unmarshal(body, &models)
				if models == nil {
					err = errors.New("invalid catalog")
				}
			}
		}
		if err == nil {
			for _, model := range models {
				if !validModel(model.ID) || len(model.Name) > 300 {
					err = errors.New("invalid model")
					break
				}
			}
		}
		if err != nil {
			if kind == "list" {
				result.LocalError = "models_unavailable"
			} else {
				result.CatalogError = "catalog_unavailable"
			}
			continue
		}
		if kind == "list" {
			result.Local = models
		} else {
			result.Catalog = models
		}
	}
	return result, nil
}
