package upstream

import (
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../testdata/upstream/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func TestDecodeVerifiedShape(t *testing.T) {
	observed, err := Decode(strings.NewReader(fixture(t, "observed-redacted.json")), 50, "acct_example")
	if err != nil || len(observed.Earnings) != 2 {
		t.Fatalf("observed redacted shape: %v", err)
	}
	s, err := Decode(strings.NewReader(fixture(t, "normal.json")), 50, "acct_example")
	if err != nil {
		t.Fatal(err)
	}
	if s.Earnings[0].RecordID != "9007199254740993" || s.Earnings[0].AmountMicroUSD != 1234 {
		t.Fatal("integer precision lost")
	}
	if s.Earnings[1].AmountMicroUSD != -34 || s.Earnings[1].PromptTokens != 0 {
		t.Fatal("legitimate adjustment or zero rejected")
	}
	if got := s.Earnings[0].CreatedAt.UnixMicro(); got != time.Date(2026, 10, 16, 1, 2, 3, 123456000, time.UTC).UnixMicro() {
		t.Fatalf("timestamp precision lost: %d", got)
	}
	if s.Earnings[1].CreatedAt.Location() != time.UTC {
		t.Fatal("timestamp not normalized to UTC")
	}
	for _, fraction := range []string{".123Z", ".1234Z", ".12345Z", ".123456Z"} {
		body := strings.Replace(fixture(t, "normal.json"), ".123456Z", fraction, 1)
		if _, err := Decode(strings.NewReader(body), 50, "acct_example"); err != nil {
			t.Fatalf("observed fractional precision %s: %v", fraction, err)
		}
	}
	s, err = Decode(strings.NewReader(fixture(t, "duplicate-identical.json")), 50, "acct_example")
	if err != nil || len(s.Earnings) != 1 {
		t.Fatalf("identical duplicates: %v", err)
	}
	body := strings.Replace(fixture(t, "duplicate-identical.json"), `"count": 2`, `"count": 1`, 1)
	if duplicate, err := Decode(strings.NewReader(body), 50, "acct_example"); err != nil || len(duplicate.Earnings) != 1 {
		t.Fatalf("duplicate exceeded unique lifetime count: %v", err)
	}
}
func TestDecodeRejectsInvalidSnapshot(t *testing.T) {
	for _, name := range []string{"duplicate-conflict.json", "missing-amount.json", "overflow.json", "malformed.json", "unauthorized.json"} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(fixture(t, name)), 50, "acct_example"); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
	base := fixture(t, "normal.json")
	cases := []string{
		strings.Replace(base, "acct_example", "another_account", 1),
		strings.Replace(base, `"amount_micro_usd": 1234`, `"amount_micro_usd": null`, 1),
		strings.Replace(base, `"amount_micro_usd": 1234`, `"amount_micro_usd": 1.234`, 1),
		strings.Replace(base, `"amount_micro_usd": 1234`, `"amount_micro_usd": 1234, "amount_micro_usd": 4567`, 1),
		strings.Replace(base, ".123456Z", ".123456789Z", 1),
		strings.Replace(base, "2026-10-16T01:02:03.123456Z", "2026-10-16 01:02:03", 1),
		base + ` {}`,
		`null`,
		strings.Repeat(" ", 2<<20) + base,
	}
	for i, body := range cases {
		if _, err := Decode(strings.NewReader(body), 50, "acct_example"); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	if _, err := Decode(strings.NewReader(base), 1, "acct_example"); err == nil {
		t.Fatal("oversized window accepted")
	}
}
func TestStatusClassification(t *testing.T) {
	for _, c := range []struct {
		status int
		code   string
		retry  bool
	}{{200, "ok", false}, {401, "authentication_failed", false}, {403, "authentication_failed", false}, {429, "rate_limited", true}, {503, "upstream_unavailable", true}, {302, "redirect_rejected", false}, {400, "upstream_request_rejected", false}} {
		if got := ClassifyStatus(c.status); got.Code != c.code || got.Retryable != c.retry {
			t.Fatalf("%d: %+v", c.status, got)
		}
	}
}
