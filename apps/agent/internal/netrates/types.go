package netrates

import "time"

type Cohort struct {
	ChipFamilies []string
	MemoryGB     int
}

type ProviderSnap struct {
	ID, Model      string
	Requests, Tokens int64
}

type Snapshot struct {
	At        time.Time
	Providers []ProviderSnap
}

type Interval struct {
	StartedAt, EndedAt            time.Time
	Model                         string
	Providers                     int
	DeltaRequests, DeltaTokens    int64
}

type Candidate struct {
	Model            string  `json:"model"`
	Downloaded       bool    `json:"downloaded"`
	AvgProviders     float64 `json:"avg_providers"`
	RequestsPerHour  float64 `json:"requests_per_hour"`
	TokensPerHour    float64 `json:"tokens_per_hour"`
	USDPerHourMicro  int64   `json:"-"`
	PriceSource      string  `json:"price_source"`
	Insufficient     bool    `json:"insufficient"`
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
