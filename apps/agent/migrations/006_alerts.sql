ALTER TABLE device_state_samples ADD COLUMN trust_received_at REAL;
ALTER TABLE device_state_samples ADD COLUMN session_id TEXT;

-- Policy state and delivery outbox survive pruning of the visible 50 records.
CREATE TABLE alert_state (
 id INTEGER PRIMARY KEY CHECK(id=1), state_json TEXT NOT NULL CHECK(json_valid(state_json))
) STRICT;
CREATE TABLE alert_records (
 id INTEGER PRIMARY KEY AUTOINCREMENT, record_json TEXT NOT NULL CHECK(json_valid(record_json))
) STRICT;
CREATE TABLE alert_outbox (
 id INTEGER PRIMARY KEY,
 alert_type TEXT NOT NULL, record_json TEXT NOT NULL CHECK(json_valid(record_json)),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 4),
 next_attempt_at_us INTEGER NOT NULL
) STRICT;
