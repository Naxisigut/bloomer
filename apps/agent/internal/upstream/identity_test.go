package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPublicIdentityExactMatchNoTokenAndFailures(t *testing.T) {
	body := `{"providers":[{"provider_id":"other","se_public_key":"other-key"},{"provider_id":"own-1","se_public_key":"local-key"},{"provider_id":"own-2","se_public_key":"local-key"}]}`
	status, calls := 200, 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/providers/attestation" || r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" {
			t.Error("wrong public lookup")
		}
		w.Header().Set("Retry-After", "120")
		w.Header().Set("Location", "https://example.invalid")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	defer server.Close()
	c, err := NewIdentityClient(server.URL+"/v1/provider/account-earnings", server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ids, err := c.ProviderIDs(context.Background(), "local-key", now)
	if err != nil || len(ids) != 2 || ids[0] != "own-1" {
		t.Fatalf("exact join: %v %v", ids, err)
	}
	for _, bad := range []string{`{"providers":[]}`, `{"providers":[{"provider_id":"own","se_public_key":"local-key"},{"provider_id":"own","se_public_key":"other-key"}]}`, `{"providers":null}`, `{"providers":[],"providers":[]}`} {
		body = bad
		if _, err = c.ProviderIDs(context.Background(), "local-key", now); err == nil {
			t.Fatal("invalid mapping accepted")
		}
	}
	status = 302
	if _, err = c.ProviderIDs(context.Background(), "local-key", now); err == nil {
		t.Fatal("redirect accepted")
	}
	status = 429
	c.ProviderIDs(context.Background(), "local-key", now)
	before := calls
	c.ProviderIDs(context.Background(), "local-key", now.Add(time.Minute))
	if calls != before {
		t.Fatal("rate limit ignored")
	}
}
