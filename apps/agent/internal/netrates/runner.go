package netrates

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/control"
)

const roundTimeout = 90 * time.Second
const sampleMaxAge = 10 * time.Minute
const tokenTrendWindow = 24 * time.Hour
const longWindow = time.Hour
const ownRatioLookback = 7 * 24 * time.Hour

type netRateStore interface {
	LoadNetRateBaseline(ctx context.Context) (Snapshot, bool, error)
	SaveNetRateRound(ctx context.Context, snap Snapshot, intervals []Interval, retainAfter time.Time) error
	NetRateIntervals(ctx context.Context, since time.Time) ([]Interval, error)
	InsertNetRateSwitch(ctx context.Context, r SwitchRecord) (int64, error)
	UpdateNetRateSwitch(ctx context.Context, id int64, status, errCode string) error
	UpdateNetRateSwitchJob(ctx context.Context, id int64, jobID string) error
	NetRateSwitches(ctx context.Context, limit int) ([]SwitchRecord, error)
}

type statsClient interface {
	Stats(ctx context.Context, cohort Cohort) (Snapshot, error)
	Pricing(ctx context.Context) (map[string]Price, error)
}

// OwnTokenTotals reads this account's billed prompt and completion tokens per model since a time.
type OwnTokenTotals func(ctx context.Context, since time.Time) (map[string]TokenTotals, error)

type controlBridge interface {
	Models(ctx context.Context) (control.Models, error)
	Submit(r control.Request) (control.Job, error)
	LastModelChange() time.Time
	Job(id string) (control.Job, bool)
	Busy() bool
}

type CandidateView struct {
	Model                  string             `json:"model"`
	Downloaded             bool               `json:"downloaded"`
	AvgProviders           float64            `json:"avg_providers"`
	BusyRatio              *float64           `json:"busy_ratio"`
	RequestsPerHour        float64            `json:"requests_per_hour"`
	TokensPerHour          float64            `json:"tokens_per_hour"`
	USDPerHourMicro        string             `json:"usd_per_hour_micro"`
	TrimmedUSDPerHourMicro *string            `json:"trimmed_usd_per_hour_micro"`
	Hour                   *CandidateHourView `json:"hour"`
	PriceSource            string             `json:"price_source"`
	EffectivePriceMicro    string             `json:"effective_price_micro"`
	InputRatio             float64            `json:"input_ratio"`
	RatioSource            string             `json:"ratio_source"`
	Cohort                 string             `json:"cohort"`
	Insufficient           bool               `json:"insufficient"`
}

type CandidateHourView struct {
	Samples                int      `json:"samples"`
	AvgProviders           float64  `json:"avg_providers"`
	BusyRatio              *float64 `json:"busy_ratio"`
	RequestsPerHour        float64  `json:"requests_per_hour"`
	TokensPerHour          float64  `json:"tokens_per_hour"`
	USDPerHourMicro        string   `json:"usd_per_hour_micro"`
	TrimmedUSDPerHourMicro *string  `json:"trimmed_usd_per_hour_micro"`
}

type RecommendationView struct {
	Model           string `json:"model"`
	USDPerHourMicro string `json:"usd_per_hour_micro"`
	Fallback        bool   `json:"fallback"`
}

type SwitchGateView struct {
	NextEligibleAt *string `json:"next_eligible_at"`
	BlockedReason  *string `json:"blocked_reason"`
	BackoffUntil   *string `json:"backoff_until"`
}

type StatusView struct {
	Enabled         bool                `json:"enabled"`
	AutoSwitch      bool                `json:"auto_switch"`
	AutoSwitchSince *string             `json:"auto_switch_since"`
	WindowSamples   int                 `json:"window_samples"`
	Samples         int                 `json:"samples"`
	UpdatedAt       *string             `json:"updated_at"`
	SnapshotAt      *string             `json:"snapshot_at"`
	CurrentModel    *string             `json:"current_model"`
	Recommendation  *RecommendationView `json:"recommendation"`
	Candidates      []CandidateView     `json:"candidates"`
	Switch          SwitchGateView      `json:"switch"`
	LastError       *string             `json:"last_error"`
	IntervalMinutes int                 `json:"interval_minutes"`

	LongWindowMinutes    int      `json:"long_window_minutes"`
	SwitchAdvantageRatio float64  `json:"switch_advantage_ratio"`
	ChipTiers            []string `json:"chip_tiers"`
	MemoryGB             int      `json:"memory_gb"`
}

type TokenTrendPointView struct {
	At     string `json:"at"`
	Tokens int64  `json:"tokens"`
}

type TokenTrendSeriesView struct {
	Model  string                `json:"model"`
	Points []TokenTrendPointView `json:"points"`
}

type TokenTrendView struct {
	Series []TokenTrendSeriesView `json:"series"`
}

type SwitchItemView struct {
	ID        int64         `json:"id"`
	At        string        `json:"at"`
	FromModel string        `json:"from_model"`
	ToModel   string        `json:"to_model"`
	Reason    string        `json:"reason"`
	Status    string        `json:"status"`
	Error     *string       `json:"error"`
	JobID     string        `json:"job_id"`
	Summary   []SummaryView `json:"summary"`
}

type SummaryView struct {
	Model           string  `json:"model"`
	USDPerHourMicro string  `json:"usd_per_hour_micro"`
	AvgProviders    float64 `json:"avg_providers"`
}

type Runner struct {
	mu             sync.RWMutex
	cfg            config.NetworkRates
	settings       *Settings
	store          netRateStore
	client         statsClient
	control        controlBridge
	currentModel   func(context.Context) (string, bool)
	now            func() time.Time
	log            *slog.Logger
	interval       time.Duration
	minSwitch      time.Duration
	wakeSwitch     chan struct{}
	prices         map[string]Price
	networkRatio   float64
	ownTotals      OwnTokenTotals
	lastSnapshotAt time.Time
	view           StatusView
}

type estimateState struct {
	now                                       time.Time
	cands                                     []Candidate
	samples                                   int
	rec                                       *Recommendation
	current                                   string
	currentOK, modelsUnavailable, controlBusy bool
	downloaded                                map[string]bool
	autoSwitch                                bool
	autoSince, lastSwitchAt, backoffUntil     *time.Time
}

func NewRunner(cfg config.NetworkRates, settings *Settings, store netRateStore, client statsClient, ctl controlBridge, currentModel func(context.Context) (string, bool), now func() time.Time, log *slog.Logger) *Runner {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		cfg: cfg, settings: settings, store: store, client: client, control: ctl,
		currentModel: currentModel, now: now, log: log,
		interval:   time.Duration(cfg.IntervalMinutes) * time.Minute,
		minSwitch:  time.Duration(cfg.SwitchMinIntervalMinutes) * time.Minute,
		wakeSwitch: make(chan struct{}, 1),
		view: StatusView{
			Enabled: true, WindowSamples: cfg.WindowSamples, IntervalMinutes: cfg.IntervalMinutes, Candidates: []CandidateView{},
			LongWindowMinutes: int(longWindow / time.Minute), SwitchAdvantageRatio: cfg.SwitchAdvantageRatio,
			ChipTiers: append([]string{}, cfg.ChipTiers...), MemoryGB: cfg.MemoryGB,
		},
	}
}

// WithOwnTokenTotals lets estimates use this account's own prompt/completion ratio per model.
func (r *Runner) WithOwnTokenTotals(fn OwnTokenTotals) *Runner {
	r.ownTotals = fn
	return r
}

func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.collectLoop(ctx)
	}()
	r.switchLoop(ctx)
	wg.Wait()
}

func (r *Runner) collectLoop(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.runCollect(ctx)
	r.notifySwitch()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runCollect(ctx)
			r.notifySwitch()
		}
	}
}

func (r *Runner) switchLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		delay, timed := r.timeUntilSwitchAttempt()
		if !timed {
			select {
			case <-ctx.Done():
				return
			case <-r.wakeSwitch:
			}
			continue
		}
		if delay <= 0 {
			r.runSwitch(ctx)
			if nextDelay, nextTimed := r.timeUntilSwitchAttempt(); !nextTimed || nextDelay <= 0 {
				select {
				case <-ctx.Done():
					return
				case <-r.wakeSwitch:
				}
			}
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			r.runSwitch(ctx)
		case <-r.wakeSwitch:
			timer.Stop()
		}
	}
}

func (r *Runner) notifySwitch() {
	if r.wakeSwitch == nil {
		return
	}
	select {
	case r.wakeSwitch <- struct{}{}:
	default:
	}
}

func (r *Runner) timeUntilSwitchAttempt() (time.Duration, bool) {
	autoSwitch, autoSince, lastSwitchAt, backoffUntil, _ := r.settings.Snapshot()
	if !autoSwitch {
		return 0, false
	}
	now := r.now().UTC()
	next := nextEligibleAt(autoSwitch, autoSince, r.lastModelChange(), lastSwitchAt, r.minSwitch, backoffUntil)
	if next == nil {
		return 0, true
	}
	d := next.Sub(now)
	if d < 0 {
		return 0, true
	}
	return d, true
}

func (r *Runner) Status() StatusView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneStatus(r.view)
}

func (r *Runner) SetAutoSwitch(on bool) (StatusView, error) {
	now := r.now().UTC()
	if err := r.settings.SetAutoSwitch(on, now); err != nil {
		return StatusView{}, err
	}
	r.mu.Lock()
	r.view.AutoSwitch = on
	if on {
		r.view.AutoSwitchSince = formatTime(now)
	} else {
		r.view.AutoSwitchSince = nil
	}
	r.refreshSwitchGateLocked(now)
	view := cloneStatus(r.view)
	r.mu.Unlock()
	r.notifySwitch()
	return view, nil
}

func (r *Runner) TokenTrend(ctx context.Context) (TokenTrendView, error) {
	r.mu.RLock()
	models := make([]string, 0, len(r.view.Candidates))
	tierModels := map[string]bool{}
	for _, candidate := range r.view.Candidates {
		models = append(models, candidate.Model)
		if candidate.Cohort == CohortTier {
			tierModels[candidate.Model] = true
		}
	}
	since := r.now().Add(-tokenTrendWindow)
	r.mu.RUnlock()
	intervals, err := r.store.NetRateIntervals(ctx, since)
	if err != nil {
		return TokenTrendView{}, err
	}
	scoped := intervals[:0:0]
	for _, iv := range intervals {
		if (iv.Scope == ScopeTier) == tierModels[iv.Model] {
			scoped = append(scoped, iv)
		}
	}
	computed := OutputTokenTrend(scoped, models)
	view := TokenTrendView{Series: make([]TokenTrendSeriesView, 0, len(computed))}
	for _, series := range computed {
		points := make([]TokenTrendPointView, 0, len(series.Points))
		for _, point := range series.Points {
			points = append(points, TokenTrendPointView{At: point.At.UTC().Format(time.RFC3339Nano), Tokens: point.Tokens})
		}
		view.Series = append(view.Series, TokenTrendSeriesView{Model: series.Model, Points: points})
	}
	return view, nil
}

func (r *Runner) Switches(ctx context.Context, limit int) ([]SwitchItemView, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 50 {
		limit = 50
	}
	rows, err := r.store.NetRateSwitches(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SwitchItemView, 0, len(rows))
	for _, row := range rows {
		out = append(out, switchToView(row))
	}
	return out, nil
}

func (r *Runner) runRound(parent context.Context) {
	r.runCollect(parent)
	r.runSwitch(parent)
}

func (r *Runner) runCollect(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, roundTimeout)
	defer cancel()
	now := r.now().UTC().Truncate(time.Microsecond)
	if r.lastSnapshotAt.IsZero() {
		if prev, ok, err := r.store.LoadNetRateBaseline(ctx); err == nil && ok {
			r.lastSnapshotAt = prev.At.UTC()
		}
	}
	r.reconcileSwitches(ctx, now)

	var lastErr *string
	statsSnap, statsErr := r.client.Stats(ctx, Cohort{ChipFamilies: r.cfg.ChipFamilies, MemoryGB: r.cfg.MemoryGB})
	if statsErr != nil {
		code := "stats_unavailable"
		lastErr = &code
		r.log.Warn("network rates stats unavailable")
	} else {
		prices, priceErr := r.client.Pricing(ctx)
		if priceErr != nil {
			code := "pricing_unavailable"
			lastErr = &code
			r.log.Warn("network rates pricing unavailable")
		} else {
			r.mu.Lock()
			r.prices = prices
			r.mu.Unlock()
		}
		if ratio := NetworkRatio(statsSnap); ratio > 0 {
			r.mu.Lock()
			r.networkRatio = ratio
			r.mu.Unlock()
		}
		if err := r.persistSnapshot(ctx, statsSnap, now); err != nil {
			r.log.Warn("network rates persist failed", "error", err)
		} else {
			r.lastSnapshotAt = statsSnap.At.UTC()
		}
	}

	st, err := r.loadEstimateState(ctx, now)
	if err != nil {
		r.log.Warn("network rates intervals unavailable", "error", err)
		r.mu.Lock()
		r.view.LastError = lastErr
		r.mu.Unlock()
		return
	}
	r.publishCollectView(st, lastErr, statsErr)
}

func (r *Runner) runSwitch(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, roundTimeout)
	defer cancel()
	now := r.now().UTC().Truncate(time.Microsecond)
	r.reconcileSwitches(ctx, now)
	st, err := r.loadEstimateState(ctx, now)
	if err != nil {
		r.log.Warn("network rates intervals unavailable", "error", err)
		return
	}
	view := r.publishSwitchView(st)
	r.maybeSwitch(ctx, now, view, st.cands, st.rec, st.current, st.currentOK, st.downloaded, st.modelsUnavailable, st.autoSwitch, st.autoSince, st.lastSwitchAt, st.backoffUntil, st.samples)
	r.mu.Lock()
	r.refreshSwitchGateLocked(now)
	r.mu.Unlock()
}

func (r *Runner) loadEstimateState(ctx context.Context, now time.Time) (estimateState, error) {
	intervals, err := r.store.NetRateIntervals(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return estimateState{}, err
	}
	downloaded, modelsUnavailable := r.downloadedModels(ctx)
	r.mu.RLock()
	networkRatio := r.networkRatio
	r.mu.RUnlock()
	cands, samples := Estimate(EstimateInput{
		Intervals: intervals, Now: now, Window: r.cfg.WindowSamples, LongWindow: longWindow,
		Prices: r.priceMap(), FallbackModel: r.cfg.FallbackModel, Downloaded: downloaded,
		MinProviders: r.cfg.MinProviders, TierScope: len(r.cfg.ChipTiers) > 0,
		OwnRatios: r.ownRatios(ctx, now), NetworkRatio: networkRatio,
	})
	rec := Recommend(cands, samples, r.cfg.WindowSamples, r.cfg.NearZeroMicroUSDPerHour, r.cfg.FallbackModel)
	current, currentOK := r.readCurrentModel(ctx, now)
	autoSwitch, autoSince, lastSwitchAt, backoffUntil, _ := r.settings.Snapshot()
	return estimateState{
		now: now, cands: cands, samples: samples, rec: rec, current: current, currentOK: currentOK,
		downloaded: downloaded, modelsUnavailable: modelsUnavailable,
		controlBusy: r.control != nil && r.control.Busy(),
		autoSwitch:  autoSwitch, autoSince: autoSince, lastSwitchAt: lastSwitchAt, backoffUntil: backoffUntil,
	}, nil
}

func (st estimateState) gateInput(r *Runner) switchInput {
	return switchInput{
		now: st.now, autoSwitch: st.autoSwitch, autoSince: st.autoSince, lastSwitchAt: st.lastSwitchAt, backoffUntil: st.backoffUntil,
		samples: st.samples, window: r.cfg.WindowSamples, minInterval: r.minSwitch,
		controlBusy: st.controlBusy, modelsUnavailable: st.modelsUnavailable, currentModel: st.current, currentOK: st.currentOK,
		lastModelChange: r.lastModelChange(), ratio: r.cfg.SwitchAdvantageRatio, nearZeroMicro: r.cfg.NearZeroMicroUSDPerHour,
		fallbackModel: r.cfg.FallbackModel, recommendation: st.rec, candidates: st.cands, downloaded: st.downloaded,
	}
}

func (r *Runner) applyEstimateViewLocked(st estimateState) {
	r.view.Enabled = true
	r.view.AutoSwitch = st.autoSwitch
	r.view.AutoSwitchSince = formatTimePtr(st.autoSince)
	r.view.WindowSamples = r.cfg.WindowSamples
	r.view.IntervalMinutes = r.cfg.IntervalMinutes
	r.view.LongWindowMinutes = int(longWindow / time.Minute)
	r.view.SwitchAdvantageRatio = r.cfg.SwitchAdvantageRatio
	r.view.ChipTiers = append([]string{}, r.cfg.ChipTiers...)
	r.view.MemoryGB = r.cfg.MemoryGB
	r.view.Samples = st.samples
	r.view.UpdatedAt = formatTime(st.now)
	r.view.CurrentModel = optionalString(st.current, st.currentOK)
	r.view.Recommendation = recommendationView(st.rec)
	r.view.Candidates = candidateViews(st.cands)
	r.view.Switch = r.evaluateSwitchGate(st.gateInput(r))
}

func (r *Runner) publishCollectView(st estimateState, lastErr *string, statsErr error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applyEstimateViewLocked(st)
	r.view.LastError = lastErr
	r.view.SnapshotAt = formatTimePtr(r.snapshotTime(statsErr))
}

func (r *Runner) publishSwitchView(st estimateState) StatusView {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applyEstimateViewLocked(st)
	return cloneStatus(r.view)
}

func (r *Runner) priceMap() map[string]Price {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.prices) == 0 {
		return nil
	}
	out := make(map[string]Price, len(r.prices))
	for k, v := range r.prices {
		out[k] = v
	}
	return out
}

func (r *Runner) ownRatios(ctx context.Context, now time.Time) map[string]float64 {
	if r.ownTotals == nil {
		return nil
	}
	totals, err := r.ownTotals(ctx, now.Add(-ownRatioLookback))
	if err != nil {
		r.log.Warn("network rates own token totals unavailable", "error", err)
		return nil
	}
	return OwnRatios(totals)
}

func (r *Runner) persistSnapshot(ctx context.Context, snap Snapshot, now time.Time) error {
	prev, ok, err := r.store.LoadNetRateBaseline(ctx)
	if err != nil {
		return err
	}
	if ok && snap.At.Equal(prev.At) {
		return nil
	}
	var intervals []Interval
	valid := false
	if ok {
		intervals, valid = Diff(prev, snap, r.interval, r.cfg.ChipTiers)
		if !valid {
			intervals = nil
		}
	}
	return r.store.SaveNetRateRound(ctx, snap, intervals, now.Add(-7*24*time.Hour))
}

func (r *Runner) downloadedModels(ctx context.Context) (map[string]bool, bool) {
	out := map[string]bool{}
	if r.control == nil {
		return out, true
	}
	models, err := r.control.Models(ctx)
	if err != nil || models.LocalError != "" {
		return out, true
	}
	for _, m := range models.Local {
		out[m.ID] = true
	}
	return out, false
}

func (r *Runner) readCurrentModel(ctx context.Context, now time.Time) (string, bool) {
	if r.currentModel == nil {
		return "", false
	}
	model, ok := r.currentModel(ctx)
	if !ok || model == "" {
		return "", false
	}
	return model, true
}

func (r *Runner) lastModelChange() time.Time {
	if r.control == nil {
		return time.Time{}
	}
	return r.control.LastModelChange()
}

func (r *Runner) reconcileSwitches(ctx context.Context, now time.Time) {
	rows, err := r.store.NetRateSwitches(ctx, 50)
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.Status != "running" {
			continue
		}
		if row.JobID == "" {
			// Agent stopped between recording intent and submitting the job.
			if now.Sub(row.At) > 20*time.Minute {
				_ = r.store.UpdateNetRateSwitch(ctx, row.ID, "unconfirmed", "job_missing")
			}
			continue
		}
		if r.control == nil {
			_ = r.store.UpdateNetRateSwitch(ctx, row.ID, "unconfirmed", "job_missing")
			_ = r.settings.RecordFailure(now)
			continue
		}
		job, ok := r.control.Job(row.JobID)
		if !ok {
			_ = r.store.UpdateNetRateSwitch(ctx, row.ID, "unconfirmed", "job_missing")
			_ = r.settings.RecordFailure(now)
			continue
		}
		if job.Status == "running" {
			continue
		}
		errCode := job.Error
		_ = r.store.UpdateNetRateSwitch(ctx, row.ID, job.Status, errCode)
		switch job.Status {
		case "succeeded":
			_ = r.settings.ResetFailures()
		case "failed", "unconfirmed":
			_ = r.settings.RecordFailure(now)
		}
	}
}

func (r *Runner) maybeSwitch(ctx context.Context, now time.Time, view StatusView, cands []Candidate, rec *Recommendation, current string, currentOK bool, downloaded map[string]bool, modelsUnavailable, autoSwitch bool, autoSince, lastSwitchAt, backoffUntil *time.Time, samples int) {
	if view.Switch.BlockedReason != nil {
		return
	}
	decision := pickTarget(current, rec, cands, downloaded, r.cfg.FallbackModel, r.cfg.SwitchAdvantageRatio, r.cfg.NearZeroMicroUSDPerHour)
	if !decision.switchNow {
		return
	}
	from := current
	if !currentOK {
		from = ""
	}
	recordID, err := r.store.InsertNetRateSwitch(ctx, SwitchRecord{
		At: now, From: from, To: decision.target, Reason: decision.reason, Status: "running",
		Summary: buildSummary(cands, 16),
	})
	if err != nil {
		r.log.Warn("network rates switch record failed", "error", err)
		return
	}
	if err = r.settings.SetLastSwitchAt(now); err != nil {
		r.log.Warn("network rates checkpoint failed", "error", err)
	}
	reqID, err := autoRequestID(now)
	if err != nil {
		_ = r.store.UpdateNetRateSwitch(ctx, recordID, "failed", "control_unavailable")
		_ = r.settings.RecordFailure(now)
		return
	}
	if r.control == nil {
		_ = r.store.UpdateNetRateSwitch(ctx, recordID, "failed", "control_unavailable")
		_ = r.settings.RecordFailure(now)
		return
	}
	job, err := r.control.Submit(control.Request{RequestID: reqID, Action: "switch", Models: []string{decision.target}})
	if err != nil {
		code := "control_unavailable"
		var ce *control.Error
		if errors.As(err, &ce) {
			code = ce.Code
		}
		_ = r.store.UpdateNetRateSwitch(ctx, recordID, "failed", code)
		_ = r.settings.RecordFailure(now)
		return
	}
	_ = r.store.UpdateNetRateSwitchJob(ctx, recordID, job.ID)
}

type switchInput struct {
	now                                       time.Time
	autoSwitch                                bool
	autoSince, lastSwitchAt, backoffUntil     *time.Time
	samples, window                           int
	minInterval                               time.Duration
	controlBusy, modelsUnavailable, currentOK bool
	currentModel, fallbackModel               string
	lastModelChange                           time.Time
	ratio                                     float64
	nearZeroMicro                             int64
	recommendation                            *Recommendation
	candidates                                []Candidate
	downloaded                                map[string]bool
}

func (r *Runner) evaluateSwitchGate(in switchInput) SwitchGateView {
	reason := blockedReason(in)
	next := nextEligibleAt(in.autoSwitch, in.autoSince, in.lastModelChange, in.lastSwitchAt, in.minInterval, in.backoffUntil)
	var blocked *string
	if reason != "" {
		blocked = &reason
	}
	return SwitchGateView{NextEligibleAt: formatTimePtr(next), BlockedReason: blocked, BackoffUntil: formatTimePtr(in.backoffUntil)}
}

func (r *Runner) refreshSwitchGateLocked(now time.Time) {
	autoSwitch, autoSince, lastSwitchAt, backoffUntil, _ := r.settings.Snapshot()
	var current string
	var currentOK bool
	if r.view.CurrentModel != nil {
		current = *r.view.CurrentModel
		currentOK = current != ""
	}
	rec := recommendationFromView(r.view.Recommendation)
	cands := candidatesFromView(r.view.Candidates)
	downloaded := map[string]bool{}
	for _, c := range cands {
		if c.Downloaded {
			downloaded[c.Model] = true
		}
	}
	r.view.Switch = r.evaluateSwitchGate(switchInput{
		now: now, autoSwitch: autoSwitch, autoSince: autoSince, lastSwitchAt: lastSwitchAt, backoffUntil: backoffUntil,
		samples: r.view.Samples, window: r.cfg.WindowSamples, minInterval: r.minSwitch,
		controlBusy: r.control != nil && r.control.Busy(), modelsUnavailable: false, currentModel: current, currentOK: currentOK,
		lastModelChange: r.lastModelChange(), ratio: r.cfg.SwitchAdvantageRatio, nearZeroMicro: r.cfg.NearZeroMicroUSDPerHour,
		fallbackModel: r.cfg.FallbackModel, recommendation: rec, candidates: cands, downloaded: downloaded,
	})
}

func (r *Runner) snapshotTime(statsErr error) *time.Time {
	if statsErr == nil && !r.lastSnapshotAt.IsZero() {
		t := r.lastSnapshotAt
		return &t
	}
	if !r.lastSnapshotAt.IsZero() {
		t := r.lastSnapshotAt
		return &t
	}
	return nil
}

func blockedReason(in switchInput) string {
	if !in.autoSwitch {
		return "auto_switch_off"
	}
	if in.samples < in.window {
		return "insufficient_samples"
	}
	if in.autoSince != nil && in.now.Sub(*in.autoSince) < in.minInterval {
		return "warming_up"
	}
	last := in.lastModelChange
	if in.lastSwitchAt != nil && in.lastSwitchAt.After(last) {
		last = *in.lastSwitchAt
	}
	if !last.IsZero() && in.now.Sub(last) < in.minInterval {
		return "min_interval"
	}
	if in.backoffUntil != nil && in.now.Before(*in.backoffUntil) {
		return "backoff"
	}
	if in.controlBusy {
		return "control_busy"
	}
	if in.modelsUnavailable {
		return "models_unavailable"
	}
	if !in.currentOK {
		return "current_model_unknown"
	}
	decision := pickTarget(in.currentModel, in.recommendation, in.candidates, in.downloaded, in.fallbackModel, in.ratio, in.nearZeroMicro)
	if decision.blocked != "" {
		return decision.blocked
	}
	if !decision.switchNow {
		return "already_optimal"
	}
	return ""
}

type targetDecision struct {
	target, reason, blocked string
	switchNow               bool
}

func pickTarget(current string, rec *Recommendation, cands []Candidate, downloaded map[string]bool, fallback string, ratio float64, nearZero int64) targetDecision {
	eligible := eligibleCandidates(cands)
	allNearZero := len(eligible) > 0
	for _, c := range eligible {
		if c.USDPerHourMicro >= nearZero {
			allNearZero = false
			break
		}
	}
	useFallback := len(eligible) == 0 || allNearZero
	if useFallback {
		if current == fallback {
			return targetDecision{blocked: "already_optimal"}
		}
		if !downloaded[fallback] {
			return targetDecision{blocked: "fallback_not_downloaded"}
		}
		return targetDecision{target: fallback, reason: "fallback", switchNow: true}
	}
	if rec == nil {
		return targetDecision{blocked: "already_optimal"}
	}
	target := rec.Model
	if current == target {
		return targetDecision{blocked: "already_optimal"}
	}
	currentUSD := candidateUSD(cands, current)
	targetUSD := rec.USDPerHourMicro
	threshold := int64(float64(currentUSD) * (1 + ratio))
	if targetUSD < threshold {
		return targetDecision{blocked: "below_threshold"}
	}
	return targetDecision{target: target, reason: "better_model", switchNow: true}
}

func eligibleCandidates(cands []Candidate) []Candidate {
	var out []Candidate
	for _, c := range cands {
		if c.Downloaded && !c.Insufficient {
			out = append(out, c)
		}
	}
	return out
}

func candidateUSD(cands []Candidate, model string) int64 {
	for _, c := range cands {
		if c.Model == model {
			if c.Insufficient {
				return 0
			}
			return c.USDPerHourMicro
		}
	}
	return 0
}

func nextEligibleAt(autoSwitch bool, since *time.Time, lastChange time.Time, lastSwitch *time.Time, min time.Duration, backoff *time.Time) *time.Time {
	if !autoSwitch {
		return nil
	}
	var best time.Time
	if since != nil {
		best = maxTime(best, since.Add(min))
	}
	last := lastChange
	if lastSwitch != nil && lastSwitch.After(last) {
		last = *lastSwitch
	}
	if !last.IsZero() {
		best = maxTime(best, last.Add(min))
	}
	if backoff != nil {
		best = maxTime(best, *backoff)
	}
	if best.IsZero() {
		return nil
	}
	t := best.UTC()
	return &t
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func buildSummary(cands []Candidate, limit int) []SummaryItem {
	if limit < 1 {
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].USDPerHourMicro != cands[j].USDPerHourMicro {
			return cands[i].USDPerHourMicro > cands[j].USDPerHourMicro
		}
		return cands[i].Model < cands[j].Model
	})
	if len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]SummaryItem, len(cands))
	for i, c := range cands {
		out[i] = SummaryItem{Model: c.Model, USDPerHourMicro: c.USDPerHourMicro, AvgProviders: c.AvgProviders}
	}
	return out
}

func autoRequestID(now time.Time) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("auto-%d-%s", now.UnixNano(), hex.EncodeToString(buf)), nil
}

func candidateViews(cands []Candidate) []CandidateView {
	out := make([]CandidateView, len(cands))
	for i, c := range cands {
		out[i] = CandidateView{
			Model: c.Model, Downloaded: c.Downloaded, AvgProviders: c.AvgProviders, BusyRatio: c.BusyRatio,
			RequestsPerHour: c.RequestsPerHour, TokensPerHour: c.TokensPerHour,
			USDPerHourMicro: strconv.FormatInt(c.USDPerHourMicro, 10), TrimmedUSDPerHourMicro: microString(c.TrimmedUSDPerHourMicro),
			Hour: hourView(c.Hour), PriceSource: c.PriceSource, EffectivePriceMicro: strconv.FormatInt(c.EffectivePriceMicro, 10),
			InputRatio: c.InputRatio, RatioSource: c.RatioSource, Cohort: c.Cohort, Insufficient: c.Insufficient,
		}
	}
	return out
}

func hourView(w *WindowStats) *CandidateHourView {
	if w == nil {
		return nil
	}
	return &CandidateHourView{
		Samples: w.Samples, AvgProviders: w.AvgProviders, BusyRatio: w.BusyRatio,
		RequestsPerHour: w.RequestsPerHour, TokensPerHour: w.TokensPerHour,
		USDPerHourMicro: strconv.FormatInt(w.USDPerHourMicro, 10), TrimmedUSDPerHourMicro: microString(w.TrimmedUSDPerHourMicro),
	}
}

func microString(v *int64) *string {
	if v == nil {
		return nil
	}
	s := strconv.FormatInt(*v, 10)
	return &s
}

func recommendationView(rec *Recommendation) *RecommendationView {
	if rec == nil {
		return nil
	}
	return &RecommendationView{Model: rec.Model, USDPerHourMicro: strconv.FormatInt(rec.USDPerHourMicro, 10), Fallback: rec.Fallback}
}

func switchToView(row SwitchRecord) SwitchItemView {
	return SwitchItemView{
		ID: row.ID, At: row.At.UTC().Format(time.RFC3339Nano), FromModel: row.From, ToModel: row.To,
		Reason: row.Reason, Status: row.Status, Error: optionalString(row.Error, row.Error != ""),
		JobID: row.JobID, Summary: summaryViews(row.Summary),
	}
}

func summaryViews(items []SummaryItem) []SummaryView {
	out := make([]SummaryView, len(items))
	for i, it := range items {
		out[i] = SummaryView{Model: it.Model, USDPerHourMicro: strconv.FormatInt(it.USDPerHourMicro, 10), AvgProviders: it.AvgProviders}
	}
	return out
}

func formatTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s
}

func formatTimePtr(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	return formatTime(*t)
}

func optionalString(v string, ok bool) *string {
	if !ok || v == "" {
		return nil
	}
	s := v
	return &s
}

func cloneStatus(v StatusView) StatusView {
	cp := v
	cp.Candidates = append([]CandidateView{}, v.Candidates...)
	cp.ChipTiers = append([]string{}, v.ChipTiers...)
	if v.Recommendation != nil {
		rec := *v.Recommendation
		cp.Recommendation = &rec
	}
	return cp
}

func recommendationFromView(v *RecommendationView) *Recommendation {
	if v == nil {
		return nil
	}
	usd, _ := strconv.ParseInt(v.USDPerHourMicro, 10, 64)
	return &Recommendation{Model: v.Model, USDPerHourMicro: usd, Fallback: v.Fallback}
}

func candidatesFromView(rows []CandidateView) []Candidate {
	out := make([]Candidate, len(rows))
	for i, c := range rows {
		usd, _ := strconv.ParseInt(c.USDPerHourMicro, 10, 64)
		out[i] = Candidate{
			Model: c.Model, Downloaded: c.Downloaded, AvgProviders: c.AvgProviders,
			RequestsPerHour: c.RequestsPerHour, TokensPerHour: c.TokensPerHour,
			USDPerHourMicro: usd, PriceSource: c.PriceSource, Insufficient: c.Insufficient,
		}
	}
	return out
}
