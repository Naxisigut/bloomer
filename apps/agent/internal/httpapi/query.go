package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/store"
)

type queryError struct{ code, message string }

func (e *queryError) Error() string { return e.code }
func bad(message string) error      { return &queryError{"invalid_request", message} }

type cursor struct {
	Version int           `json:"v"`
	Account string        `json:"account"`
	From    int64         `json:"from"`
	To      int64         `json:"to"`
	Model   string        `json:"model"`
	After   store.SortKey `json:"after"`
}

func (h *Handler) encodeCursor(c cursor) (string, error) {
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, h.tokenHash[:])
	mac.Write([]byte("bloomer-page-v1\x00"))
	mac.Write(body)
	value := base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(value) > 4096 {
		return "", bad("The record identifier is too long for pagination")
	}
	return value, nil
}
func (h *Handler) decodeCursor(value string) (cursor, error) {
	var c cursor
	if len(value) > 4096 {
		return c, bad("Cursor exceeds size limit")
	}
	left, right, ok := strings.Cut(value, ".")
	if !ok {
		return c, bad("Invalid cursor")
	}
	body, err := base64.RawURLEncoding.DecodeString(left)
	if err != nil {
		return c, bad("Invalid cursor")
	}
	signature, err := base64.RawURLEncoding.DecodeString(right)
	if err != nil {
		return c, bad("Invalid cursor")
	}
	mac := hmac.New(sha256.New, h.tokenHash[:])
	mac.Write([]byte("bloomer-page-v1\x00"))
	mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return c, bad("Invalid cursor signature")
	}
	if json.Unmarshal(body, &c) != nil || c.Version != 1 || c.Account == "" || c.From >= c.To || c.After.ID == "" || c.After.At < c.From || c.After.At >= c.To {
		return c, bad("Invalid cursor contents")
	}
	return c, nil
}

type filters struct {
	from, to time.Time
	model    string
	limit    int
	cursor   *cursor
	window   store.QueryRange
}

func parseInstant(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || t.IsZero() || t.Nanosecond()%1000 != 0 {
		return time.Time{}, bad("Times must be RFC3339 instants with at most microsecond precision")
	}
	return t.UTC(), nil
}
func validateParams(q url.Values, names ...string) error {
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	for key, values := range q {
		if !allowed[key] || len(values) != 1 {
			return bad("Unknown or repeated query parameter")
		}
	}
	return nil
}
func (h *Handler) parseFilters(q url.Values, now time.Time, paging bool) (filters, error) {
	f := filters{from: now.Add(-config.Retention), to: now, limit: 100, model: q.Get("model")}
	names := []string{"from", "to", "model", "group_by", "timezone"}
	if paging {
		names = []string{"from", "to", "model", "limit", "cursor"}
	}
	if err := validateParams(q, names...); err != nil {
		return f, err
	}
	if len(f.model) > 1024 {
		return f, bad("Model filter exceeds size limit")
	}
	if paging && q.Has("cursor") {
		c, err := h.decodeCursor(q.Get("cursor"))
		if err != nil {
			return f, err
		}
		f.cursor = &c
		f.from = time.UnixMicro(c.From).UTC()
		f.to = time.UnixMicro(c.To).UTC()
		if q.Has("model") && q.Get("model") != c.Model {
			return f, bad("Cursor model filter does not match")
		}
		f.model = c.Model
	}
	for _, entry := range []struct {
		name   string
		target *time.Time
	}{{"from", &f.from}, {"to", &f.to}} {
		if q.Has(entry.name) {
			value, err := parseInstant(q.Get(entry.name))
			if err != nil {
				return f, err
			}
			if f.cursor != nil && !value.Equal(*entry.target) {
				return f, bad("Cursor time bounds do not match")
			}
			*entry.target = value
		}
	}
	if paging && q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 500 {
			return f, bad("Limit must be between 1 and 500")
		}
		f.limit = n
	}
	if f.cursor != nil && !f.to.After(now.Add(-config.Retention)) {
		return f, &queryError{"cursor_expired", "The cursor time range has expired; refresh the query"}
	}
	if f.cursor == nil && !q.Has("from") && !f.to.After(now.Add(-config.Retention)) {
		f.window = store.QueryRange{RetentionStart: now.Add(-config.Retention), From: now.Add(-config.Retention), To: f.to, Truncated: true}
		return f, nil
	}
	window, err := store.RetainedRange(now, f.from, f.to)
	if err != nil {
		return f, bad("from must be earlier than to")
	}
	f.window = window
	return f, nil
}

func meta(v *store.ReadView, window store.QueryRange, now time.Time) map[string]any {
	stale := v.State.LastSuccess.IsZero() || v.State.LastSuccess.After(now) || now.Sub(v.State.LastSuccess) > config.StaleAfter
	return map[string]any{"last_successful_sync_at": formatTime(v.State.LastSuccess), "data_stale": stale,
		"retention_start": window.RetentionStart.Format(time.RFC3339Nano), "effective_from": window.From.Format(time.RFC3339Nano), "effective_to": window.To.Format(time.RFC3339Nano), "range_truncated": window.Truncated,
		"coverage": map[string]any{"status": v.Coverage.Status, "start": formatTime(v.Coverage.Start), "gap_count": v.Coverage.GapCount, "gaps": v.Coverage.Gaps, "gaps_truncated": v.Coverage.GapsTruncated}}
}
func queryFailure(w http.ResponseWriter, err error, id string) {
	var parameter *queryError
	switch {
	case errors.As(err, &parameter):
		failure(w, 400, parameter.code, parameter.message, id)
	case errors.Is(err, store.ErrDataUnavailable):
		failure(w, 503, "data_unavailable", "No successful account snapshot is available yet", id)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		failure(w, 504, "query_timeout", "Query cancelled or exceeded its time budget", id)
	case errors.Is(err, store.ErrDeviceIdentityUnavailable):
		failure(w, 409, "device_identity_unavailable", "Device identity is not recognized yet; local earnings cannot be scoped", id)
	case errors.Is(err, store.ErrGroupLimit):
		failure(w, 422, "group_limit_exceeded", "Too many groups; use a narrower range or model filter", id)
	case errors.Is(err, store.ErrAmountOverflow):
		failure(w, 503, "amount_out_of_range", "The aggregate amount exceeds the supported integer range", id)
	default:
		failure(w, 503, "database_unavailable", "Local database is unavailable", id)
	}
}
func (h *Handler) overview(ctx context.Context, w http.ResponseWriter, r *http.Request, id string, now time.Time) {
	if err := validateParams(r.URL.Query()); err != nil {
		queryFailure(w, err, id)
		return
	}
	window, _ := store.RetainedRange(now, time.Time{}, time.Time{})
	var body map[string]any
	err := h.store.Query(ctx, h.accountID, now, func(v *store.ReadView) error {
		official, local, err := v.Overview(ctx, window)
		if err != nil {
			return err
		}
		body = map[string]any{"account_id": v.State.AccountID, "official": official, "local_last_365_days": local, "meta": meta(v, window, now)}
		return nil
	})
	if err != nil {
		queryFailure(w, err, id)
		return
	}
	writeJSON(w, 200, body)
}
func (h *Handler) earnings(ctx context.Context, w http.ResponseWriter, r *http.Request, id string, now time.Time) {
	f, err := h.parseFilters(r.URL.Query(), now, true)
	if err != nil {
		queryFailure(w, err, id)
		return
	}
	var body map[string]any
	err = h.store.Query(ctx, h.accountID, now, func(v *store.ReadView) error {
		var after *store.SortKey
		if f.cursor != nil {
			if f.cursor.Account != v.State.AccountID {
				return bad("Cursor belongs to another account")
			}
			after = &f.cursor.After
		}
		page, err := v.Page(ctx, f.window, f.model, after, f.limit)
		if err != nil {
			return err
		}
		var next *string
		if page.Next != nil {
			value, err := h.encodeCursor(cursor{Version: 1, Account: v.State.AccountID, From: f.from.UnixMicro(), To: f.to.UnixMicro(), Model: f.model, After: *page.Next})
			if err != nil {
				return err
			}
			next = &value
		}
		body = map[string]any{"items": page.Items, "next_cursor": next, "meta": meta(v, f.window, now)}
		return nil
	})
	if err != nil {
		queryFailure(w, err, id)
		return
	}
	writeJSON(w, 200, body)
}
func (h *Handler) stats(ctx context.Context, w http.ResponseWriter, r *http.Request, id string, now time.Time) {
	q := r.URL.Query()
	f, err := h.parseFilters(q, now, false)
	if err != nil {
		queryFailure(w, err, id)
		return
	}
	group := q.Get("group_by")
	if group == "" {
		group = "day"
	}
	if group != "hour" && group != "day" && group != "month" && group != "model" {
		queryFailure(w, bad("Unsupported grouping"), id)
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
		items, err := v.Stats(ctx, f.window, f.model, group, location)
		if err != nil {
			return err
		}
		body = map[string]any{"group_by": group, "timezone": zone, "items": items, "meta": meta(v, f.window, now)}
		return nil
	})
	if err != nil {
		queryFailure(w, err, id)
		return
	}
	writeJSON(w, 200, body)
}
