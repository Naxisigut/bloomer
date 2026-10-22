package httpapi

import (
	"context"
	"net/http"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/telemetry"
)

func (h *Handler) deviceState(ctx context.Context, w http.ResponseWriter, r *http.Request, id string, now time.Time) {
	if err := validateParams(r.URL.Query()); err != nil {
		queryFailure(w, err, id)
		return
	}
	device, err := h.store.DeviceIdentity(ctx)
	if err != nil {
		queryFailure(w, err, id)
		return
	}
	sample, err := h.store.LatestDeviceSample(ctx)
	if err != nil {
		queryFailure(w, err, id)
		return
	}
	stale := true
	if sample != nil {
		stale = sample.CollectedAt.After(now) || now.Sub(sample.CollectedAt) > config.StaleAfter
	}
	metadata := map[string]any{
		"sample_interval_seconds": int(telemetry.Interval.Seconds()),
		"data_stale":              stale,
	}
	writeJSON(w, 200, map[string]any{"device": device, "sample": sample, "meta": metadata})
}
