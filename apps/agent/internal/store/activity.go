package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/telemetry"
)

func (s *Store) DeviceEarnings(ctx context.Context, key string, ids []string, now time.Time) (telemetry.EarningsObservation, error) {
	var out telemetry.EarningsObservation
	if key == "" || len(ids) == 0 {
		return out, errors.New("device identity unavailable")
	}
	digest := sha256.Sum256([]byte(key))
	hash := hex.EncodeToString(digest[:])
	// Cache the verified association, not the public feed or the attestation key.
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if id == "" {
				return errors.New("invalid provider identity")
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO device_provider_ids(identity_hash,provider_id) VALUES(?,?)", hash, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return out, err
	}
	err := s.Query(ctx, "", now, func(v *ReadView) error {
		out.LastSyncAt = v.State.LastSuccess
		var at int64
		err := v.tx.QueryRowContext(ctx, `SELECT e.provider_id,e.created_at_us FROM device_provider_ids p
   JOIN earnings e ON e.provider_id=p.provider_id AND e.account_id=?
   WHERE p.identity_hash=? AND e.created_at_us>=?
   ORDER BY e.created_at_us DESC,e.record_id DESC LIMIT 1`, v.State.AccountID, hash, now.Add(-config.Retention).UnixMicro()).Scan(&out.ProviderID, &at)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out.LastEarningAt = telemetry.Ptr(time.UnixMicro(at).UTC())
		return nil
	})
	return out, err
}
