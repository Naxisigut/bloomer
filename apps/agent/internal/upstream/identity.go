package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"bloomer/agent/internal/config"
)

// The public attestation feed is used only to join local identity to earnings.
// It requires no account token and is never used to infer routing eligibility.
type IdentityClient struct {
	client  *Client
	mu      sync.Mutex
	retryAt time.Time
}

func NewIdentityClient(earningsURL string, transport http.RoundTripper) (*IdentityClient, error) {
	c, err := NewClient(earningsURL, "", transport)
	if err != nil {
		return nil, err
	}
	c.endpoint.Path = "/v1/providers/attestation"
	return &IdentityClient{client: c}, nil
}

func (c *IdentityClient) ProviderIDs(ctx context.Context, key string, now time.Time) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key == "" {
		return nil, &Error{Code: "device_identity_unavailable"}
	}
	if now.Before(c.retryAt) {
		return nil, &Error{Code: "rate_limited", RetryAt: c.retryAt}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.client.endpoint.String(), nil)
	if err != nil {
		return nil, &Error{Code: "upstream_request_failed"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	response, err := c.client.http.Do(req)
	if err != nil {
		return nil, &Error{Code: "network_failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == 429 {
			c.retryAt = retryAfter(response.Header.Get("Retry-After"), now)
		}
		return nil, &Error{Code: "identity_lookup_failed", RetryAt: c.retryAt}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, config.MaxResponseBytes+1))
	if err != nil || len(body) > config.MaxResponseBytes || uniqueMembers(body) != nil {
		return nil, &Error{Code: "invalid_response"}
	}
	var wire struct {
		Providers *[]struct {
			ID  string `json:"provider_id"`
			Key string `json:"se_public_key"`
		} `json:"providers"`
	}
	if json.Unmarshal(body, &wire) != nil || wire.Providers == nil {
		return nil, &Error{Code: "invalid_response"}
	}
	ids, seen := []string{}, map[string]bool{}
	// One SE identity may have multiple registration sessions. All exact matches
	// belong to this device; never choose by model/chip/account order.
	for _, p := range *wire.Providers {
		if p.Key != key {
			continue
		}
		if p.ID == "" || len(p.ID) > 256 || strings.ContainsAny(p.ID, "\r\n\x00") {
			return nil, &Error{Code: "invalid_response"}
		}
		if !seen[p.ID] {
			ids = append(ids, p.ID)
			seen[p.ID] = true
		}
	}
	if len(ids) == 0 {
		return nil, &Error{Code: "device_identity_unavailable"}
	}
	// Conflicting duplicate IDs must not join a different identity's earnings.
	for _, p := range *wire.Providers {
		if seen[p.ID] && p.Key != key {
			return nil, &Error{Code: "identity_conflict"}
		}
	}
	return ids, nil
}
