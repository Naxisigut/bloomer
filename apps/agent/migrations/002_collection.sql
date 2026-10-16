-- Process-wide state is required even before the first account snapshot succeeds.
ALTER TABLE maintenance_state ADD COLUMN bound_account_id TEXT;
ALTER TABLE maintenance_state ADD COLUMN last_attempt_at_us INTEGER;
ALTER TABLE maintenance_state ADD COLUMN last_error_code TEXT;
ALTER TABLE maintenance_state ADD COLUMN last_sync_mode TEXT;
ALTER TABLE maintenance_state ADD COLUMN recovery_reason TEXT;
ALTER TABLE maintenance_state ADD COLUMN clock_high_water_us INTEGER;
ALTER TABLE maintenance_state ADD COLUMN cleanup_cutoff_us INTEGER;
ALTER TABLE maintenance_state ADD COLUMN last_cleanup_attempt_at_us INTEGER;
ALTER TABLE maintenance_state ADD COLUMN cleanup_error_code TEXT;
CREATE INDEX earnings_retention_time ON earnings(created_at_us);
