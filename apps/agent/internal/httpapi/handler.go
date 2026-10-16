// Package httpapi exposes authenticated process and collection health.
// Earnings queries will be implemented and finalized in P4.
package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/health"
	"bloomer/agent/internal/store"
)

type Handler struct {
	store              *store.Store
	tokenHash          [32]byte
	version, accountID string
	runtime            *health.Runtime
}

func New(s *store.Store, token, version, accountID string, runtime *health.Runtime) http.Handler {
	return &Handler{store: s, tokenHash: sha256.Sum256([]byte(token)), version: version, accountID: accountID, runtime: runtime}
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, code, message, id string) {
	writeJSON(w, status, APIError{code, message, id})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		failure(w, 503, "unavailable", "Request identifier unavailable", "")
		return
	}
	requestID := hex.EncodeToString(id)
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	auth := r.Header.Get("Authorization")
	hash := sha256.Sum256([]byte(strings.TrimPrefix(auth, "Bearer ")))
	if !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare(hash[:], h.tokenHash[:]) != 1 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		failure(w, 401, "unauthorized", "A valid access token is required", requestID)
		return
	}
	if r.Header.Get("Origin") != "" {
		failure(w, 403, "origin_forbidden", "This origin is not configured", requestID)
		return
	}
	if r.URL.Path != "/v1/health" {
		failure(w, 404, "not_found", "Endpoint is unavailable in this build", requestID)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		failure(w, 405, "method_not_allowed", "Use GET", requestID)
		return
	}
	ctx, cancel := contextWithTimeout(r)
	defer cancel()
	schema, err := h.store.SchemaVersion(ctx)
	if err != nil {
		failure(w, 503, "database_unavailable", "Local database is unavailable", requestID)
		return
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	state, coverage, err := h.store.LoadHealth(ctx, h.accountID, now)
	if err != nil {
		failure(w, 503, "database_unavailable", "Local collection state is unavailable", requestID)
		return
	}
	runtime := h.runtime.Snapshot()
	code := runtime.LastError
	if code == "" {
		code = state.LastError
	}
	collectionState := runtime.CollectionState
	if collectionState != "disabled" && collectionState != "collecting" {
		if code != "" {
			collectionState = "degraded"
		}
		if state.RetryNotBefore.After(now) {
			collectionState = "rate_limited"
		}
	}
	reason := runtime.RecoveryReason
	if reason == "" {
		reason = state.RecoveryReason
	}
	cleanupState := runtime.CleanupState
	cleanupError := runtime.CleanupError
	if cleanupError == "" {
		cleanupError = state.CleanupError
	}
	if cleanupError != "" {
		cleanupState = "failed"
	}
	stale := state.LastSuccess.IsZero() || state.LastSuccess.After(now) || now.Sub(state.LastSuccess) > config.StaleAfter
	writeJSON(w, 200, map[string]any{
		"version": h.version, "phase": "P3", "collection_state": collectionState, "schema_version": schema,
		"last_successful_sync_at": formatTime(state.LastSuccess), "data_stale": stale, "last_error": optionalString(code),
		"last_attempt_at": formatTime(state.LastAttempt), "last_clean_shutdown_at": formatTime(state.LastShutdown),
		"sync_mode": optionalString(state.LastMode), "recovery_reason": optionalString(reason), "retry_not_before": formatTime(state.RetryNotBefore),
		"retention_start": now.Add(-config.Retention).Format(time.RFC3339Nano),
		"coverage":        map[string]any{"status": coverage.Status, "start": formatTime(coverage.Start), "gap_count": coverage.GapCount},
		"cleanup":         map[string]any{"state": cleanupState, "last_success_at": formatTime(state.LastCleanup), "last_error": optionalString(cleanupError)},
	})
}
func formatTime(v time.Time) *string {
	if v.IsZero() {
		return nil
	}
	s := v.UTC().Format(time.RFC3339Nano)
	return &s
}
func optionalString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
