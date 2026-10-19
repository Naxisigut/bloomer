package service

import (
	"context"
	"time"

	"bloomer/agent/internal/store"
	"bloomer/agent/internal/telemetry"
	"bloomer/agent/internal/upstream"
)

func deviceEarningsSource(db *store.Store, client *upstream.IdentityClient) func(context.Context, string, time.Time) (telemetry.EarningsObservation, error) {
	return func(ctx context.Context, key string, now time.Time) (telemetry.EarningsObservation, error) {
		ids, err := client.ProviderIDs(ctx, key, now)
		if err != nil {
			return telemetry.EarningsObservation{}, err
		}
		return db.DeviceEarnings(ctx, key, ids, now)
	}
}
