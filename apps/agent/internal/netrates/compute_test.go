package netrates

import (
	"math"
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
	ivs, ok := Diff(prev, next, interval, nil)
	if !ok || len(ivs) != 2 || ivs[0].Model != "m1" || ivs[1].Model != "m2" {
		t.Fatalf("diff: ok=%v %+v", ok, ivs)
	}
	if ivs[0].Providers != 1 || ivs[0].DeltaRequests != 2 || ivs[0].DeltaTokens != 30 {
		t.Fatalf("m1 interval: %+v", ivs[0])
	}
	if ivs[1].Providers != 1 || ivs[1].DeltaRequests != 3 || ivs[1].DeltaTokens != 10 {
		t.Fatalf("m2 interval: %+v", ivs[1])
	}
	_, ok = Diff(next, prev, interval, nil)
	if ok {
		t.Fatal("expected invalid when next not after prev")
	}
	_, ok = Diff(prev, Snapshot{At: base.Add(20 * time.Minute), Providers: next.Providers}, interval, nil)
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
	ivs, ok := Diff(prev, next, 5*time.Minute, nil)
	if !ok || len(ivs) != 1 || ivs[0].Providers != 1 || ivs[0].DeltaRequests != 0 || ivs[0].DeltaTokens != 0 {
		t.Fatal(ivs, ok)
	}
	negPrev := Snapshot{At: base, Providers: []ProviderSnap{{ID: "p1", Model: "m1", Requests: 20, Tokens: 100}}}
	negNext := Snapshot{At: base.Add(5 * time.Minute), Providers: []ProviderSnap{{ID: "p1", Model: "m1", Requests: 10, Tokens: 90}}}
	ivs, ok = Diff(negPrev, negNext, 5*time.Minute, nil)
	if !ok || len(ivs) != 0 {
		t.Fatal("negative delta provider should be skipped entirely", ivs)
	}
}

func TestDiffTierScopeBusyAndMax(t *testing.T) {
	base := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	prev := Snapshot{At: base, Providers: []ProviderSnap{
		{ID: "p1", Model: "m", Tier: "Pro", Tokens: 100},
		{ID: "p2", Model: "m", Tier: "Pro", Tokens: 100},
		{ID: "p3", Model: "m", Tier: "Max", Tokens: 100},
	}}
	next := Snapshot{At: base.Add(5 * time.Minute), Providers: []ProviderSnap{
		{ID: "p1", Model: "m", Tier: "Pro", Tokens: 400},
		{ID: "p2", Model: "m", Tier: "Pro", Tokens: 100},
		{ID: "p3", Model: "m", Tier: "Max", Tokens: 1100},
	}}
	ivs, ok := Diff(prev, next, 5*time.Minute, []string{"Pro"})
	if !ok || len(ivs) != 2 || ivs[0].Scope != "" || ivs[1].Scope != ScopeTier {
		t.Fatalf("scopes: %+v", ivs)
	}
	all, tier := ivs[0], ivs[1]
	if all.Providers != 3 || all.BusyProviders != 2 || all.DeltaTokens != 1300 || all.MaxProviderTokens != 1000 {
		t.Fatalf("all: %+v", all)
	}
	if tier.Providers != 2 || tier.BusyProviders != 1 || tier.DeltaTokens != 300 || tier.MaxProviderTokens != 300 {
		t.Fatalf("tier: %+v", tier)
	}
}

func TestEstimateRevenueTierFallbackAndWindows(t *testing.T) {
	now := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	var intervals []Interval
	// 12 intervals of 5 minutes ending at now: the hour window holds all, the short window the last 6.
	for i := 0; i < 12; i++ {
		to := now.Add(-time.Duration(11-i) * 5 * time.Minute)
		from := to.Add(-5 * time.Minute)
		hotTokens := int64(12000)
		if i < 6 {
			hotTokens = 0
		}
		intervals = append(intervals,
			Interval{StartedAt: from, EndedAt: to, Model: "hot", Providers: 4, BusyProviders: 1, DeltaTokens: hotTokens, MaxProviderTokens: hotTokens},
			Interval{StartedAt: from, EndedAt: to, Model: "hot", Scope: ScopeTier, Providers: 3, BusyProviders: 1, DeltaTokens: hotTokens, MaxProviderTokens: hotTokens},
			Interval{StartedAt: from, EndedAt: to, Model: "few", Providers: 4, BusyProviders: 2, DeltaTokens: 4000, MaxProviderTokens: 3000},
			Interval{StartedAt: from, EndedAt: to, Model: "few", Scope: ScopeTier, Providers: 1, BusyProviders: 1, DeltaTokens: 1000, MaxProviderTokens: 1000},
		)
	}
	in := EstimateInput{
		Intervals: intervals, Now: now, Window: 6, LongWindow: time.Hour,
		Prices:        map[string]Price{"hot": {Input: 100000, Output: 1000000}, "few": {Input: 50000, Output: 200000}},
		FallbackModel: "gemma-4-26b-qat-4bit", Downloaded: map[string]bool{"hot": true, "few": true},
		MinProviders: 3, TierScope: true,
		OwnRatios: map[string]float64{"hot": 2}, NetworkRatio: 8,
	}
	cands, samples := Estimate(in)
	if samples != 6 || len(cands) != 2 {
		t.Fatal(samples, cands)
	}
	byModel := map[string]Candidate{}
	for _, c := range cands {
		byModel[c.Model] = c
	}
	hot, few := byModel["hot"], byModel["few"]
	// hot: same-tier cohort of 3; 12000 tokens per 5 minutes over 3 machines = 48000 tokens/h each.
	if hot.Cohort != CohortTier || hot.AvgProviders != 3 || hot.TokensPerHour != 48000 {
		t.Fatalf("hot cohort: %+v", hot)
	}
	if hot.TierAvgProviders == nil || *hot.TierAvgProviders != 3 || hot.AllAvgProviders != 4 {
		t.Fatalf("hot providers: %+v", hot)
	}
	// Own ratio 2: 1000000 + 2*100000 per million output tokens.
	if hot.RatioSource != RatioOwn || hot.EffectivePriceMicro != 1200000 || hot.USDPerHourMicro != 57600 {
		t.Fatalf("hot price: %+v", hot)
	}
	if hot.BusyRatio == nil || *hot.BusyRatio != 1.0/3 || hot.TrimmedUSDPerHourMicro == nil || *hot.TrimmedUSDPerHourMicro != 0 {
		t.Fatalf("hot busy/trimmed: %+v", hot)
	}
	if hot.Hour == nil || hot.Hour.Samples != 12 || hot.Hour.TokensPerHour != 24000 || hot.Hour.USDPerHourMicro != 28800 {
		t.Fatalf("hot hour: %+v", hot.Hour)
	}
	// few: only one same-tier machine, so it falls back to the whole cohort and the network ratio.
	if few.Cohort != CohortAll || few.AvgProviders != 4 || math.Abs(few.TokensPerHour-12000) > 1e-6 || few.USDPerHourMicro != 7200 || few.RatioSource != RatioNetwork || few.EffectivePriceMicro != 600000 {
		t.Fatalf("few: %+v", few)
	}
	if few.TierAvgProviders == nil || *few.TierAvgProviders != 1 || few.AllAvgProviders != 4 {
		t.Fatalf("few providers: %+v", few)
	}
	// Without the 3000-token top machine: 1000 tokens over 3 machines per 5 minutes = 4000 tokens/h.
	if few.TrimmedUSDPerHourMicro == nil || *few.TrimmedUSDPerHourMicro != 2400 || few.BusyRatio == nil || *few.BusyRatio != 0.5 {
		t.Fatalf("few trimmed: %+v", few)
	}

	in.TierScope = false
	in.OwnRatios = nil
	in.NetworkRatio = 0
	cands, _ = Estimate(in)
	for _, c := range cands {
		if c.Cohort != CohortAll || c.RatioSource != RatioNone || c.EffectivePriceMicro != in.Prices[c.Model].Output || c.TierAvgProviders != nil {
			t.Fatalf("whole cohort output-only: %+v", c)
		}
	}
}

func TestEstimateLegacyRowsHaveNoDetail(t *testing.T) {
	now := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	var intervals []Interval
	for i := 0; i < 6; i++ {
		to := now.Add(-time.Duration(5-i) * 5 * time.Minute)
		intervals = append(intervals, Interval{StartedAt: to.Add(-5 * time.Minute), EndedAt: to, Model: "m", Providers: 4, BusyProviders: -1, DeltaTokens: 1000, MaxProviderTokens: -1})
	}
	cands, _ := Estimate(EstimateInput{Intervals: intervals, Now: now, Window: 6, LongWindow: time.Hour, MinProviders: 3})
	if len(cands) != 1 || cands[0].BusyRatio != nil || cands[0].TrimmedUSDPerHourMicro != nil || cands[0].Hour == nil || cands[0].Hour.BusyRatio != nil {
		t.Fatalf("legacy: %+v", cands)
	}
}

func TestOwnRatiosNeedEnoughRecords(t *testing.T) {
	got := OwnRatios(map[string]TokenTotals{
		"a": {Records: 30, Prompt: 900, Completion: 100},
		"b": {Records: 29, Prompt: 900, Completion: 100},
		"c": {Records: 50, Prompt: 900, Completion: 0},
	})
	if len(got) != 1 || got["a"] != 9 {
		t.Fatal(got)
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
	in := EstimateInput{
		Intervals: intervals, Now: start.Add(30 * time.Minute), Window: 6,
		Prices: map[string]Price{"hot": {Output: 2200000}}, FallbackModel: "gemma-4-26b-qat-4bit",
		Downloaded: map[string]bool{"hot": true, "thin": true}, MinProviders: 3,
	}
	cands, samples := Estimate(in)
	if samples != 6 || len(cands) != 2 {
		t.Fatal(samples, cands)
	}
	if cands[0].Model != "hot" || cands[0].PriceSource != "official" || !cands[1].Insufficient {
		t.Fatal(cands)
	}
	in.Prices = map[string]Price{}
	cands, _ = Estimate(in)
	if cands[0].PriceSource != "fallback" || cands[0].USDPerHourMicro <= 0 || cands[0].EffectivePriceMicro != defaultFallbackPrice.Output {
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
