package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"bloomer/agent/internal/store"
)

const maxAnalysisSpan = 366 * 24 * time.Hour
const maxAnalysisHourSpan = 31 * 24 * time.Hour

func validateAnalysisParams(q url.Values) ([]string, error) {
	allowed := map[string]int{"from": 1, "to": 1, "timezone": 1, "granularity": 1, "model": 8}
	for key, values := range q {
		limit, ok := allowed[key]
		if !ok || len(values) > limit || len(values) == 0 {
			return nil, bad("Unknown or repeated query parameter")
		}
	}
	models := q["model"]
	seen := map[string]bool{}
	for _, m := range models {
		if len(m) > 1024 {
			return nil, bad("Model filter exceeds size limit")
		}
		if seen[m] {
			return nil, bad("Duplicate model filter")
		}
		seen[m] = true
	}
	return models, nil
}

func (h *Handler) parseAnalysisFilters(q url.Values, now time.Time) (store.QueryRange, string, []string, error) {
	models, err := validateAnalysisParams(q)
	if err != nil {
		return store.QueryRange{}, "", nil, err
	}
	if !q.Has("from") || !q.Has("to") {
		return store.QueryRange{}, "", nil, bad("from and to are required")
	}
	from, err := parseInstant(q.Get("from"))
	if err != nil {
		return store.QueryRange{}, "", nil, err
	}
	to, err := parseInstant(q.Get("to"))
	if err != nil {
		return store.QueryRange{}, "", nil, err
	}
	if !from.Before(to) {
		return store.QueryRange{}, "", nil, bad("from must be earlier than to")
	}
	span := to.Sub(from)
	if span > maxAnalysisSpan {
		return store.QueryRange{}, "", nil, bad("Query range exceeds 366 days")
	}
	granularity := q.Get("granularity")
	if granularity == "" {
		granularity = "day"
	}
	if granularity != "hour" && granularity != "day" && granularity != "month" {
		return store.QueryRange{}, "", nil, bad("Unsupported granularity")
	}
	if granularity == "hour" && span > maxAnalysisHourSpan {
		return store.QueryRange{}, "", nil, bad("Hour granularity allows at most 31 days")
	}
	window, err := store.RetainedRange(now, from, to)
	if err != nil {
		return store.QueryRange{}, "", nil, bad("from must be earlier than to")
	}
	return window, granularity, models, nil
}

func (h *Handler) earningsAnalysis(ctx context.Context, w http.ResponseWriter, r *http.Request, id string, now time.Time) {
	q := r.URL.Query()
	window, granularity, models, err := h.parseAnalysisFilters(q, now)
	if err != nil {
		queryFailure(w, err, id)
		return
	}
	zone := q.Get("timezone")
	if zone == "" || zone == "Local" || len(zone) > 128 {
		queryFailure(w, bad("An explicit IANA time zone is required"), id)
		return
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		queryFailure(w, bad("Unknown IANA time zone"), id)
		return
	}
	var body map[string]any
	err = h.store.Query(ctx, h.accountID, now, func(v *store.ReadView) error {
		result, err := v.EarningsAnalysis(ctx, window, granularity, models, location, zone)
		if err != nil {
			return err
		}
		body = map[string]any{
			"timezone": result.Timezone, "granularity": result.Granularity,
			"from": result.From, "to": result.To,
			"totals": result.Totals, "models": result.Models, "series": result.Series,
			"hour_of_day": result.HourOfDay, "weekday_hour": result.WeekdayHour,
			"meta": meta(v, window, now),
		}
		return nil
	})
	if err != nil {
		queryFailure(w, err, id)
		return
	}
	writeJSON(w, 200, body)
}
