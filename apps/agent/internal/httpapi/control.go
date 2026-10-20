package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"bloomer/agent/internal/control"
)

func (h *Handler) WithControl(manager *control.Manager) *Handler { h.control = manager; return h }
func (h *Handler) deviceControl(w http.ResponseWriter, r *http.Request, id string) {
	path := r.URL.Path
	if path != "/v1/device/control" && path != "/v1/device/control/models" && path != "/v1/device/control/jobs" {
		failure(w, 404, "not_found", "Endpoint unavailable", id)
		return
	}
	method := "GET"
	if path == "/v1/device/control/jobs" && r.Method == "POST" {
		method = "POST"
	}
	if r.Method != method {
		w.Header().Set("Allow", "GET")
		if path == "/v1/device/control/jobs" {
			w.Header().Set("Allow", "GET, POST")
		}
		failure(w, 405, "method_not_allowed", "Method unavailable", id)
		return
	}
	if r.URL.RawQuery != "" {
		failure(w, 400, "invalid_request", "Query parameters are not supported", id)
		return
	}
	if h.control == nil {
		failure(w, 503, "control_unavailable", "Remote control unavailable", id)
		return
	}
	var body any
	var err error
	switch {
	case path == "/v1/device/control":
		body = h.control.Status()
	case path == "/v1/device/control/models":
		ctx, cancel := contextWithTimeout(r)
		defer cancel()
		body, err = h.control.Models(ctx)
	case r.Method == "GET":
		body = struct {
			Jobs []control.Job `json:"jobs"`
		}{h.control.Status().Jobs}
	default:
		if r.Header.Get("Content-Type") != "application/json" {
			failure(w, 415, "invalid_request", "Use application/json", id)
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		decoder.DisallowUnknownFields()
		var request *control.Request
		if decoder.Decode(&request) != nil || request == nil || decoder.Decode(new(any)) != io.EOF {
			failure(w, 400, "invalid_request", "Invalid control request", id)
			return
		}
		body, err = h.control.Submit(*request)
	}
	if err != nil {
		var e *control.Error
		if errors.As(err, &e) {
			failure(w, e.Status, e.Code, "Remote operation unavailable", id)
		} else {
			failure(w, 503, "control_unavailable", "Remote control unavailable", id)
		}
		return
	}
	status := 200
	if r.Method == "POST" {
		status = 202
	}
	writeJSON(w, status, body)
}
