-- Keep only the newest telemetry row per installation; dashboard reads latest state only.
DELETE FROM device_state_samples WHERE id NOT IN (
  SELECT id FROM device_state_samples ORDER BY collected_at_us DESC, id DESC LIMIT 1
);
CREATE UNIQUE INDEX IF NOT EXISTS device_state_one_per_device ON device_state_samples(device_id);
