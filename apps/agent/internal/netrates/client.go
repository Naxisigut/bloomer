package netrates

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 8 << 20

var (
	ErrStatsUnavailable   = errors.New("stats_unavailable")
	ErrPricingUnavailable = errors.New("pricing_unavailable")
)

type Client struct {
	base *url.URL
	http *http.Client
}

func NewClient(baseURL string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid stats base URL")
	}
	if hc == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		hc = &http.Client{Timeout: 60 * time.Second, Transport: transport}
	}
	return &Client{base: u, http: hc}, nil
}

func (c *Client) Stats(ctx context.Context, cohort Cohort) (Snapshot, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/stats"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Snapshot{}, ErrStatsUnavailable
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Snapshot{}, ErrStatsUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Snapshot{}, ErrStatsUnavailable
	}
	snap, err := decodeStats(resp.Body, cohort)
	if err != nil {
		return Snapshot{}, ErrStatsUnavailable
	}
	return snap, nil
}

func (c *Client) Pricing(ctx context.Context) (map[string]int64, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/pricing"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrPricingUnavailable
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, ErrPricingUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrPricingUnavailable
	}
	body := io.LimitReader(resp.Body, maxResponseBytes+1)
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, ErrPricingUnavailable
	}
	if len(data) > maxResponseBytes {
		return nil, ErrPricingUnavailable
	}
	var envelope struct {
		Prices []struct {
			Model       string `json:"model"`
			OutputPrice int64  `json:"output_price"`
		} `json:"prices"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return nil, ErrPricingUnavailable
	}
	out := make(map[string]int64, len(envelope.Prices))
	for _, row := range envelope.Prices {
		if row.Model == "" || row.OutputPrice < 0 {
			continue
		}
		out[row.Model] = row.OutputPrice
	}
	return out, nil
}

func decodeStats(r io.Reader, cohort Cohort) (Snapshot, error) {
	lr := &io.LimitedReader{R: r, N: maxResponseBytes + 1}
	dec := json.NewDecoder(lr)
	tok, err := dec.Token()
	if err != nil {
		return Snapshot{}, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return Snapshot{}, errors.New("expected object")
	}
	families := map[string]bool{}
	for _, f := range cohort.ChipFamilies {
		families[f] = true
	}
	var snap Snapshot
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return Snapshot{}, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return Snapshot{}, errors.New("expected field name")
		}
		switch key {
		case "snapshot_at":
			at, err := decodeSnapshotAt(dec)
			if err != nil {
				return Snapshot{}, err
			}
			snap.At = at
		case "providers":
			providers, err := decodeProviders(dec, families, cohort.MemoryGB)
			if err != nil {
				return Snapshot{}, err
			}
			snap.Providers = providers
		default:
			if err = skipValue(dec); err != nil {
				return Snapshot{}, err
			}
		}
	}
	if _, err = dec.Token(); err != nil {
		return Snapshot{}, err
	}
	if snap.At.IsZero() {
		return Snapshot{}, errors.New("missing snapshot_at")
	}
	if lr.N <= 0 {
		return Snapshot{}, errors.New("response too large")
	}
	return snap, nil
}

func decodeSnapshotAt(dec *json.Decoder) (time.Time, error) {
	tok, err := dec.Token()
	if err != nil {
		return time.Time{}, err
	}
	switch v := tok.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return time.Time{}, errors.New("invalid snapshot_at")
		}
		if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return at.UTC(), nil
		}
		if at, err := time.Parse(time.RFC3339, s); err == nil {
			return at.UTC(), nil
		}
		return time.Time{}, errors.New("invalid snapshot_at")
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return time.Time{}, errors.New("invalid snapshot_at")
		}
		if n > 1_000_000_000_000 {
			return time.UnixMicro(n).UTC(), nil
		}
		return time.Unix(n, 0).UTC(), nil
	case float64:
		if v <= 0 {
			return time.Time{}, errors.New("invalid snapshot_at")
		}
		if v > 1_000_000_000_000 {
			return time.UnixMicro(int64(v)).UTC(), nil
		}
		return time.Unix(int64(v), 0).UTC(), nil
	default:
		return time.Time{}, errors.New("invalid snapshot_at")
	}
}

func decodeProviders(dec *json.Decoder, families map[string]bool, memoryGB int) ([]ProviderSnap, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return nil, errors.New("expected providers array")
	}
	var out []ProviderSnap
	for dec.More() {
		var row struct {
			ID             string `json:"id"`
			ChipFamily     string `json:"chip_family"`
			MemoryGB       int    `json:"memory_gb"`
			CurrentModel   string `json:"current_model"`
			RequestsServed int64  `json:"requests_served"`
			TokensGenerated int64 `json:"tokens_generated"`
		}
		if err = dec.Decode(&row); err != nil {
			return nil, err
		}
		if row.ID == "" || row.CurrentModel == "" {
			continue
		}
		if !families[row.ChipFamily] || row.MemoryGB != memoryGB {
			continue
		}
		out = append(out, ProviderSnap{
			ID: row.ID, Model: row.CurrentModel,
			Requests: row.RequestsServed, Tokens: row.TokensGenerated,
		})
	}
	if tok, err = dec.Token(); err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != ']' {
		return nil, errors.New("expected end of providers array")
	}
	return out, nil
}

func skipValue(dec *json.Decoder) error {
	var raw json.RawMessage
	return dec.Decode(&raw)
}
