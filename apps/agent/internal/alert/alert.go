// Package alert evaluates telemetry without changing its measurement semantics.
// Rule state and a delivery outbox commit atomically before any network I/O.
package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"
	_ "time/tzdata"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/store"
	"bloomer/agent/internal/telemetry"
)

var ErrDisabled = errors.New("alerts disabled")
var shanghai, _ = time.LoadLocation("Asia/Shanghai")
var ruleTypes = []string{"coordinator_disconnected", "connection_flapping", "provider_not_running", "model_not_ready", "no_earnings", "provider_state_unknown"}
var labels = map[string]string{"coordinator_disconnected": "协调器连接中断", "connection_flapping": "连接频繁重连", "provider_not_running": "Provider 进程停止", "model_not_ready": "模型未就绪", "no_earnings": "设备长时间无收益", "provider_state_unknown": "Provider 状态未知", "test": "测试推送"}

type Record struct {
	ID               int64      `json:"id"`
	Type             string     `json:"type"`
	Event            string     `json:"event"`
	Level            string     `json:"level"`
	DeviceName       string     `json:"device_name"`
	Title            string     `json:"title"`
	Body             string     `json:"body"`
	CreatedAt        time.Time  `json:"created_at"`
	FirstTriggeredAt time.Time  `json:"first_triggered_at"`
	LastPushAt       *time.Time `json:"last_push_at"`
	Recovered        bool       `json:"recovered"`
	DurationSeconds  int64      `json:"duration_seconds"`
	DeliveryStatus   string     `json:"delivery_status"`
	Attempts         int        `json:"attempts"`
	LastAttemptAt    *time.Time `json:"last_attempt_at"`
	NextAttemptAt    *time.Time `json:"next_attempt_at"`
	ErrorCode        string     `json:"error_code,omitempty"`
}
type ruleState struct {
	Active           bool       `json:"active"`
	Recovered        bool       `json:"recovered"`
	Count            int        `json:"count"`
	FirstTriggeredAt time.Time  `json:"first_triggered_at"`
	LastQueuedAt     time.Time  `json:"last_queued_at"`
	LastPushAt       *time.Time `json:"last_push_at"`
}
type engineState struct {
	Rules        map[string]*ruleState `json:"rules"`
	LastSampleAt time.Time             `json:"last_sample_at"`
	LastSession  string                `json:"last_session"`
	Changes      []time.Time           `json:"changes"`
}
type Sender interface {
	Send(context.Context, Record) error
}
type Manager struct {
	db         *store.Store
	policy     config.Alert
	logger     *slog.Logger
	sender     Sender
	now        func() time.Time
	wake       chan struct{}
	dispatchMu sync.Mutex
}

func New(db *store.Store, policy config.Alert, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if policy.DeviceName == "" {
		policy.DeviceName, _ = os.Hostname()
		if policy.DeviceName == "" {
			policy.DeviceName = "本机设备"
		}
	}
	return &Manager{db: db, policy: policy, logger: logger, sender: newBark(policy, logger), now: time.Now, wake: make(chan struct{}, 1)}
}
func loadState(ctx context.Context, tx *sql.Tx) (engineState, error) {
	state := engineState{Rules: map[string]*ruleState{}}
	var body string
	err := tx.QueryRowContext(ctx, "SELECT state_json FROM alert_state WHERE id=1").Scan(&body)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(body), &state); err != nil {
			return state, err
		}
	}
	if state.Rules == nil {
		state.Rules = map[string]*ruleState{}
	}
	return state, nil
}
func saveState(ctx context.Context, tx *sql.Tx, state engineState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO alert_state VALUES(1,?) ON CONFLICT(id) DO UPDATE SET state_json=excluded.state_json", string(body))
	return err
}
func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

type verdict struct {
	known, bad bool
	samples    int
	detail     string
}

func known(bad bool, samples int, detail string) verdict { return verdict{true, bad, samples, detail} }
func (m *Manager) Evaluate(ctx context.Context, s telemetry.Sample) error {
	if !m.policy.Enabled {
		return nil
	}
	err := m.db.Write(ctx, func(tx *sql.Tx) error {
		state, err := loadState(ctx, tx)
		if err != nil {
			return err
		}
		// Duplicate/out-of-order observations must not advance a consecutive counter.
		if !s.CollectedAt.After(state.LastSampleAt) {
			return nil
		}
		if s.CollectedAt.Sub(state.LastSampleAt) > 2*telemetry.Interval {
			for _, r := range state.Rules {
				r.Count = 0
			}
		}
		state.LastSampleAt = s.CollectedAt
		verdicts := m.verdicts(s, &state)
		for _, kind := range ruleTypes {
			v := verdicts[kind]
			r := state.Rules[kind]
			if r == nil {
				r = &ruleState{}
				state.Rules[kind] = r
			}
			if !v.known {
				r.Count = 0
				continue
			} // Unknown evidence neither triggers nor recovers.
			event := ""
			if v.bad {
				if r.Count < v.samples {
					r.Count++
				}
				if !r.Active && r.Count >= v.samples {
					r.Active = true
					r.Recovered = false
					r.FirstTriggeredAt = s.CollectedAt
					r.LastPushAt = nil
					event = "triggered"
				} else if r.Active && s.CollectedAt.Sub(r.LastQueuedAt) >= time.Duration(m.policy.RepeatMinutes)*time.Minute {
					event = "reminder"
				}
			} else {
				r.Count = 0
				if r.Active {
					r.Active = false
					r.Recovered = true
					event = "recovered"
				}
			}
			if event != "" {
				record := m.record(kind, event, s.CollectedAt, r.FirstTriggeredAt, v.detail)
				record.LastPushAt = r.LastPushAt
				if err = m.enqueue(ctx, tx, &record); err != nil {
					return err
				}
				r.LastQueuedAt = s.CollectedAt
			}
		}
		return saveState(ctx, tx, state)
	})
	if err == nil {
		m.signal()
	}
	return err
}
func (m *Manager) verdicts(s telemetry.Sample, state *engineState) map[string]verdict {
	p := m.policy
	n := p.ConsecutiveSamples
	v := map[string]verdict{}
	runningKnown := s.ProviderRunning != nil && s.Quality["provider_running"] == telemetry.OK
	running := runningKnown && *s.ProviderRunning
	fresh := s.ProviderStateAt != nil && s.CollectedAt.Sub(*s.ProviderStateAt) >= -5*time.Second && s.CollectedAt.Sub(*s.ProviderStateAt) <= 90*time.Second
	v["provider_not_running"] = verdict{known: runningKnown, bad: !running, samples: n, detail: "已确认 Provider 进程未运行"}
	if running {
		v["provider_not_running"] = known(false, n, "Provider 进程已运行")
	}
	v["provider_state_unknown"] = known(!runningKnown || (running && !fresh), n, "状态文件缺失、无效或超过 90 秒未更新，连接状态未知")
	if runningKnown && !running {
		v["provider_state_unknown"] = known(false, n, "Provider 进程状态已确认")
	}
	connectedKnown := running && fresh && s.TrustReceivedAt != nil && s.Quality["trust_received_at"] == telemetry.OK
	age := time.Duration(0)
	if connectedKnown {
		value := *s.TrustReceivedAt
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > float64(s.CollectedAt.Unix())+5 {
			connectedKnown = false
		} else {
			age = s.CollectedAt.Sub(time.Unix(int64(value), int64((value-math.Floor(value))*1e9)))
		}
	}
	if running && fresh && !connectedKnown {
		v["provider_state_unknown"] = known(true, n, "授权心跳缺失或无效，连接状态未知")
	}
	if connectedKnown {
		v["provider_state_unknown"] = known(false, n, "状态文件新鲜，授权心跳时间已可读取")
	}
	connected := connectedKnown && age <= time.Duration(p.DisconnectSeconds)*time.Second
	if connectedKnown {
		v["coordinator_disconnected"] = known(!connected, n, "授权心跳已 "+duration(age)+"未更新")
	}
	if running && fresh && s.ModelReady != nil && s.Quality["model_ready"] == telemetry.OK {
		v["model_not_ready"] = known(!*s.ModelReady, n, "当前模型未在本机完成就绪")
		if *s.ModelReady {
			v["model_not_ready"] = known(false, n, "当前模型已在本机完成就绪")
		}
	}
	changes := state.Changes[:0]
	for _, at := range state.Changes {
		if at.After(s.CollectedAt.Add(-time.Duration(p.FlapWindowMinutes)*time.Minute)) && !at.After(s.CollectedAt) {
			changes = append(changes, at)
		}
	}
	state.Changes = changes
	if running && fresh && s.SessionID != nil && *s.SessionID != "" && s.Quality["session_id"] == telemetry.OK {
		if state.LastSession != "" && state.LastSession != *s.SessionID {
			state.Changes = append(state.Changes, s.CollectedAt)
		}
		state.LastSession = *s.SessionID
		v["connection_flapping"] = known(len(state.Changes) >= p.FlapChanges, 1, fmt.Sprintf("过去 %d 分钟会话变化 %d 次", p.FlapWindowMinutes, len(state.Changes)))
	}
	if connected && s.LastDeviceEarningAt != nil && !s.LastDeviceEarningAt.After(s.CollectedAt) {
		threshold := p.NoEarningsMinutes
		hour := s.CollectedAt.In(shanghai).Hour()
		night := hour >= p.NightStartHour && hour < p.NightEndHour
		if p.NightStartHour > p.NightEndHour {
			night = hour >= p.NightStartHour || hour < p.NightEndHour
		}
		if night {
			threshold = p.NightNoEarningsMinutes
		}
		gap := s.CollectedAt.Sub(*s.LastDeviceEarningAt)
		v["no_earnings"] = known(gap > time.Duration(threshold)*time.Minute, 1, fmt.Sprintf("本机设备已有 %s无收益，当前阈值 %d 分钟", duration(gap), threshold))
	}
	return v
}
func duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	minutes := int64(d / time.Minute)
	if minutes >= 60 {
		return fmt.Sprintf("%d 小时 %d 分钟", minutes/60, minutes%60)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	return fmt.Sprintf("%d 秒", int64(d/time.Second))
}
func (m *Manager) record(kind, event string, at, start time.Time, detail string) Record {
	level := "critical"
	if kind == "connection_flapping" || kind == "no_earnings" || kind == "test" || event == "recovered" {
		level = "active"
	}
	label := labels[kind]
	if event == "recovered" {
		label += "已恢复"
		detail = "已恢复，持续 " + duration(at.Sub(start)) + "。" + detail
	}
	if event == "reminder" {
		label += "（持续告警）"
		detail = "异常已持续 " + duration(at.Sub(start)) + "。" + detail
	}
	return Record{Type: kind, Event: event, Level: level, DeviceName: m.policy.DeviceName, Title: m.policy.DeviceName + " · " + label, Body: fmt.Sprintf("设备：%s\n告警：%s\n开始时间：%s（北京时间）\n%s", m.policy.DeviceName, labels[kind], start.In(shanghai).Format("2006-01-02 15:04:05"), detail), CreatedAt: at, FirstTriggeredAt: start, Recovered: event == "recovered", DurationSeconds: max(0, int64(at.Sub(start)/time.Second)), DeliveryStatus: "pending"}
}
func (m *Manager) enqueue(ctx context.Context, tx *sql.Tx, r *Record) error {
	// A recovery supersedes undelivered triggers, so a delayed retry cannot
	// announce an old outage after its recovery. History still explains the result.
	if r.Type != "test" {
		rows, err := tx.QueryContext(ctx, "SELECT record_json FROM alert_outbox WHERE alert_type=?", r.Type)
		if err != nil {
			return err
		}
		var pending []Record
		for rows.Next() {
			var body string
			var old Record
			if err = rows.Scan(&body); err != nil {
				rows.Close()
				return err
			}
			if err = json.Unmarshal([]byte(body), &old); err != nil {
				rows.Close()
				return err
			}
			pending = append(pending, old)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, old := range pending {
			old.DeliveryStatus = "superseded"
			old.NextAttemptAt = nil
			body, err := json.Marshal(old)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE alert_records SET record_json=? WHERE id=?", string(body), old.ID); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM alert_outbox WHERE alert_type=?", r.Type); err != nil {
			return err
		}
	}
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO alert_records(record_json) VALUES(?)", string(body))
	if err != nil {
		return err
	}
	r.ID, err = result.LastInsertId()
	if err != nil {
		return err
	}
	body, err = json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE alert_records SET record_json=? WHERE id=?", string(body), r.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO alert_outbox(id,alert_type,record_json,next_attempt_at_us) VALUES(?,?,?,?)", r.ID, r.Type, string(body), r.CreatedAt.UnixMicro()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM alert_records WHERE id NOT IN (SELECT id FROM alert_records ORDER BY id DESC LIMIT 50)")
	return err
}
func (m *Manager) Test(ctx context.Context) (Record, error) {
	if !m.policy.Enabled {
		return Record{}, ErrDisabled
	}
	at := m.now().UTC()
	r := m.record("test", "test", at, at, "Bark 测试推送，设备告警通道已接入。")
	err := m.db.Write(ctx, func(tx *sql.Tx) error { return m.enqueue(ctx, tx, &r) })
	if err == nil {
		m.signal()
	}
	return r, err
}
func (m *Manager) Records(ctx context.Context) ([]Record, error) { return Records(ctx, m.db) }
func Records(ctx context.Context, db *store.Store) ([]Record, error) {
	out := make([]Record, 0)
	rows, err := db.ReadDB().QueryContext(ctx, "SELECT record_json FROM alert_records ORDER BY id DESC LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var r Record
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(body), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
