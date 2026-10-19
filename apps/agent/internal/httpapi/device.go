package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/store"
	"bloomer/agent/internal/telemetry"
)

func (h *Handler) deviceState(ctx context.Context, w http.ResponseWriter, r *http.Request, id string, now time.Time) {
	history := r.URL.Path == "/v1/device/state/history"
	q := r.URL.Query()
	names := []string{}
	if history {
		names = []string{"from", "to", "before_id", "limit"}
	}
	if err := validateParams(q, names...); err != nil {
		queryFailure(w, err, id)
		return
	}
	from, to := now.Add(-24*time.Hour), now.Add(time.Microsecond)
	limit, before := 100, int64(0)
	if !history {
		from = now.Add(-config.Retention)
		limit = 1
	} else {
		for _, value := range []struct {
			name   string
			target *time.Time
		}{{"from", &from}, {"to", &to}} {
			if q.Has(value.name) {
				v, err := parseInstant(q.Get(value.name))
				if err != nil {
					queryFailure(w, err, id)
					return
				}
				*value.target = v
			}
		}
		if q.Has("limit") {
			v, err := strconv.Atoi(q.Get("limit"))
			if err != nil || v < 1 || v > 500 {
				queryFailure(w, bad("Limit must be between 1 and 500"), id)
				return
			}
			limit = v
		}
		if q.Has("before_id") {
			v, err := strconv.ParseInt(q.Get("before_id"), 10, 64)
			if err != nil || v <= 0 {
				queryFailure(w, bad("before_id must be a positive integer"), id)
				return
			}
			before = v
		}
		if !from.Before(to) {
			queryFailure(w, bad("from must be earlier than to"), id)
			return
		}
	}
	device, err := h.store.DeviceIdentity(ctx)
	if err != nil {
		queryFailure(w, err, id)
		return
	}
	var page store.DevicePage
	if history {
		page, err = h.store.DeviceSamples(ctx, now, from, to, before, limit)
	} else {
		var latest *telemetry.Sample
		latest, err = h.store.LatestDeviceSample(ctx, now)
		if latest != nil {
			page.Items = append(page.Items, *latest)
		}
	}
	if err != nil {
		queryFailure(w, err, id)
		return
	}
	cutoff := now.Add(-config.Retention)
	truncated := from.Before(cutoff)
	if truncated {
		from = cutoff
	}
	metadata := map[string]any{"sample_interval_seconds": int(telemetry.Interval.Seconds()), "retention_start": cutoff, "effective_from": from, "effective_to": to, "range_truncated": truncated}
	if history {
		writeJSON(w, 200, map[string]any{"device": device, "items": page.Items, "next_before_id": page.NextBeforeID, "meta": metadata})
		return
	}
	var sample *telemetry.Sample
	stale := true
	if len(page.Items) > 0 {
		sample = &page.Items[0]
		stale = sample.CollectedAt.After(now) || now.Sub(sample.CollectedAt) > config.StaleAfter
	}
	metadata["data_stale"] = stale
	writeJSON(w, 200, map[string]any{"device": device, "sample": sample, "meta": metadata})
}
