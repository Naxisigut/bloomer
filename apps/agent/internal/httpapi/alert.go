package httpapi

import (
	"errors"
	"io"
	"net/http"

	"bloomer/agent/internal/alert"
)

func (h *Handler) WithAlerts(manager *alert.Manager) *Handler { h.alerts = manager; return h }
func (h *Handler) alertEndpoints(w http.ResponseWriter, r *http.Request, id string) {
	method := http.MethodGet
	if r.URL.Path == "/v1/alerts/test" {
		method = http.MethodPost
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		failure(w, 405, "method_not_allowed", "Method unavailable", id)
		return
	}
	if r.URL.RawQuery != "" {
		failure(w, 400, "invalid_request", "Query parameters are not supported", id)
		return
	}
	ctx, cancel := contextWithTimeout(r)
	defer cancel()
	if method == http.MethodGet {
		records, err := alert.Records(ctx, h.store)
		if err != nil {
			failure(w, 503, "database_unavailable", "Alert history unavailable", id)
			return
		}
		writeJSON(w, 200, struct {
			Items []alert.Record `json:"items"`
		}{records})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2))
	if err != nil || len(body) > 0 {
		failure(w, 400, "invalid_request", "Test push requires an empty body", id)
		return
	}
	if h.alerts == nil {
		failure(w, 503, "alerts_disabled", "Alerts are disabled", id)
		return
	}
	record, err := h.alerts.Test(ctx)
	if errors.Is(err, alert.ErrDisabled) {
		failure(w, 503, "alerts_disabled", "Alerts are disabled", id)
		return
	}
	if err != nil {
		failure(w, 503, "database_unavailable", "Test push could not be queued", id)
		return
	}
	writeJSON(w, 202, record)
}
