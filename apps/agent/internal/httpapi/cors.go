package httpapi

import (
	"net/http"
	"strings"
)

// WithAllowedOrigins is configured once at startup. Empty keeps CLI-only access.
func (h *Handler) WithAllowedOrigins(origins []string) *Handler {
	h.allowedOrigins = make(map[string]bool, len(origins))
	for _, origin := range origins {
		h.allowedOrigins[origin] = true
	}
	return h
}

func (h *Handler) cors(w http.ResponseWriter, r *http.Request, id string) bool {
	w.Header().Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // Existing authenticated CLI and health callers are unchanged.
	}
	if len(r.Header.Values("Origin")) != 1 || !h.allowedOrigins[origin] {
		failure(w, 403, "origin_forbidden", "This origin is not configured", id)
		return false
	}
	// Set before authentication and routing so allowed browsers can read errors.
	w.Header().Set("Access-Control-Allow-Origin", origin)
	if r.Method != http.MethodOptions {
		return true
	}
	w.Header().Add("Vary", "Access-Control-Request-Method")
	w.Header().Add("Vary", "Access-Control-Request-Headers")
	method := r.Header.Get("Access-Control-Request-Method")
	if method != http.MethodGet && method != http.MethodPost {
		failure(w, 403, "cors_forbidden", "Preflight method is not allowed", id)
		return false
	}
	if headers := r.Header.Get("Access-Control-Request-Headers"); headers != "" {
		for _, header := range strings.Split(headers, ",") {
			switch strings.ToLower(strings.TrimSpace(header)) {
			case "authorization", "content-type", "accept":
			default:
				failure(w, 403, "cors_forbidden", "Preflight header is not allowed", id)
				return false
			}
		}
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
	w.WriteHeader(http.StatusNoContent)
	return false // Preflight carries no access token and never executes a query/job.
}
