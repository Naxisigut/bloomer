package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var retryDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

// Run delivers independently of collection. A restart resumes the persisted due time.
func (m *Manager) Run(ctx context.Context) {
	if !m.policy.Enabled {
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		for ctx.Err() == nil {
			sent, err := m.dispatch(ctx)
			if err != nil {
				if ctx.Err() == nil {
					m.logger.Error("alert delivery checkpoint failed")
				}
				break
			}
			if !sent {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-m.wake:
		}
	}
}
func (m *Manager) dispatch(ctx context.Context) (bool, error) {
	m.dispatchMu.Lock()
	defer m.dispatchMu.Unlock()
	at := m.now().UTC()
	var body string
	var attempts int
	err := m.db.ReadDB().QueryRowContext(ctx, "SELECT record_json,attempts FROM alert_outbox WHERE next_attempt_at_us<=? ORDER BY id LIMIT 1", at.UnixMicro()).Scan(&body, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var r Record
	if err = json.Unmarshal([]byte(body), &r); err != nil {
		return false, err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	sendErr := m.sender.Send(sendCtx, r)
	cancel()
	if ctx.Err() != nil {
		return false, ctx.Err()
	} // Keep the outbox intact on shutdown.
	at = m.now().UTC()
	r.Attempts = attempts + 1
	r.LastAttemptAt = &at
	r.NextAttemptAt = nil
	r.ErrorCode = ""
	if sendErr == nil {
		r.DeliveryStatus = "sent"
		r.LastPushAt = &at
	} else {
		r.ErrorCode = "bark_send_failed"
		r.DeliveryStatus = "failed"
		if attempts < len(retryDelays) {
			next := at.Add(retryDelays[attempts])
			r.NextAttemptAt = &next
			r.DeliveryStatus = "retrying"
		}
	}
	err = m.db.Write(ctx, func(tx *sql.Tx) error {
		encoded, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE alert_records SET record_json=? WHERE id=?", string(encoded), r.ID); err != nil {
			return err
		}
		if r.NextAttemptAt != nil {
			_, err = tx.ExecContext(ctx, "UPDATE alert_outbox SET record_json=?,attempts=?,next_attempt_at_us=? WHERE id=?", string(encoded), r.Attempts, r.NextAttemptAt.UnixMicro(), r.ID)
		} else {
			_, err = tx.ExecContext(ctx, "DELETE FROM alert_outbox WHERE id=?", r.ID)
		}
		if err != nil {
			return err
		}
		if sendErr == nil && r.Type != "test" {
			state, err := loadState(ctx, tx)
			if err != nil {
				return err
			}
			if rule := state.Rules[r.Type]; rule != nil && rule.FirstTriggeredAt.Equal(r.FirstTriggeredAt) {
				rule.LastPushAt = &at
			}
			return saveState(ctx, tx, state)
		}
		return nil
	})
	if err == nil {
		// Error text and remote response bodies may contain the device key. Log only fixed fields.
		if sendErr == nil {
			m.logger.Info("Bark push sent", "alert_type", r.Type, "record_id", r.ID, "attempt", r.Attempts)
		} else {
			m.logger.Warn("Bark push failed", "alert_type", r.Type, "record_id", r.ID, "attempt", r.Attempts, "status", r.DeliveryStatus)
		}
	}
	return true, err
}
