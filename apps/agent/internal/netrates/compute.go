package netrates

import (
	"math"
	"sort"
	"time"
)

const defaultFallbackPriceMicro = int64(220000)

func Diff(prev, next Snapshot, interval time.Duration) (intervals []Interval, valid bool) {
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
	type agg struct {
		providers                  int
		deltaRequests, deltaTokens int64
	}
	byModel := map[string]*agg{}
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
		a := byModel[n.Model]
		if a == nil {
			a = &agg{}
			byModel[n.Model] = a
		}
		a.providers++
		a.deltaRequests += dr
		a.deltaTokens += dt
	}
	if len(byModel) == 0 {
		return nil, true
	}
	models := make([]string, 0, len(byModel))
	for model := range byModel {
		models = append(models, model)
	}
	sort.Strings(models)
	out := make([]Interval, 0, len(models))
	for _, model := range models {
		a := byModel[model]
		out = append(out, Interval{
			StartedAt: prev.At, EndedAt: next.At, Model: model,
			Providers: a.providers, DeltaRequests: a.deltaRequests, DeltaTokens: a.deltaTokens,
		})
	}
	return out, true
}

func Estimate(intervals []Interval, window int, prices map[string]int64, fallbackModel string, downloaded map[string]bool, minProviders int) (cands []Candidate, samples int) {
	if window < 1 || len(intervals) == 0 {
		return nil, 0
	}
	endedSet := latestEndedAt(intervals, window)
	if len(endedSet) == 0 {
		return nil, 0
	}
	samples = len(endedSet)
	type totals struct {
		providerHours              float64
		deltaRequests, deltaTokens int64
		providerSum                int64
	}
	byModel := map[string]*totals{}
	for _, iv := range intervals {
		if !endedSet[iv.EndedAt] {
			continue
		}
		hours := iv.EndedAt.Sub(iv.StartedAt).Hours()
		if hours <= 0 {
			continue
		}
		t := byModel[iv.Model]
		if t == nil {
			t = &totals{}
			byModel[iv.Model] = t
		}
		t.providerHours += float64(iv.Providers) * hours
		t.deltaRequests += iv.DeltaRequests
		t.deltaTokens += iv.DeltaTokens
		t.providerSum += int64(iv.Providers)
	}
	cands = make([]Candidate, 0, len(byModel))
	for model, t := range byModel {
		var reqPerHour, tokPerHour float64
		if t.providerHours > 0 {
			reqPerHour = float64(t.deltaRequests) / t.providerHours
			tokPerHour = float64(t.deltaTokens) / t.providerHours
		}
		avg := float64(t.providerSum) / float64(samples)
		price, source := modelPrice(prices, model, fallbackModel)
		usd := int64(math.Round(tokPerHour * float64(price) / 1e6))
		cands = append(cands, Candidate{
			Model: model, Downloaded: downloaded[model],
			AvgProviders: avg, RequestsPerHour: reqPerHour, TokensPerHour: tokPerHour,
			USDPerHourMicro: usd, PriceSource: source,
			Insufficient: avg < float64(minProviders),
		})
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

// TokenTrendPoint is one model's per-machine output-token rate at one sample.
type TokenTrendPoint struct {
	At     time.Time
	Tokens int64
}

// TokenTrendSeries is one model's points, oldest first.
type TokenTrendSeries struct {
	Model  string
	Points []TokenTrendPoint
}

// OutputTokenTrend groups intervals by model using the same per-machine hourly rate as the
// candidate table: round(delta_tokens / (providers * hours)). Intervals with providers<=0,
// hours<=0, or delta_tokens<0 are skipped and not filled with zeros. A non-empty models list
// keeps that order; an empty list returns every remaining model sorted by name.
func OutputTokenTrend(intervals []Interval, models []string) []TokenTrendSeries {
	type sample struct {
		at     time.Time
		tokens int64
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
			series.Points = append(series.Points, TokenTrendPoint{At: pt.at, Tokens: pt.tokens})
		}
		out = append(out, series)
	}
	return out
}

func modelPrice(prices map[string]int64, model, fallbackModel string) (int64, string) {
	if p, ok := prices[model]; ok {
		return p, "official"
	}
	if p, ok := prices[fallbackModel]; ok {
		return p, "fallback"
	}
	return defaultFallbackPriceMicro, "fallback"
}
