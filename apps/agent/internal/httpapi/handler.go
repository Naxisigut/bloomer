// Package httpapi exposes the P1 authenticated process health endpoint.
// Earnings queries will be implemented and finalized in P4.
package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/store"
)

type Handler struct {
	store              *store.Store
	tokenHash          [32]byte
	version, accountID string
}

func New(s *store.Store, token, version, accountID string) http.Handler {
	return &Handler{store: s, tokenHash: sha256.Sum256([]byte(token)), version: version, accountID: accountID}
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
	var success *string
	var coverageStart *string
	coverage := "uninitialized"
	if h.accountID != "" {
		var last, start sql.NullInt64
		err = h.store.ReadDB().QueryRowContext(ctx, "SELECT last_successful_sync_at_us, coverage_start_us, coverage_status FROM sync_state WHERE account_id=?", h.accountID).Scan(&last, &start, &coverage)
		if err != nil && err != sql.ErrNoRows {
			failure(w, 503, "database_unavailable", "Local database is unavailable", requestID)
			return
		}
		success = formatTime(last)
		coverageStart = formatTime(start)
	}
	// P1 intentionally runs without an upstream client or a scheduler.
	writeJSON(w, 200, map[string]any{
		"version": h.version, "phase": "P1", "collection_state": "disabled", "schema_version": schema,
		"last_successful_sync_at": success, "data_stale": true, "last_error": nil,
		"retention_start": time.Now().UTC().Add(-config.Retention).Format(time.RFC3339Nano),
		"coverage":        map[string]any{"status": coverage, "start": coverageStart},
		"cleanup":         map[string]any{"state": "not_implemented", "last_success_at": nil},
	})
}
func formatTime(v sql.NullInt64) *string {
	if !v.Valid {
		return nil
	}
	s := time.UnixMicro(v.Int64).UTC().Format(time.RFC3339Nano)
	return &s
}
