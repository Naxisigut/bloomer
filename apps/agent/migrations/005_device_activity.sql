-- Keep old routing history on disk for upgrade compatibility, but stop using it.
ALTER TABLE device_state_samples ADD COLUMN inference_active INTEGER CHECK(inference_active IN (0,1));
ALTER TABLE device_state_samples ADD COLUMN request_work_pending INTEGER CHECK(request_work_pending IN (0,1));
ALTER TABLE device_state_samples ADD COLUMN is_idle INTEGER CHECK(is_idle IN (0,1));
ALTER TABLE device_state_samples ADD COLUMN last_device_earning_at_us INTEGER;
ALTER TABLE device_state_samples ADD COLUMN last_earnings_sync_at_us INTEGER;
ALTER TABLE device_state_samples ADD COLUMN earnings_provider_id TEXT;
ALTER TABLE device_state_samples ADD COLUMN activity_error TEXT;
CREATE INDEX earnings_provider_time ON earnings(account_id, provider_id, created_at_us DESC, record_id DESC);
-- Remember only IDs proven to belong to the same attestation identity, across reconnects.
CREATE TABLE device_provider_ids (
    identity_hash TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    PRIMARY KEY(identity_hash, provider_id)
) STRICT, WITHOUT ROWID;
