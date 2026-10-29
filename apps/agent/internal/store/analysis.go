package store

import (
	"context"
	"errors"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrDeviceIdentityUnavailable = errors.New("device identity unavailable")

type AnalysisTotals struct {
	AmountMicroUSD string `json:"amount_micro_usd"`
	RecordCount    int64  `json:"record_count"`
}

type AnalysisModelRow struct {
	Model          string `json:"model"`
	AmountMicroUSD string `json:"amount_micro_usd"`
	RecordCount    int64  `json:"record_count"`
	ActiveBuckets  int64  `json:"active_buckets"`
}

type AnalysisSeriesEntry struct {
	Model          string `json:"model"`
	AmountMicroUSD string `json:"amount_micro_usd"`
	RecordCount    int64  `json:"record_count"`
}

type AnalysisSeriesBucket struct {
	From  string                `json:"from"`
	To    string                `json:"to"`
	Items []AnalysisSeriesEntry `json:"items"`
}

type AnalysisHourOfDay struct {
	Hour           int    `json:"hour"`
	Model          string `json:"model"`
	AmountMicroUSD string `json:"amount_micro_usd"`
	RecordCount    int64  `json:"record_count"`
}

type AnalysisWeekdayHour struct {
	Weekday        int    `json:"weekday"`
	Hour           int    `json:"hour"`
	AmountMicroUSD string `json:"amount_micro_usd"`
	RecordCount    int64  `json:"record_count"`
}

type AnalysisResult struct {
	Timezone    string                 `json:"timezone"`
	Granularity string                 `json:"granularity"`
	From        string                 `json:"from"`
	To          string                 `json:"to"`
	Totals      AnalysisTotals         `json:"totals"`
	Models      []AnalysisModelRow     `json:"models"`
	Series      []AnalysisSeriesBucket `json:"series"`
	HourOfDay   []AnalysisHourOfDay    `json:"hour_of_day"`
	WeekdayHour []AnalysisWeekdayHour  `json:"weekday_hour"`
}

type aggAmount struct {
	amount big.Int
	count  int64
}

type seriesBucket struct {
	start, end time.Time
	from, to   string
	items      map[string]*aggAmount
}

func (v *ReadView) DeviceProviderIDs(ctx context.Context) ([]string, error) {
	rows, err := v.tx.QueryContext(ctx, "SELECT DISTINCT provider_id FROM device_provider_ids ORDER BY provider_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (v *ReadView) EarningsAnalysis(ctx context.Context, window QueryRange, granularity string, models []string, loc *time.Location, zone string) (AnalysisResult, error) {
	out := AnalysisResult{
		Timezone: zone, Granularity: granularity,
		From: window.From.Format(time.RFC3339Nano), To: window.To.Format(time.RFC3339Nano),
		Models: make([]AnalysisModelRow, 0), Series: make([]AnalysisSeriesBucket, 0),
		HourOfDay: make([]AnalysisHourOfDay, 0), WeekdayHour: make([]AnalysisWeekdayHour, 0),
	}
	ids, err := v.DeviceProviderIDs(ctx)
	if err != nil {
		return out, err
	}
	if len(ids) == 0 {
		return out, ErrDeviceIdentityUnavailable
	}
	filter := map[string]bool{}
	for _, m := range models {
		filter[m] = true
	}
	all := len(models) == 0
	// Rewards are paid to the account rather than to one Provider, so they are
	// stored under RewardProviderID instead of a verified device Provider.
	query := `SELECT model,amount_micro_usd,created_at_us FROM earnings
 WHERE account_id=? AND created_at_us>=? AND created_at_us<?
 AND (provider_id IN (SELECT provider_id FROM device_provider_ids) OR provider_id=?)`
	rows, err := v.tx.QueryContext(ctx, query, v.State.AccountID, window.From.UnixMicro(), window.To.UnixMicro(), RewardProviderID)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	modelAll := map[string]*struct {
		amount        big.Int
		count         int64
		activeBuckets map[string]struct{}
	}{}
	series := map[string]*seriesBucket{}
	hourModel := map[string]*aggAmount{}
	weekdayHour := map[string]*aggAmount{}
	var totals aggAmount
	groupCount := 0

	addGroup := func() error {
		groupCount++
		if groupCount > 10000 {
			return ErrGroupLimit
		}
		return nil
	}

	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		var name string
		var amount, at int64
		if err = rows.Scan(&name, &amount, &at); err != nil {
			return out, err
		}
		local := time.UnixMicro(at).In(loc)
		bucketKey, start, end := analysisBucket(local, granularity, loc)

		m := modelAll[name]
		if m == nil {
			if err = addGroup(); err != nil {
				return out, err
			}
			m = &struct {
				amount        big.Int
				count         int64
				activeBuckets map[string]struct{}
			}{activeBuckets: map[string]struct{}{}}
			modelAll[name] = m
		}
		var operand big.Int
		operand.SetInt64(amount)
		m.amount.Add(&m.amount, &operand)
		m.count++
		m.activeBuckets[bucketKey] = struct{}{}

		if !all && !filter[name] {
			continue
		}
		totals.amount.Add(&totals.amount, &operand)
		totals.count++

		sb := series[bucketKey]
		if sb == nil {
			if err = addGroup(); err != nil {
				return out, err
			}
			from := start.UTC().Format(time.RFC3339Nano)
			to := end.UTC().Format(time.RFC3339Nano)
			sb = &seriesBucket{start: start, end: end, from: from, to: to, items: map[string]*aggAmount{}}
			series[bucketKey] = sb
		}
		item := sb.items[name]
		if item == nil {
			item = &aggAmount{}
			sb.items[name] = item
		}
		item.amount.Add(&item.amount, &operand)
		item.count++

		hour := local.Hour()
		hmKey := strconv.Itoa(hour) + "\x00" + name
		hm := hourModel[hmKey]
		if hm == nil {
			if err = addGroup(); err != nil {
				return out, err
			}
			hm = &aggAmount{}
			hourModel[hmKey] = hm
		}
		hm.amount.Add(&hm.amount, &operand)
		hm.count++

		weekday := int(local.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		whKey := strconv.Itoa(weekday) + "\x00" + strconv.Itoa(hour)
		wh := weekdayHour[whKey]
		if wh == nil {
			if err = addGroup(); err != nil {
				return out, err
			}
			wh = &aggAmount{}
			weekdayHour[whKey] = wh
		}
		wh.amount.Add(&wh.amount, &operand)
		wh.count++
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if !totals.amount.IsInt64() {
		return out, ErrAmountOverflow
	}
	out.Totals = AnalysisTotals{AmountMicroUSD: totals.amount.String(), RecordCount: totals.count}

	modelRows := make([]AnalysisModelRow, 0, len(modelAll))
	for name, m := range modelAll {
		if !m.amount.IsInt64() {
			return out, ErrAmountOverflow
		}
		modelRows = append(modelRows, AnalysisModelRow{
			Model: name, AmountMicroUSD: m.amount.String(), RecordCount: m.count,
			ActiveBuckets: int64(len(m.activeBuckets)),
		})
	}
	sort.Slice(modelRows, func(i, j int) bool {
		var ai, aj big.Int
		ai.SetString(modelRows[i].AmountMicroUSD, 10)
		aj.SetString(modelRows[j].AmountMicroUSD, 10)
		if c := aj.Cmp(&ai); c != 0 {
			return c < 0
		}
		return modelRows[i].Model < modelRows[j].Model
	})
	out.Models = modelRows

	seriesList := make([]*seriesBucket, 0, len(series))
	for _, b := range series {
		seriesList = append(seriesList, b)
	}
	sort.Slice(seriesList, func(i, j int) bool { return seriesList[i].start.Before(seriesList[j].start) })
	for _, b := range seriesList {
		entries := make([]AnalysisSeriesEntry, 0, len(b.items))
		for model, agg := range b.items {
			if !agg.amount.IsInt64() {
				return out, ErrAmountOverflow
			}
			entries = append(entries, AnalysisSeriesEntry{Model: model, AmountMicroUSD: agg.amount.String(), RecordCount: agg.count})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Model < entries[j].Model })
		out.Series = append(out.Series, AnalysisSeriesBucket{From: b.from, To: b.to, Items: entries})
	}

	for k, agg := range hourModel {
		if !agg.amount.IsInt64() {
			return out, ErrAmountOverflow
		}
		sep := strings.IndexByte(k, 0)
		hour, _ := strconv.Atoi(k[:sep])
		out.HourOfDay = append(out.HourOfDay, AnalysisHourOfDay{
			Hour: hour, Model: k[sep+1:], AmountMicroUSD: agg.amount.String(), RecordCount: agg.count,
		})
	}
	sort.Slice(out.HourOfDay, func(i, j int) bool {
		a, b := out.HourOfDay[i], out.HourOfDay[j]
		if a.Hour != b.Hour {
			return a.Hour < b.Hour
		}
		return a.Model < b.Model
	})

	for k, agg := range weekdayHour {
		if !agg.amount.IsInt64() {
			return out, ErrAmountOverflow
		}
		sep := strings.IndexByte(k, 0)
		weekday, _ := strconv.Atoi(k[:sep])
		hour, _ := strconv.Atoi(k[sep+1:])
		out.WeekdayHour = append(out.WeekdayHour, AnalysisWeekdayHour{
			Weekday: weekday, Hour: hour, AmountMicroUSD: agg.amount.String(), RecordCount: agg.count,
		})
	}
	sort.Slice(out.WeekdayHour, func(i, j int) bool {
		a, b := out.WeekdayHour[i], out.WeekdayHour[j]
		if a.Weekday != b.Weekday {
			return a.Weekday < b.Weekday
		}
		return a.Hour < b.Hour
	})
	return out, nil
}

func analysisBucket(local time.Time, granularity string, loc *time.Location) (key string, start, end time.Time) {
	switch granularity {
	case "month":
		start, end = bounds(local, "month", loc)
		key = start.In(loc).Format("2006-01")
	case "day":
		start, end = bounds(local, "day", loc)
		key = start.In(loc).Format(time.DateOnly)
	default:
		start, end = bounds(local, "hour", loc)
		_, offset := local.Zone()
		key = local.Format("2006-01-02T15") + "/" + strconv.Itoa(offset)
	}
	return key, start, end
}
