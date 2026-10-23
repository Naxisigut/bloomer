package netrates

import (
	"testing"
	"time"
)

func TestOutputTokenTrendPerModelPerMachineHour(t *testing.T) {
	base := time.Date(2026, 10, 23, 1, 0, 0, 0, time.UTC)
	end := base.Add(5 * time.Minute)
	later := end.Add(5 * time.Minute)
	intervals := []Interval{
		{StartedAt: base, EndedAt: end, Model: "a", Providers: 2, DeltaTokens: 6000},
		{StartedAt: base, EndedAt: end, Model: "b", Providers: 1, DeltaTokens: 3000},
		{StartedAt: base, EndedAt: end, Model: "c", Providers: 1, DeltaTokens: 9000},
		{StartedAt: end, EndedAt: later, Model: "a", Providers: 2, DeltaTokens: 3000},
		{StartedAt: end, EndedAt: later, Model: "skip-providers", Providers: 0, DeltaTokens: 1000},
		{StartedAt: later, EndedAt: later, Model: "skip-hours", Providers: 1, DeltaTokens: 1000},
		{StartedAt: later, EndedAt: later.Add(5 * time.Minute), Model: "skip-neg", Providers: 1, DeltaTokens: -1},
	}
	all := OutputTokenTrend(intervals, nil)
	if len(all) != 3 || all[0].Model != "a" || all[1].Model != "b" || all[2].Model != "c" {
		t.Fatalf("all models: %+v", all)
	}
	if len(all[0].Points) != 2 || !all[0].Points[0].At.Equal(end) || all[0].Points[0].Tokens != 36000 || all[0].Points[1].Tokens != 18000 {
		t.Fatalf("a: %+v", all[0])
	}
	if len(all[1].Points) != 1 || all[1].Points[0].Tokens != 36000 || all[2].Points[0].Tokens != 108000 {
		t.Fatalf("b/c: %+v %+v", all[1], all[2])
	}
	selected := OutputTokenTrend(intervals, []string{"b", "a", "missing"})
	if len(selected) != 2 || selected[0].Model != "b" || selected[1].Model != "a" || selected[0].Points[0].Tokens != 36000 || selected[1].Points[0].Tokens != 36000 {
		t.Fatalf("selected models: %+v", selected)
	}
	empty := OutputTokenTrend(nil, []string{"a"})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty: %#v", empty)
	}
}

func TestDiffIntervalValidityAndAggregation(t *testing.T) {
	base := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	interval := 5 * time.Minute
	prev := Snapshot{At: base, Providers: []ProviderSnap{
		{ID: "p1", Model: "m1", Requests: 10, Tokens: 100},
		{ID: "p2", Model: "m1", Requests: 5, Tokens: 50},
		{ID: "p3", Model: "m2", Requests: 1, Tokens: 10},
	}}
	next := Snapshot{At: base.Add(5 * time.Minute), Providers: []ProviderSnap{
		{ID: "p1", Model: "m1", Requests: 12, Tokens: 130},
		{ID: "p2", Model: "m2", Requests: 8, Tokens: 60},
		{ID: "p3", Model: "m2", Requests: 4, Tokens: 20},
	}}
	ivs, ok := Diff(prev, next, interval)
	if !ok || len(ivs) != 2 || ivs[0].Model != "m1" || ivs[1].Model != "m2" {
		t.Fatalf("diff: ok=%v %+v", ok, ivs)
	}
	if ivs[0].Providers != 1 || ivs[0].DeltaRequests != 2 || ivs[0].DeltaTokens != 30 {
		t.Fatalf("m1 interval: %+v", ivs[0])
	}
	if ivs[1].Providers != 1 || ivs[1].DeltaRequests != 3 || ivs[1].DeltaTokens != 10 {
		t.Fatalf("m2 interval: %+v", ivs[1])
	}
	_, ok = Diff(next, prev, interval)
	if ok {
		t.Fatal("expected invalid when next not after prev")
	}
	_, ok = Diff(prev, Snapshot{At: base.Add(20 * time.Minute), Providers: next.Providers}, interval)
	if ok {
		t.Fatal("expected invalid interval gap")
	}
}

func TestDiffZeroDeltasAndNegativeSkip(t *testing.T) {
	base := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	prev := Snapshot{At: base, Providers: []ProviderSnap{{ID: "p1", Model: "m1", Requests: 10, Tokens: 100}}}
	next := Snapshot{At: base.Add(5 * time.Minute), Providers: []ProviderSnap{
		{ID: "p1", Model: "m1", Requests: 10, Tokens: 100},
		{ID: "p2", Model: "m1", Requests: 5, Tokens: 50},
	}}
	ivs, ok := Diff(prev, next, 5*time.Minute)
	if !ok || len(ivs) != 1 || ivs[0].Providers != 1 || ivs[0].DeltaRequests != 0 || ivs[0].DeltaTokens != 0 {
		t.Fatal(ivs, ok)
	}
	negPrev := Snapshot{At: base, Providers: []ProviderSnap{{ID: "p1", Model: "m1", Requests: 20, Tokens: 100}}}
	negNext := Snapshot{At: base.Add(5 * time.Minute), Providers: []ProviderSnap{{ID: "p1", Model: "m1", Requests: 10, Tokens: 90}}}
	ivs, ok = Diff(negPrev, negNext, 5*time.Minute)
	if !ok || len(ivs) != 0 {
		t.Fatal("negative delta provider should be skipped entirely", ivs)
	}
}

func TestEstimateWindowFallbackAndMinProviders(t *testing.T) {
	start := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	var intervals []Interval
	for i := 0; i < 6; i++ {
		from := start.Add(time.Duration(i) * 5 * time.Minute)
		to := from.Add(5 * time.Minute)
		intervals = append(intervals, Interval{StartedAt: from, EndedAt: to, Model: "hot", Providers: 4, DeltaRequests: 40, DeltaTokens: 400000})
		if i%2 == 0 {
			intervals = append(intervals, Interval{StartedAt: from, EndedAt: to, Model: "thin", Providers: 1, DeltaRequests: 1, DeltaTokens: 1000})
		}
	}
	prices := map[string]int64{"hot": 2200000}
	downloaded := map[string]bool{"hot": true, "thin": true}
	cands, samples := Estimate(intervals, 6, prices, "gemma-4-26b-qat-4bit", downloaded, 3)
	if samples != 6 || len(cands) != 2 {
		t.Fatal(samples, cands)
	}
	if cands[0].Model != "hot" || cands[0].PriceSource != "official" || !cands[1].Insufficient {
		t.Fatal(cands)
	}
	cands, _ = Estimate(intervals, 6, map[string]int64{}, "gemma-4-26b-qat-4bit", downloaded, 3)
	if cands[0].PriceSource != "fallback" || cands[0].USDPerHourMicro <= 0 {
		t.Fatal(cands[0])
	}
}

func TestRecommend(t *testing.T) {
	cands := []Candidate{
		{Model: "a", Downloaded: true, USDPerHourMicro: 5000, Insufficient: false},
		{Model: "b", Downloaded: true, USDPerHourMicro: 1000, Insufficient: true},
		{Model: "gemma-4-26b-qat-4bit", Downloaded: true, USDPerHourMicro: 800, Insufficient: false},
	}
	if Recommend(cands, 5, 6, 10000, "gemma-4-26b-qat-4bit") != nil {
		t.Fatal("expected nil when samples below window")
	}
	rec := Recommend(cands, 6, 6, 10000, "gemma-4-26b-qat-4bit")
	if rec == nil || rec.Model != "gemma-4-26b-qat-4bit" || !rec.Fallback || rec.USDPerHourMicro != 800 {
		t.Fatal(rec)
	}
	cands[0].USDPerHourMicro = 500000
	rec = Recommend(cands, 6, 6, 10000, "gemma-4-26b-qat-4bit")
	if rec == nil || rec.Model != "a" || rec.Fallback || rec.USDPerHourMicro != 500000 {
		t.Fatal(rec)
	}
	cands = []Candidate{{Model: "x", Downloaded: false, USDPerHourMicro: 999999, Insufficient: false}}
	rec = Recommend(cands, 6, 6, 10000, "gemma-4-26b-qat-4bit")
	if rec == nil || !rec.Fallback {
		t.Fatal(rec)
	}
}
