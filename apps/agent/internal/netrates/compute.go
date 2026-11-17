package netrates

import (
	"math"
	"sort"
	"time"
)

var defaultFallbackPrice = Price{Input: 42000, Output: 220000}

const (
	CohortTier = "tier"
	CohortAll  = "all"

	RatioOwn     = "own"
	RatioNetwork = "network"
	RatioNone    = "none"
)

// Diff aggregates per-provider counter deltas by model. Providers whose chip tier is in tiers
// are additionally aggregated under ScopeTier.
func Diff(prev, next Snapshot, interval time.Duration, tiers []string) (intervals []Interval, valid bool) {
	if !next.At.After(prev.At) {
		return nil, false
	}
	elapsed := next.At.Sub(prev.At)
	min := time.Duration(float64(interval) * 0.5)
	max := time.Duration(float64(interval) * 2)
	if elapsed < min || elapsed > max {
		return nil, false
	}
	prevByID := make(map[string]ProviderSnap, len(prev.Providers))
	for _, p := range prev.Providers {
		prevByID[p.ID] = p
	}
	tierSet := make(map[string]bool, len(tiers))
	for _, tier := range tiers {
		tierSet[tier] = true
	}
	type key struct{ model, scope string }
	type agg struct {
		providers, busy            int
		deltaRequests, deltaTokens int64
		maxTokens                  int64
	}
	byKey := map[key]*agg{}
	add := func(k key, dr, dt int64) {
		a := byKey[k]
		if a == nil {
			a = &agg{}
			byKey[k] = a
		}
		a.providers++
		if dt > 0 {
			a.busy++
		}
		if dt > a.maxTokens {
			a.maxTokens = dt
		}
		a.deltaRequests += dr
		a.deltaTokens += dt
	}
	for _, n := range next.Providers {
		p, ok := prevByID[n.ID]
		if !ok || p.Model != n.Model {
			continue
		}
		dr := n.Requests - p.Requests
		dt := n.Tokens - p.Tokens
		if dr < 0 || dt < 0 {
			continue
		}
		add(key{n.Model, ""}, dr, dt)
		if tierSet[n.Tier] {
			add(key{n.Model, ScopeTier}, dr, dt)
		}
	}
	if len(byKey) == 0 {
		return nil, true
	}
	keys := make([]key, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].model != keys[j].model {
			return keys[i].model < keys[j].model
		}
		return keys[i].scope < keys[j].scope
	})
	out := make([]Interval, 0, len(keys))
	for _, k := range keys {
		a := byKey[k]
		out = append(out, Interval{
			StartedAt: prev.At, EndedAt: next.At, Model: k.model, Scope: k.scope,
			Providers: a.providers, BusyProviders: a.busy,
			DeltaRequests: a.deltaRequests, DeltaTokens: a.deltaTokens, MaxProviderTokens: a.maxTokens,
		})
	}
	return out, true
}

type EstimateInput struct {
	Intervals     []Interval
	Now           time.Time
	Window        int
	LongWindow    time.Duration
	Prices        map[string]Price
	FallbackModel string
	Downloaded    map[string]bool
	MinProviders  int
	// TierScope prefers ScopeTier rows for models with at least MinProviders same-tier machines.
	TierScope bool
	// OwnRatios is prompt/completion per model from this account's billed history.
	OwnRatios    map[string]float64
	NetworkRatio float64
}

type windowTotals struct {
	providerHours              float64
	deltaRequests, deltaTokens int64
	providerSum                int64
	detailedProviders, busy    int64
	trimmedTokens              int64
	trimmedHours               float64
}

func (t *windowTotals) add(iv Interval, hours float64) {
	t.providerHours += float64(iv.Providers) * hours
	t.deltaRequests += iv.DeltaRequests
	t.deltaTokens += iv.DeltaTokens
	t.providerSum += int64(iv.Providers)
	if iv.BusyProviders < 0 || iv.MaxProviderTokens < 0 {
		return
	}
	t.detailedProviders += int64(iv.Providers)
	t.busy += int64(iv.BusyProviders)
	if iv.Providers >= 2 {
		t.trimmedTokens += iv.DeltaTokens - iv.MaxProviderTokens
		t.trimmedHours += float64(iv.Providers-1) * hours
	}
}

func avgProviders(t *windowTotals, samples int) float64 {
	if t == nil || samples <= 0 {
		return 0
	}
	return float64(t.providerSum) / float64(samples)
}

func statsFor(t *windowTotals, samples int, priceMicro int64) WindowStats {
	if t == nil {
		return WindowStats{Samples: samples}
	}
	return t.stats(samples, priceMicro)
}

func (t *windowTotals) stats(samples int, priceMicro int64) WindowStats {
	out := WindowStats{Samples: samples, AvgProviders: avgProviders(t, samples)}
	if t.providerHours > 0 {
		out.RequestsPerHour = float64(t.deltaRequests) / t.providerHours
		out.TokensPerHour = float64(t.deltaTokens) / t.providerHours
	}
	out.USDPerHourMicro = usdPerHour(out.TokensPerHour, priceMicro)
	if t.detailedProviders > 0 {
		ratio := float64(t.busy) / float64(t.detailedProviders)
		out.BusyRatio = &ratio
	}
	if t.trimmedHours > 0 {
		tok := float64(t.trimmedTokens) / t.trimmedHours
		usd := usdPerHour(tok, priceMicro)
		out.TrimmedTokensPerHour = &tok
		out.TrimmedUSDPerHourMicro = &usd
	}
	return out
}

func usdPerHour(tokensPerHour float64, priceMicro int64) int64 {
	return int64(math.Round(tokensPerHour * float64(priceMicro) / 1e6))
}

// Estimate returns per-machine figures for every model seen in the latest Window intervals,
// plus the same figures over LongWindow. Revenue is output tokens times the official output
// price plus the estimated prompt tokens (output tokens times the prompt/completion ratio)
// times the official input price.
func Estimate(in EstimateInput) (cands []Candidate, samples int) {
	if in.Window < 1 || len(in.Intervals) == 0 {
		return nil, 0
	}
	var whole []Interval
	for _, iv := range in.Intervals {
		if iv.Scope == "" {
			whole = append(whole, iv)
		}
	}
	endedSet := latestEndedAt(whole, in.Window)
	if len(endedSet) == 0 {
		return nil, 0
	}
	samples = len(endedSet)
	var longSince time.Time
	longEnded := map[time.Time]bool{}
	if in.LongWindow > 0 {
		longSince = in.Now.Add(-in.LongWindow)
		for _, iv := range whole {
			if iv.EndedAt.After(longSince) {
				longEnded[iv.EndedAt] = true
			}
		}
	}
	type key struct{ model, scope string }
	short := map[key]*windowTotals{}
	long := map[key]*windowTotals{}
	bucket := func(m map[key]*windowTotals, k key) *windowTotals {
		t := m[k]
		if t == nil {
			t = &windowTotals{}
			m[k] = t
		}
		return t
	}
	for _, iv := range in.Intervals {
		if iv.Scope != "" && (iv.Scope != ScopeTier || !in.TierScope) {
			continue
		}
		hours := iv.EndedAt.Sub(iv.StartedAt).Hours()
		if hours <= 0 {
			continue
		}
		k := key{iv.Model, iv.Scope}
		if endedSet[iv.EndedAt] {
			bucket(short, k).add(iv, hours)
		}
		if longEnded[iv.EndedAt] {
			bucket(long, k).add(iv, hours)
		}
	}
	cands = make([]Candidate, 0, len(short))
	for k := range short {
		if k.scope != "" {
			continue
		}
		model := k.model
		scope, cohort := "", CohortAll
		if in.TierScope {
			if t := short[key{model, ScopeTier}]; t != nil && float64(t.providerSum)/float64(samples) >= float64(in.MinProviders) {
				scope, cohort = ScopeTier, CohortTier
			}
		}
		price, source := modelPrice(in.Prices, model, in.FallbackModel)
		ratio, ratioSource := promptRatio(in.OwnRatios, in.NetworkRatio, model)
		effective := price.Output + int64(math.Round(ratio*float64(price.Input)))
		st := short[key{model, scope}].stats(samples, effective)
		c := Candidate{
			Model: model, Downloaded: in.Downloaded[model],
			AvgProviders: st.AvgProviders, BusyRatio: st.BusyRatio,
			RequestsPerHour: st.RequestsPerHour, TokensPerHour: st.TokensPerHour,
			USDPerHourMicro: st.USDPerHourMicro, TrimmedUSDPerHourMicro: st.TrimmedUSDPerHourMicro,
			PriceSource: source, EffectivePriceMicro: effective, InputRatio: ratio, RatioSource: ratioSource,
			Cohort: cohort, Insufficient: st.AvgProviders < float64(in.MinProviders),
			AllAvgProviders: avgProviders(short[key{model, ""}], samples),
		}
		allStats := statsFor(short[key{model, ""}], samples, effective)
		c.All = &allStats
		if in.TierScope {
			v := avgProviders(short[key{model, ScopeTier}], samples)
			c.TierAvgProviders = &v
			tierStats := statsFor(short[key{model, ScopeTier}], samples, effective)
			c.Tier = &tierStats
		}
		if t := long[key{model, scope}]; t != nil {
			hour := t.stats(len(longEnded), effective)
			c.Hour = &hour
		}
		cands = append(cands, c)
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].USDPerHourMicro != cands[j].USDPerHourMicro {
			return cands[i].USDPerHourMicro > cands[j].USDPerHourMicro
		}
		return cands[i].Model < cands[j].Model
	})
	return cands, samples
}

func Recommend(cands []Candidate, samples, window int, nearZero int64, fallbackModel string) *Recommendation {
	if samples < window {
		return nil
	}
	var eligible []Candidate
	for _, c := range cands {
		if c.Downloaded && !c.Insufficient {
			eligible = append(eligible, c)
		}
	}
	fallbackUSD := int64(0)
	for _, c := range cands {
		if c.Model == fallbackModel {
			fallbackUSD = c.USDPerHourMicro
			break
		}
	}
	if len(eligible) == 0 {
		return &Recommendation{Model: fallbackModel, USDPerHourMicro: fallbackUSD, Fallback: true}
	}
	allNearZero := true
	for _, c := range eligible {
		if c.USDPerHourMicro >= nearZero {
			allNearZero = false
			break
		}
	}
	if allNearZero {
		return &Recommendation{Model: fallbackModel, USDPerHourMicro: fallbackUSD, Fallback: true}
	}
	top := eligible[0]
	for _, c := range cands {
		if c.Downloaded && !c.Insufficient {
			top = c
			break
		}
	}
	return &Recommendation{Model: top.Model, USDPerHourMicro: top.USDPerHourMicro}
}

func latestEndedAt(intervals []Interval, window int) map[time.Time]bool {
	seen := map[time.Time]bool{}
	var ends []time.Time
	for _, iv := range intervals {
		if seen[iv.EndedAt] {
			continue
		}
		seen[iv.EndedAt] = true
		ends = append(ends, iv.EndedAt)
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i].After(ends[j]) })
	if len(ends) > window {
		ends = ends[:window]
	}
	out := make(map[time.Time]bool, len(ends))
	for _, t := range ends {
		out[t] = true
	}
	return out
}

// TokenTrendPoint is one model's output-token rates at one sample. Tokens is
// per-machine hourly output; Total is the cohort hourly output.
type TokenTrendPoint struct {
	At     time.Time
	Tokens int64
	Total  int64
}

// TokenTrendSeries is one model's points, oldest first.
type TokenTrendSeries struct {
	Model  string
	Points []TokenTrendPoint
}

// OutputTokenTrend groups intervals by model using the same per-machine hourly rate as the
// candidate table: round(delta_tokens / (providers * hours)). Total is the cohort rate:
// round(delta_tokens / hours). Intervals with providers<=0, hours<=0, or delta_tokens<0
// are skipped and not filled with zeros. A non-empty models list keeps that order; an
// empty list returns every remaining model sorted by name.
func OutputTokenTrend(intervals []Interval, models []string) []TokenTrendSeries {
	type sample struct {
		at     time.Time
		tokens int64
		total  int64
	}
	byModel := map[string]map[int64]sample{}
	for _, iv := range intervals {
		hours := iv.EndedAt.Sub(iv.StartedAt).Hours()
		if iv.Providers <= 0 || hours <= 0 || iv.DeltaTokens < 0 {
			continue
		}
		key := iv.EndedAt.UTC().UnixMicro()
		points := byModel[iv.Model]
		if points == nil {
			points = map[int64]sample{}
			byModel[iv.Model] = points
		}
		points[key] = sample{
			at:     iv.EndedAt.UTC(),
			tokens: int64(math.Round(float64(iv.DeltaTokens) / (float64(iv.Providers) * hours))),
			total:  int64(math.Round(float64(iv.DeltaTokens) / hours)),
		}
	}
	var order []string
	if len(models) > 0 {
		seen := map[string]bool{}
		for _, model := range models {
			if seen[model] || byModel[model] == nil {
				continue
			}
			seen[model] = true
			order = append(order, model)
		}
	} else {
		order = make([]string, 0, len(byModel))
		for model := range byModel {
			order = append(order, model)
		}
		sort.Strings(order)
	}
	out := make([]TokenTrendSeries, 0, len(order))
	for _, model := range order {
		points := byModel[model]
		keys := make([]int64, 0, len(points))
		for key := range points {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		series := TokenTrendSeries{Model: model, Points: make([]TokenTrendPoint, 0, len(keys))}
		for _, key := range keys {
			pt := points[key]
			series.Points = append(series.Points, TokenTrendPoint{At: pt.at, Tokens: pt.tokens, Total: pt.total})
		}
		out = append(out, series)
	}
	return out
}

func modelPrice(prices map[string]Price, model, fallbackModel string) (Price, string) {
	if p, ok := prices[model]; ok {
		return p, "official"
	}
	if p, ok := prices[fallbackModel]; ok {
		return p, "fallback"
	}
	return defaultFallbackPrice, "fallback"
}

const minOwnRatioRecords = 30

// OwnRatios keeps models with enough billed records to give a stable prompt/completion ratio.
func OwnRatios(totals map[string]TokenTotals) map[string]float64 {
	out := map[string]float64{}
	for model, t := range totals {
		if t.Records >= minOwnRatioRecords && t.Completion > 0 && t.Prompt >= 0 {
			out[model] = float64(t.Prompt) / float64(t.Completion)
		}
	}
	return out
}

// NetworkRatio is the network-wide prompt/completion ratio over the last 24 hours, or 0.
func NetworkRatio(snap Snapshot) float64 {
	if snap.Output24h <= 0 || snap.Prompt24h < 0 {
		return 0
	}
	return float64(snap.Prompt24h) / float64(snap.Output24h)
}

func promptRatio(own map[string]float64, network float64, model string) (float64, string) {
	if r, ok := own[model]; ok {
		return r, RatioOwn
	}
	if network > 0 {
		return network, RatioNetwork
	}
	return 0, RatioNone
}
