// Package upstream defines the verified wire format and read-only HTTP client.
package upstream

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"bloomer/agent/internal/config"
)

type Earning struct {
	AccountID        string
	RecordID         string
	ProviderID       string
	ProviderKey      string
	JobID            string
	Model            string
	AmountMicroUSD   int64
	PromptTokens     int64
	CompletionTokens int64
	CreatedAt        time.Time
}
type Summary struct {
	AccountID                   string
	TotalRecordCount            int64
	TotalMicroUSD               int64
	AvailableBalanceMicroUSD    int64
	WithdrawableBalanceMicroUSD int64
}
type Snapshot struct {
	Summary      Summary
	Earnings     []Earning
	HistoryLimit int
	RecentCount  int
}

type wireEarning struct {
	ID          json.RawMessage `json:"id"`
	AccountID   *string         `json:"account_id"`
	ProviderID  *string         `json:"provider_id"`
	ProviderKey *string         `json:"provider_key"`
	JobID       *string         `json:"job_id"`
	Model       *string         `json:"model"`
	Amount      *int64          `json:"amount_micro_usd"`
	Prompt      *int64          `json:"prompt_tokens"`
	Completion  *int64          `json:"completion_tokens"`
	CreatedAt   *string         `json:"created_at"`
}
type wireSnapshot struct {
	AccountID    *string        `json:"account_id"`
	Count        *int64         `json:"count"`
	Total        *int64         `json:"total_micro_usd"`
	Available    *int64         `json:"available_balance_micro_usd"`
	Withdrawable *int64         `json:"withdrawable_balance_micro_usd"`
	HistoryLimit *int           `json:"history_limit"`
	RecentCount  *int           `json:"recent_count"`
	Earnings     *[]wireEarning `json:"earnings"`
}

// Decode never uses float64. Missing/null required values differ from valid zero.
// Unknown fields (including email and decimal display amounts) are discarded.
// Same-ID identical records fold; conflicting duplicates fail the entire snapshot.
func Decode(r io.Reader, limit int, expectedAccount string) (Snapshot, error) {
	var out Snapshot
	if limit < 1 || limit > 1000 {
		return out, errors.New("unsupported response limit")
	}
	body, err := io.ReadAll(io.LimitReader(r, config.MaxResponseBytes+1))
	if err != nil {
		return out, &readError{cause: err}
	}
	if len(body) > config.MaxResponseBytes {
		return out, errors.New("response exceeds size limit")
	}
	// encoding/json accepts duplicate object members. Reject these before decoding
	// so conflicting amounts cannot silently become last-key-wins.
	if err = uniqueMembers(body); err != nil {
		return out, err
	}
	var wire wireSnapshot
	if err = json.Unmarshal(body, &wire); err != nil {
		return out, errors.New("invalid upstream JSON or field type")
	}
	if wire.AccountID == nil || *wire.AccountID == "" || wire.Count == nil || wire.Total == nil || wire.Available == nil || wire.Withdrawable == nil || wire.HistoryLimit == nil || wire.RecentCount == nil || wire.Earnings == nil {
		return out, errors.New("missing required snapshot field")
	}
	if expectedAccount != "" && *wire.AccountID != expectedAccount {
		return out, ErrAccountMismatch
	}
	if *wire.Count < 0 || *wire.RecentCount != len(*wire.Earnings) || len(*wire.Earnings) > limit || *wire.HistoryLimit != limit {
		return out, errors.New("invalid upstream record counts")
	}
	out.Summary = Summary{*wire.AccountID, *wire.Count, *wire.Total, *wire.Available, *wire.Withdrawable}
	out.HistoryLimit = *wire.HistoryLimit
	out.RecentCount = *wire.RecentCount
	out.Earnings = make([]Earning, 0, len(*wire.Earnings))
	seen := map[string]Earning{}
	for _, v := range *wire.Earnings {
		if v.AccountID == nil || v.ProviderID == nil || v.ProviderKey == nil || v.JobID == nil || v.Model == nil || *v.Model == "" || v.Amount == nil || v.Prompt == nil || v.Completion == nil || v.CreatedAt == nil {
			return Snapshot{}, errors.New("missing required earning field")
		}
		if *v.AccountID != out.Summary.AccountID {
			return Snapshot{}, ErrAccountMismatch
		}
		if *v.Prompt < 0 || *v.Completion < 0 {
			return Snapshot{}, errors.New("negative token count")
		}
		id, err := decodeID(v.ID)
		if err != nil {
			return Snapshot{}, err
		}
		at, err := time.Parse(time.RFC3339Nano, *v.CreatedAt)
		if err != nil || at.IsZero() || at.Nanosecond()%1000 != 0 {
			return Snapshot{}, errors.New("invalid earning timestamp or unsupported sub-microsecond precision")
		}
		record := Earning{*v.AccountID, id, *v.ProviderID, *v.ProviderKey, *v.JobID, *v.Model, *v.Amount, *v.Prompt, *v.Completion, at.UTC()}
		if prior, ok := seen[id]; ok {
			if prior != record {
				return Snapshot{}, errors.New("conflicting duplicate earning ID")
			}
			continue
		}
		seen[id] = record
		out.Earnings = append(out.Earnings, record)
	}
	if *wire.Count < int64(len(out.Earnings)) {
		return Snapshot{}, errors.New("invalid upstream unique record count")
	}
	return out, nil
}

type readError struct{ cause error }

func (e *readError) Error() string { return "upstream response read failed" }
func (e *readError) Unwrap() error { return e.cause }

func decodeID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("missing earning ID")
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil || strings.TrimSpace(s) == "" {
			return "", errors.New("empty earning ID")
		}
		return s, nil
	}
	s := string(raw)
	digits := strings.TrimPrefix(s, "-")
	if digits == "" {
		return "", errors.New("invalid earning ID")
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return "", errors.New("earning ID must be text or an integer")
		}
	}
	return s, nil
}

func uniqueMembers(body []byte) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return errors.New("upstream JSON nesting exceeds limit")
		}
		t, err := d.Token()
		if err != nil {
			return errors.New("malformed upstream JSON")
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return errors.New("malformed upstream object")
				}
				key, ok := k.(string)
				if !ok || keys[key] {
					return errors.New("duplicate or invalid upstream JSON member")
				}
				keys[key] = true
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid upstream JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("response must contain one JSON value")
	}
	return nil
}
