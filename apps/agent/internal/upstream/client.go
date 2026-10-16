package upstream

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bloomer/agent/internal/config"
)

var ErrAccountMismatch = errors.New("upstream account mismatch")

// Error contains safe codes only; HTTP bodies, credentials and URLs are excluded.
type Error struct {
	Code    string
	RetryAt time.Time
}

func (e *Error) Error() string { return e.Code }

type Client struct {
	endpoint       *url.URL
	credentialFile string
	http           *http.Client
	now            func() time.Time
}

// NewClient accepts a transport for local TLS simulations; nil uses standard HTTPS.
// Timeout and redirect policy remain enforced for either transport.
func NewClient(endpoint, credentialFile string, transport http.RoundTripper) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/v1/provider/account-earnings" {
		return nil, errors.New("invalid upstream endpoint")
	}
	return &Client{endpoint: u, credentialFile: credentialFile, now: time.Now, http: &http.Client{
		Timeout:       config.RequestTimeout,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Fetch reads the credential on every request so rotation recovers without restart.
func (c *Client) Fetch(ctx context.Context, limit int, account string) (Snapshot, error) {
	if limit < 1 || limit > 1000 {
		return Snapshot{}, &Error{Code: "invalid_limit"}
	}
	token, err := config.ReadToken(c.credentialFile)
	if err != nil {
		return Snapshot{}, &Error{Code: "credential_unavailable"}
	}
	u := *c.endpoint
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	u.RawQuery = q.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Snapshot{}, &Error{Code: "upstream_request_failed"}
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		code := "network_failed"
		if ctx.Err() != nil {
			code = "request_cancelled"
		} else {
			var timeout interface{ Timeout() bool }
			if errors.As(err, &timeout) && timeout.Timeout() {
				code = "request_timeout"
			}
		}
		return Snapshot{}, &Error{Code: code}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		class := ClassifyStatus(response.StatusCode)
		if class.Code == "ok" {
			class.Code = "invalid_response"
		}
		failure := &Error{Code: class.Code}
		if response.StatusCode == http.StatusTooManyRequests {
			failure.RetryAt = retryAfter(response.Header.Get("Retry-After"), c.now().UTC())
		}
		return Snapshot{}, failure
	}
	snapshot, err := Decode(response.Body, limit, account)
	if err != nil {
		if ctx.Err() != nil {
			return Snapshot{}, &Error{Code: "request_cancelled"}
		}
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return Snapshot{}, &Error{Code: "request_timeout"}
		}
		if errors.Is(err, ErrAccountMismatch) {
			return Snapshot{}, &Error{Code: "account_mismatch"}
		}
		return Snapshot{}, &Error{Code: "invalid_response"}
	}
	return snapshot, nil
}

func retryAfter(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value != "" {
		digits := true
		for _, r := range value {
			if r < '0' || r > '9' {
				digits = false
				break
			}
		}
		if digits {
			seconds, err := strconv.ParseUint(value, 10, 64)
			// Saturate at the latest supported UTC date instead of wrapping into the past.
			max := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
			if err != nil || seconds > uint64(max.Unix()-now.Unix()) {
				return max
			}
			return time.Unix(now.Unix()+int64(seconds), int64(now.Nanosecond())).UTC()
		}
		if at, err := http.ParseTime(value); err == nil {
			if at.Before(now) {
				return now
			}
			return at.UTC()
		}
	}
	return now.Add(config.CollectionInterval)
}
