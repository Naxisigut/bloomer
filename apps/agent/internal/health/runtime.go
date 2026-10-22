// Package health keeps transient activity and write-failure status available to queries.
package health

import (
	"sync"
	"time"
)

type Status struct {
	CollectionState  string
	LastError        string
	RecoveryReason   string
	CleanupState     string
	CleanupError     string
	NextCollectionAt time.Time
}
type Runtime struct {
	mu     sync.RWMutex
	status Status
}

func New() *Runtime { return &Runtime{status: Status{CollectionState: "idle", CleanupState: "idle"}} }
func (r *Runtime) Snapshot() Status {
	if r == nil {
		return Status{CollectionState: "idle", CleanupState: "idle"}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}
func (r *Runtime) Collection(state, code, reason string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.CollectionState = state
	r.status.LastError = code
	r.status.RecoveryReason = reason
	if state == "disabled" {
		r.status.NextCollectionAt = time.Time{}
	}
}
func (r *Runtime) SetNextCollectionAt(t time.Time) {
	if r == nil || t.IsZero() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.CollectionState == "disabled" {
		return
	}
	r.status.NextCollectionAt = t.UTC()
}
func (r *Runtime) Cleanup(state, code string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.CleanupState = state
	r.status.CleanupError = code
}
