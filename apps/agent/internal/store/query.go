package store

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"sort"
	"strconv"
	"time"
	_ "time/tzdata"
)

var (
	ErrDataUnavailable = errors.New("no successful account snapshot")
	ErrAmountOverflow  = errors.New("aggregate amount exceeds int64")
	ErrGroupLimit      = errors.New("too many statistical groups")
)

type ReadView struct {
	tx       *sql.Tx
	State    State
	Coverage VisibleCoverage
}

// Query holds one short, cancellable read snapshot for data and its metadata.
func (s *Store) Query(ctx context.Context, account string, now time.Time, fn func(*ReadView) error) error {
	tx, err := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := readState(ctx, tx, account)
	if err != nil {
		return err
	}
	if state.AccountID == "" || state.LastSuccess.IsZero() {
		return ErrDataUnavailable
	}
	coverage, err := readCoverage(ctx, tx, state, now)
	if err != nil {
		return err
	}
	if err = fn(&ReadView{tx: tx, State: state, Coverage: coverage}); err != nil {
		return err
	}
	return tx.Commit()
}

type EarningItem struct {
	AccountID   string `json:"account_id"`
	RecordID    string `json:"record_id"`
	ProviderID  string `json:"provider_id"`
	ProviderKey string `json:"provider_key"`
	JobID       string `json:"job_id"`
	Model       string `json:"model"`
	Amount      string `json:"amount_micro_usd"`
	Prompt      int64  `json:"prompt_tokens"`
	Completion  int64  `json:"completion_tokens"`
	CreatedAt   string `json:"created_at"`
}
type SortKey struct {
	At int64  `json:"at"`
	ID string `json:"id"`
}
type Page struct {
	Items []EarningItem
	Next  *SortKey
}

func (v *ReadView) Page(ctx context.Context, window QueryRange, model string, after *SortKey, limit int) (Page, error) {
	page := Page{Items: make([]EarningItem, 0, limit)}
	query := `SELECT record_id,provider_id,provider_key,job_id,model,amount_micro_usd,prompt_tokens,completion_tokens,created_at_us FROM earnings WHERE account_id=? AND created_at_us>=? AND created_at_us<?`
	args := []any{v.State.AccountID, window.From.UnixMicro(), window.To.UnixMicro()}
	if model != "" {
		query += " AND model=?"
		args = append(args, model)
	}
	if after != nil {
		query += " AND (created_at_us,record_id)<(?,?)"
		args = append(args, after.At, after.ID)
	}
	query += " ORDER BY created_at_us DESC,record_id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := v.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	var last SortKey
	for rows.Next() {
		var item EarningItem
		var amount, at int64
		if err = rows.Scan(&item.RecordID, &item.ProviderID, &item.ProviderKey, &item.JobID, &item.Model, &amount, &item.Prompt, &item.Completion, &at); err != nil {
			return page, err
		}
		if len(page.Items) == limit {
			page.Next = &last
			break
		}
		item.AccountID = v.State.AccountID
		// The sentinel is ours, not upstream's; detail pages and CSV must keep
		// showing the empty Provider that the reward row actually arrived with.
		if item.ProviderID == RewardProviderID && IsRewardModel(item.Model) {
			item.ProviderID = ""
		}
		item.Amount = strconv.FormatInt(amount, 10)
		item.CreatedAt = time.UnixMicro(at).UTC().Format(time.RFC3339Nano)
		page.Items = append(page.Items, item)
		last = SortKey{At: at, ID: item.RecordID}
	}
	return page, rows.Err()
}

type OfficialSummary struct {
	RecordCount  int64  `json:"record_count"`
	Total        string `json:"total_micro_usd"`
	Available    string `json:"available_balance_micro_usd"`
	Withdrawable string `json:"withdrawable_balance_micro_usd"`
	ObservedAt   string `json:"observed_at"`
}
type LocalTotal struct {
	RecordCount int64  `json:"record_count"`
	Amount      string `json:"amount_micro_usd"`
}

func (v *ReadView) Overview(ctx context.Context, window QueryRange) (OfficialSummary, LocalTotal, error) {
	var official OfficialSummary
	var local LocalTotal
	var total, available, withdrawable, observed int64
	err := v.tx.QueryRowContext(ctx, `SELECT total_record_count,total_micro_usd,available_balance_micro_usd,withdrawable_balance_micro_usd,observed_at_us FROM account_summary_current WHERE account_id=?`, v.State.AccountID).Scan(&official.RecordCount, &total, &available, &withdrawable, &observed)
	if err == sql.ErrNoRows {
		return official, local, ErrDataUnavailable
	}
	if err != nil {
		return official, local, err
	}
	official.Total = strconv.FormatInt(total, 10)
	official.Available = strconv.FormatInt(available, 10)
	official.Withdrawable = strconv.FormatInt(withdrawable, 10)
	official.ObservedAt = time.UnixMicro(observed).UTC().Format(time.RFC3339Nano)
	local, err = v.Total(ctx, window)
	return official, local, err
}

func (v *ReadView) Total(ctx context.Context, window QueryRange) (LocalTotal, error) {
	var local LocalTotal
	// Stream integer amounts: SQLite SUM can overflow before later negative adjustments.
	rows, err := v.tx.QueryContext(ctx, "SELECT amount_micro_usd FROM earnings WHERE account_id=? AND created_at_us>=? AND created_at_us<?", v.State.AccountID, window.From.UnixMicro(), window.To.UnixMicro())
	if err != nil {
		return local, err
	}
	defer rows.Close()
	var sum, operand big.Int
	for rows.Next() {
		var amount int64
		if err = rows.Scan(&amount); err != nil {
			return local, err
		}
		operand.SetInt64(amount)
		sum.Add(&sum, &operand)
		local.RecordCount++
	}
	if !sum.IsInt64() {
		return local, ErrAmountOverflow
	}
	local.Amount = sum.String()
	return local, rows.Err()
}

type StatsItem struct {
	Key         string  `json:"key"`
	From        *string `json:"from"`
	To          *string `json:"to"`
	RecordCount int64   `json:"record_count"`
	Amount      string  `json:"amount_micro_usd"`
}
type bucket struct {
	item       StatsItem
	start, end time.Time
	amount     big.Int
}

func (v *ReadView) Stats(ctx context.Context, window QueryRange, model, group string, loc *time.Location) ([]StatsItem, error) {
	query := "SELECT model,amount_micro_usd,created_at_us FROM earnings WHERE account_id=? AND created_at_us>=? AND created_at_us<?"
	args := []any{v.State.AccountID, window.From.UnixMicro(), window.To.UnixMicro()}
	if model != "" {
		query += " AND model=?"
		args = append(args, model)
	}
	rows, err := v.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string]*bucket{}
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		var name string
		var amount, at int64
		if err = rows.Scan(&name, &amount, &at); err != nil {
			return nil, err
		}
		local := time.UnixMicro(at).In(loc)
		key := name
		switch group {
		case "hour":
			_, offset := local.Zone()
			key = local.Format("2006-01-02T15") + "/" + strconv.Itoa(offset)
		case "day":
			key = local.Format(time.DateOnly)
		case "month":
			key = local.Format("2006-01")
		case "model":
		default:
			return nil, errors.New("invalid group")
		}
		b := groups[key]
		if b == nil {
			if len(groups) >= 10000 {
				return nil, ErrGroupLimit
			}
			b = &bucket{item: StatsItem{Key: key}}
			if group != "model" {
				b.start, b.end = bounds(local, group, loc)
				if group == "hour" {
					b.item.Key = b.start.In(loc).Format("2006-01-02T15:04:05Z07:00")
				}
				from, to := b.start.UTC().Format(time.RFC3339Nano), b.end.UTC().Format(time.RFC3339Nano)
				b.item.From = &from
				b.item.To = &to
			}
			groups[key] = b
		}
		var operand big.Int
		operand.SetInt64(amount)
		b.amount.Add(&b.amount, &operand)
		b.item.RecordCount++
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sorted := make([]*bucket, 0, len(groups))
	for _, b := range groups {
		sorted = append(sorted, b)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if group == "model" {
			return sorted[i].item.Key < sorted[j].item.Key
		}
		return sorted[i].start.Before(sorted[j].start)
	})
	out := make([]StatsItem, 0, len(sorted))
	for _, b := range sorted {
		if !b.amount.IsInt64() {
			return nil, ErrAmountOverflow
		}
		b.item.Amount = b.amount.String()
		out = append(out, b.item)
	}
	return out, nil
}
func bounds(local time.Time, group string, loc *time.Location) (time.Time, time.Time) {
	y, m, d := local.Date()
	switch group {
	case "month":
		start := calendarBoundary(y, m, 1, loc)
		next := time.Date(y, m, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
		ny, nm, nd := next.Date()
		return start, calendarBoundary(ny, nm, nd, loc)
	case "day":
		start := calendarBoundary(y, m, d, loc)
		next := time.Date(y, m, d, 12, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
		ny, nm, nd := next.Date()
		return start, calendarBoundary(ny, nm, nd, loc)
	default:
		_, offset := local.Zone()
		start := time.Date(y, m, d, local.Hour(), 0, 0, 0, time.FixedZone("", offset))
		end := start.Add(time.Hour)
		zoneStart, zoneEnd := local.ZoneBounds()
		if !zoneStart.IsZero() && zoneStart.After(start) {
			start = zoneStart
		}
		if !zoneEnd.IsZero() && zoneEnd.Before(end) {
			end = zoneEnd
		}
		return start, end
	}
}

// Find the first instant of the civil date, including zones that skip midnight.
// This runs once per calendar bucket; rows themselves are streamed without caching.
func calendarBoundary(y int, m time.Month, d int, loc *time.Location) time.Time {
	anchor := time.Date(y, m, d, 12, 0, 0, 0, loc)
	lo, hi := anchor.Add(-36*time.Hour).Unix(), anchor.Add(36*time.Hour).Unix()
	target := y*10000 + int(m)*100 + d
	for lo < hi {
		mid := lo + (hi-lo)/2
		my, mm, md := time.Unix(mid, 0).In(loc).Date()
		if my*10000+int(mm)*100+md < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return time.Unix(lo, 0).In(loc)
}
