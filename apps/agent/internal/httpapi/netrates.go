package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"bloomer/agent/internal/netrates"
)

func (h *Handler) WithNetworkRates(runner *netrates.Runner) *Handler {
	h.networkRates = runner
	return h
}

func (h *Handler) deviceNetworkRates(w http.ResponseWriter, r *http.Request, id string) {
	path := r.URL.Path
	switch path {
	case "/v1/device/network-rates":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			failure(w, 405, "method_not_allowed", "Method unavailable", id)
			return
		}
	case "/v1/device/network-rates/settings":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			failure(w, 405, "method_not_allowed", "Method unavailable", id)
			return
		}
	case "/v1/device/network-rates/switches":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			failure(w, 405, "method_not_allowed", "Method unavailable", id)
			return
		}
	case "/v1/device/network-rates/token-trend":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			failure(w, 405, "method_not_allowed", "Method unavailable", id)
			return
		}
	default:
		failure(w, 404, "not_found", "Endpoint unavailable", id)
		return
	}
	if r.URL.RawQuery != "" {
		failure(w, 400, "invalid_request", "Query parameters are not supported", id)
		return
	}
	if h.networkRates == nil {
		if path == "/v1/device/network-rates/settings" {
			failure(w, 503, "network_rates_unavailable", "Network rates unavailable", id)
			return
		}
		if path == "/v1/device/network-rates/switches" {
			writeJSON(w, 200, map[string]any{"switches": []any{}})
			return
		}
		if path == "/v1/device/network-rates/token-trend" {
			writeJSON(w, 200, emptyTokenTrend())
			return
		}
		writeJSON(w, 200, disabledNetworkRates())
		return
	}
	ctx, cancel := contextWithTimeout(r)
	defer cancel()
	switch path {
	case "/v1/device/network-rates":
		writeJSON(w, 200, h.networkRates.Status())
	case "/v1/device/network-rates/token-trend":
		trend, err := h.networkRates.TokenTrend(ctx)
		if err != nil {
			failure(w, 503, "database_unavailable", "Local database is unavailable", id)
			return
		}
		writeJSON(w, 200, trend)
	case "/v1/device/network-rates/switches":
		rows, err := h.networkRates.Switches(ctx, 50)
		if err != nil {
			failure(w, 503, "database_unavailable", "Local database is unavailable", id)
			return
		}
		writeJSON(w, 200, map[string]any{"switches": rows})
	default:
		if r.Header.Get("Content-Type") != "application/json" {
			failure(w, 415, "invalid_request", "Use application/json", id)
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		decoder.DisallowUnknownFields()
		var body *struct {
			AutoSwitch bool `json:"auto_switch"`
		}
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
			failure(w, 400, "invalid_request", "Invalid network rates settings", id)
			return
		}
		view, err := h.networkRates.SetAutoSwitch(body.AutoSwitch)
		if err != nil {
			failure(w, 503, "network_rates_unavailable", "Network rates unavailable", id)
			return
		}
		writeJSON(w, 200, view)
	}
}

func emptyTokenTrend() netrates.TokenTrendView {
	return netrates.TokenTrendView{Series: []netrates.TokenTrendSeriesView{}}
}

func disabledNetworkRates() netrates.StatusView {
	return netrates.StatusView{
		Enabled: false, AutoSwitch: false, AutoSwitchSince: nil, WindowSamples: 6, Samples: 0, IntervalMinutes: 0,
		UpdatedAt: nil, SnapshotAt: nil, CurrentModel: nil, Recommendation: nil,
		Candidates: []netrates.CandidateView{}, Switch: netrates.SwitchGateView{}, LastError: nil,
		ChipTiers: []string{},
	}
}
