package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"bloomer/agent/internal/netrates"
)

func TestNetRateBaselineRoundTripAndRetention(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	at := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	snap := netrates.Snapshot{
		At: at,
		Providers: []netrates.ProviderSnap{
			{ID: "p1", Model: "m1", Requests: 1, Tokens: 2},
			{ID: "p2", Model: "m2", Requests: 3, Tokens: 4},
		},
	}
	intervals := []netrates.Interval{{
		StartedAt: at, EndedAt: at.Add(5 * time.Minute), Model: "m1", Providers: 2, DeltaRequests: 10, DeltaTokens: 100,
	}}
	old := at.Add(-8 * 24 * time.Hour)
	if err := s.SaveNetRateRound(ctx, snap, append(intervals, netrates.Interval{
		StartedAt: old, EndedAt: old.Add(5 * time.Minute), Model: "old", Providers: 1, DeltaRequests: 1, DeltaTokens: 1,
	}), at.Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := s.LoadNetRateBaseline(ctx)
	if err != nil || !ok || !loaded.At.Equal(at) || len(loaded.Providers) != 2 {
		t.Fatal(ok, loaded, err)
	}
	rows, err := s.NetRateIntervals(ctx, time.Time{})
	if err != nil || len(rows) != 1 || rows[0].Model != "m1" {
		t.Fatal(rows, err)
	}
}

func TestNetRateIntervalDetailAndLegacyRows(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	at := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	if _, err := s.writer.ExecContext(ctx, `INSERT INTO network_rate_intervals(started_at_us,ended_at_us,model,providers,delta_requests,delta_tokens) VALUES(?,?,?,?,?,?)`,
		at.UnixMicro(), at.Add(5*time.Minute).UnixMicro(), "legacy", 2, 1, 10); err != nil {
		t.Fatal(err)
	}
	detail := netrates.Interval{
		StartedAt: at, EndedAt: at.Add(5 * time.Minute), Model: "m1", Scope: netrates.ScopeTier,
		Providers: 3, BusyProviders: 2, DeltaRequests: 4, DeltaTokens: 900, MaxProviderTokens: 600,
	}
	if err := s.SaveNetRateRound(ctx, netrates.Snapshot{At: at.Add(5 * time.Minute)}, []netrates.Interval{detail}, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.NetRateIntervals(ctx, time.Time{})
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	if rows[0].Model != "legacy" || rows[0].Scope != "" || rows[0].BusyProviders != -1 || rows[0].MaxProviderTokens != -1 {
		t.Fatalf("legacy: %+v", rows[0])
	}
	if rows[1] != detail {
		t.Fatalf("detail: %+v", rows[1])
	}
}

func TestModelTokenTotalsSkipsAdjustmentsAndOtherAccounts(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	at := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		account, id, model string
		prompt, completion int64
		at                 time.Time
	}{
		{"acct", "1", "m", 800, 100, at},
		{"acct", "2", "m", 100, 50, at},
		{"acct", "3", "m", 0, 0, at},
		{"acct", "4", "m", 9999, 1, at.Add(-8 * 24 * time.Hour)},
		{"other", "5", "m", 9999, 1, at},
	} {
		if _, err := s.writer.ExecContext(ctx, `INSERT INTO earnings VALUES(?,?,'p','pk','job',?,1,?,?,?)`, row.account, row.id, row.model, row.prompt, row.completion, row.at.UnixMicro()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ModelTokenTotals(ctx, "acct", at.Add(-7*24*time.Hour))
	if err != nil || len(got) != 1 || got["m"] != (netrates.TokenTotals{Records: 2, Prompt: 900, Completion: 150}) {
		t.Fatal(got, err)
	}
}

func TestNetRateSwitchRetentionAndSummaryLimit(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	var summary []netrates.SummaryItem
	for i := 0; i < 200; i++ {
		summary = append(summary, netrates.SummaryItem{Model: strings.Repeat("m", 20), USDPerHourMicro: int64(i), AvgProviders: 1})
	}
	if _, err := s.InsertNetRateSwitch(ctx, netrates.SwitchRecord{
		At: now, From: "a", To: "b", Reason: "test", Status: "running", JobID: "job", Summary: summary,
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 205; i++ {
		if _, err := s.InsertNetRateSwitch(ctx, netrates.SwitchRecord{
			At: now.Add(time.Duration(i) * time.Second), From: "a", To: "b", Reason: "test", Status: "running", Summary: []netrates.SummaryItem{{Model: "x", USDPerHourMicro: 1}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.reader.QueryRowContext(ctx, "SELECT count(*) FROM network_rate_switches").Scan(&count); err != nil || count != 200 {
		t.Fatal(count, err)
	}
	switches, err := s.NetRateSwitches(ctx, 3)
	if err != nil || len(switches) != 3 || !switches[0].At.After(switches[1].At) {
		t.Fatal(switches, err)
	}
	if err = s.UpdateNetRateSwitchJob(ctx, switches[0].ID, "job-2"); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateNetRateSwitch(ctx, switches[0].ID, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	updated, err := s.NetRateSwitches(ctx, 1)
	if err != nil || updated[0].Status != "succeeded" {
		t.Fatal(updated, err)
	}
	body, err := marshalSwitchSummary(summary)
	if err != nil || len(body) > maxSwitchSummaryBytes {
		t.Fatal(len(body), err)
	}
}
