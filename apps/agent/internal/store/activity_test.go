package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestDeviceEarningsExcludeOtherDevicesAccountsAndRetainVerifiedSessions(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	err := s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE maintenance_state SET bound_account_id='account'; INSERT INTO sync_state(account_id,last_successful_sync_at_us) VALUES('account',?)`, now.UnixMicro()); err != nil {
			return err
		}
		for i, row := range []struct {
			account, provider string
			at                time.Time
		}{{"account", "own-old", now.Add(-10 * time.Minute)}, {"account", "other", now}, {"different-account", "own-old", now}} {
			if _, err := tx.Exec(`INSERT INTO earnings VALUES(?,?,?,?, 'job','model',1,1,1,?)`, row.account, string(rune('a'+i)), row.provider, "key", row.at.UnixMicro()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.DeviceEarnings(ctx, "local-key", []string{"own-old"}, now)
	if err != nil || v.LastEarningAt == nil || !v.LastEarningAt.Equal(now.Add(-10*time.Minute)) || v.ProviderID != "own-old" {
		t.Fatalf("another device/account contaminated result: %+v %v", v, err)
	}
	v, err = s.DeviceEarnings(ctx, "local-key", []string{"own-new"}, now)
	if err != nil || v.LastEarningAt == nil || v.ProviderID != "own-old" {
		t.Fatal("reconnect lost verified earnings")
	}
	v, err = s.DeviceEarnings(ctx, "replacement-key", []string{"unseen"}, now)
	if err != nil || v.LastEarningAt != nil {
		t.Fatal("identity change reused another identity")
	}
}
