// Package httpapi exposes authenticated process and collection health.
// All queries read one bounded local SQLite snapshot.
package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"bloomer/agent/internal/alert"
	"bloomer/agent/internal/config"
	"bloomer/agent/internal/control"
	"bloomer/agent/internal/health"
	"bloomer/agent/internal/longtask"
	"bloomer/agent/internal/netrates"
	"bloomer/agent/internal/store"
)

type Handler struct {
	alerts             *alert.Manager
	control            *control.Manager
	networkRates       *netrates.Runner
	longTask           *longtask.Watch
	store              *store.Store
	tokenHash          [32]byte
	version, accountID string
	runtime            *health.Runtime
	now                func() time.Time
	deviceStateEnabled bool
}

func New(s *store.Store, token, version, accountID string, runtime *health.Runtime) *Handler {
	return &Handler{store: s, tokenHash: sha256.Sum256([]byte(token)), version: version, accountID: accountID, runtime: runtime, now: time.Now, deviceStateEnabled: true}
}

func (h *Handler) WithDeviceStateEnabled(enabled bool) *Handler {
	h.deviceStateEnabled = enabled
	return h
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
	// Public CORS policy; actual requests still require the independent Bearer token.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		failure(w, 503, "unavailable", "Request identifier unavailable", "")
		return
	}
	requestID := hex.EncodeToString(id)
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !cors(w, r, requestID) {
		return
	}
	auth := r.Header.Get("Authorization")
	hash := sha256.Sum256([]byte(strings.TrimPrefix(auth, "Bearer ")))
	if !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare(hash[:], h.tokenHash[:]) != 1 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		failure(w, 401, "unauthorized", "A valid access token is required", requestID)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/device/control") {
		h.deviceControl(w, r, requestID)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/device/network-rates") {
		h.deviceNetworkRates(w, r, requestID)
		return
	}
	if r.URL.Path == "/v1/device/long-task-restart" {
		h.longTaskRestart(w, r, requestID)
		return
	}
	if r.URL.Path == "/v1/alerts" || r.URL.Path == "/v1/alerts/test" {
		h.alertEndpoints(w, r, requestID)
		return
	}
	if r.URL.Path != "/v1/health" && r.URL.Path != "/v1/account/overview" && r.URL.Path != "/v1/account/summary" && r.URL.Path != "/v1/earnings" && r.URL.Path != "/v1/earnings/stats" && r.URL.Path != "/v1/earnings/analysis" && r.URL.Path != "/v1/device/state" {
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
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		queryFailure(w, bad("Malformed query string"), requestID)
		return
	}
	now := h.now().UTC().Truncate(time.Microsecond)
	switch r.URL.Path {
	case "/v1/device/state":
		h.deviceState(ctx, w, r, requestID, now)
		return
	case "/v1/account/overview":
		h.overview(ctx, w, r, requestID, now)
		return
	case "/v1/account/summary":
		h.summary(ctx, w, r, requestID, now)
		return
	case "/v1/earnings":
		h.earnings(ctx, w, r, requestID, now)
		return
	case "/v1/earnings/stats":
		h.stats(ctx, w, r, requestID, now)
		return
	case "/v1/earnings/analysis":
		h.earningsAnalysis(ctx, w, r, requestID, now)
		return
	}
	schema, err := h.store.SchemaVersion(ctx)
	if err != nil {
		failure(w, 503, "database_unavailable", "Local database is unavailable", requestID)
		return
	}
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
	var nextCollectionAt *string
	if collectionState != "disabled" {
		nextCollectionAt = formatTime(runtime.NextCollectionAt)
	}
	writeJSON(w, 200, map[string]any{
		"version": h.version, "phase": "P6", "collection_state": collectionState, "schema_version": schema,
		"server_time": now.Format(time.RFC3339Nano), "next_collection_at": nextCollectionAt,
		"last_successful_sync_at": formatTime(state.LastSuccess), "data_stale": stale, "last_error": optionalString(code),
		"last_attempt_at": formatTime(state.LastAttempt), "last_clean_shutdown_at": formatTime(state.LastShutdown),
		"sync_mode": optionalString(state.LastMode), "recovery_reason": optionalString(reason), "retry_not_before": formatTime(state.RetryNotBefore),
		"retention_start": now.Add(-config.Retention).Format(time.RFC3339Nano),
		"coverage":        map[string]any{"status": coverage.Status, "start": formatTime(coverage.Start), "gap_count": coverage.GapCount, "gaps": coverage.Gaps, "gaps_truncated": coverage.GapsTruncated},
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
