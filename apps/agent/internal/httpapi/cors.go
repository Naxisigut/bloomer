package httpapi

import (
	"net/http"
	"strings"
)

// CORS permits every browser origin without cookies or HTTP credentials.
// Only valid preflights bypass Bearer authentication.
func cors(w http.ResponseWriter, r *http.Request, id string) bool {
	if r.Header.Get("Origin") == "" || r.Method != http.MethodOptions {
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
