package netrates

import "time"

// ScopeTier marks intervals aggregated from providers whose chip tier matches the configured
// tiers. An empty scope means the whole family+memory cohort.
const ScopeTier = "tier"

type Cohort struct {
	ChipFamilies []string
	MemoryGB     int
}

type ProviderSnap struct {
	ID, Model, Tier  string
	Requests, Tokens int64
}

type Snapshot struct {
	At                   time.Time
	Providers            []ProviderSnap
	Prompt24h, Output24h int64
}

// Interval is one model's aggregate between two snapshots. BusyProviders and MaxProviderTokens
// are -1 for rows recorded before they were tracked.
type Interval struct {
	StartedAt, EndedAt         time.Time
	Model, Scope               string
	Providers, BusyProviders   int
	DeltaRequests, DeltaTokens int64
	MaxProviderTokens          int64
}

// Price is micro-USD per million tokens.
type Price struct {
	Input, Output int64
}

// TokenTotals is this account's own billed tokens for one model.
type TokenTotals struct {
	Records, Prompt, Completion int64
}

// WindowStats is one model's per-machine figures over one window. BusyRatio and the trimmed
// figures are nil when the window has no detailed rows (or a single machine for trimming).
type WindowStats struct {
	Samples                int
	AvgProviders           float64
	BusyRatio              *float64
	RequestsPerHour        float64
	TokensPerHour          float64
	TrimmedTokensPerHour   *float64
	USDPerHourMicro        int64
	TrimmedUSDPerHourMicro *int64
}

type Candidate struct {
	Model                  string
	Downloaded             bool
	AvgProviders           float64
	BusyRatio              *float64
	RequestsPerHour        float64
	TokensPerHour          float64
	USDPerHourMicro        int64
	TrimmedUSDPerHourMicro *int64
	Hour                   *WindowStats
	PriceSource            string
	EffectivePriceMicro    int64
	InputRatio             float64
	RatioSource            string
	Cohort                 string
	Insufficient           bool
	// TierAvgProviders is the same-tier average; nil when chip_tiers is empty.
	TierAvgProviders *float64
	AllAvgProviders  float64
	// All and Tier are the short-window figures for every model, so the dashboard can
	// show one cohort at a time instead of mixing per-model fallbacks. Tier is nil
	// when chip_tiers is empty. Auto-switch still uses the top-level fields.
	All  *WindowStats
	Tier *WindowStats
}

type Recommendation struct {
	Model           string
	USDPerHourMicro int64
	Fallback        bool
}

type SummaryItem struct {
	Model           string  `json:"model"`
	USDPerHourMicro int64   `json:"usd_per_hour_micro"`
	AvgProviders    float64 `json:"avg_providers"`
}

type SwitchRecord struct {
	ID                              int64
	At                              time.Time
	From, To, Reason, Status, Error string
	JobID                           string
	Summary                         []SummaryItem
}
