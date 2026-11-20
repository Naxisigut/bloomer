package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"bloomer/agent/internal/notask"
)

func (h *Handler) WithNoTask(watch *notask.Watch) *Handler {
	h.noTask = watch
	return h
}

func (h *Handler) noTaskRestart(w http.ResponseWriter, r *http.Request, id string) {
	if r.URL.Path != "/v1/device/no-task-restart" {
		failure(w, 404, "not_found", "Endpoint unavailable", id)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		failure(w, 405, "method_not_allowed", "Method unavailable", id)
		return
	}
	if r.URL.RawQuery != "" {
		failure(w, 400, "invalid_request", "Query parameters are not supported", id)
		return
	}
	if h.noTask == nil {
		if r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]any{"enabled": false})
			return
		}
		failure(w, 503, "no_task_restart_unavailable", "No task restart unavailable", id)
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"enabled": h.noTask.Enabled()})
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		failure(w, 415, "invalid_request", "Use application/json", id)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	var body *struct {
		Enabled bool `json:"enabled"`
	}
	if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
		failure(w, 400, "invalid_request", "Invalid no task restart settings", id)
		return
	}
	if err := h.noTask.SetEnabled(body.Enabled); err != nil {
		failure(w, 503, "no_task_restart_unavailable", "No task restart unavailable", id)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": h.noTask.Enabled()})
}
