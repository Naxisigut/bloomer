package upstream

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPClientContractAndRotation(t *testing.T) {
	body := fixture(t, "normal.json")
	var expected atomic.Value
	expected.Store(strings.Repeat("a", 64))
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/provider/account-earnings" || r.URL.RawQuery != "limit=50" || r.Header.Get("Authorization") != "Bearer "+expected.Load().(string) {
			t.Error("incorrect request contract")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "auth.token")
	os.WriteFile(path, []byte(expected.Load().(string)), 0600)
	client, err := NewClient(server.URL+"/v1/provider/account-earnings", path, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = server.Client().Transport
	for i := 0; i < 2; i++ {
		if i == 1 {
			expected.Store(strings.Repeat("b", 64))
			os.WriteFile(path, []byte(expected.Load().(string)), 0600)
		}
		if _, err = client.Fetch(context.Background(), 50, "acct_example"); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected retries")
	}
	if _, err = client.Fetch(context.Background(), 50, "other-account"); err == nil || err.Error() != "account_mismatch" {
		t.Fatal("account mismatch accepted:", err)
	}
}

func TestHTTPFailuresAreSafeAndBounded(t *testing.T) {
	secret := strings.Repeat("s", 64)
	path := filepath.Join(t.TempDir(), "auth.token")
	os.WriteFile(path, []byte(secret), 0600)
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	var destinationCalls atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1) }))
	defer destination.Close()
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
	}{
		{"authentication", 401, secret, "authentication_failed"}, {"forbidden", 403, secret, "authentication_failed"},
		{"throttle", 429, secret, "rate_limited"}, {"server", 503, secret, "upstream_unavailable"},
		{"redirect", 302, secret, "redirect_rejected"}, {"malformed", 200, "{", "invalid_response"},
		{"oversize", 200, strings.Repeat(" ", 2<<20) + "{}", "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", destination.URL)
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			c, _ := NewClient(server.URL+"/v1/provider/account-earnings", path, nil)
			c.http.Transport = server.Client().Transport
			c.now = func() time.Time { return now }
			_, err := c.Fetch(context.Background(), 50, "")
			var safe *Error
			if !errors.As(err, &safe) || safe.Code != tc.code || strings.Contains(err.Error(), secret) || calls.Load() != 1 {
				t.Fatalf("unsafe/wrong failure: %v", err)
			}
			if tc.status == 429 && !safe.RetryAt.Equal(now.Add(120*time.Second)) {
				t.Fatal("Retry-After ignored")
			}
		})
	}
	if destinationCalls.Load() != 0 {
		t.Fatal("followed authenticated redirect")
	}
	blocked := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer blocked.Close()
	c, _ := NewClient(blocked.URL+"/v1/provider/account-earnings", path, nil)
	c.http.Transport = blocked.Client().Transport
	c.http.Timeout = 20 * time.Millisecond
	if _, err := c.Fetch(context.Background(), 50, ""); err == nil || err.Error() != "request_timeout" {
		t.Fatalf("timeout not bounded: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Fetch(ctx, 50, ""); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	os.Remove(path)
	if _, err := c.Fetch(context.Background(), 50, ""); err == nil || err.Error() != "credential_unavailable" {
		t.Fatal("missing credential was not classified")
	}
}

func TestRetryAfterFormats(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Time
	}{
		{"90", now.Add(90 * time.Second)}, {"0", now}, {now.Add(time.Hour).Format(http.TimeFormat), now.Add(time.Hour)},
		{now.Add(-time.Hour).Format(http.TimeFormat), now}, {"invalid", now.Add(time.Minute)}, {"-1", now.Add(time.Minute)},
		{"99999999999999999999999999", time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)},
	} {
		if got := retryAfter(tc.value, now); !got.Equal(tc.want) {
			t.Fatalf("%s: %s", tc.value, got)
		}
	}
}

func TestTimeoutWhileReadingResponseBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "auth.token")
	os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600)
	c, _ := NewClient(server.URL+"/v1/provider/account-earnings", path, nil)
	c.http.Transport = server.Client().Transport
	c.http.Timeout = 20 * time.Millisecond
	if _, err := c.Fetch(context.Background(), 50, ""); err == nil || err.Error() != "request_timeout" {
		t.Fatalf("body timeout misclassified: %v", err)
	}
}
