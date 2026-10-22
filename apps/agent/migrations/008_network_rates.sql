CREATE TABLE network_rate_baseline (
 provider_id TEXT PRIMARY KEY CHECK(length(provider_id) > 0),
 model TEXT NOT NULL CHECK(length(model) > 0),
 requests INTEGER NOT NULL CHECK(requests >= 0),
 tokens INTEGER NOT NULL CHECK(tokens >= 0)
) STRICT;

CREATE TABLE network_rate_state (
 id INTEGER PRIMARY KEY CHECK(id = 1),
 snapshot_at_us INTEGER NOT NULL
) STRICT;

CREATE TABLE network_rate_intervals (
 id INTEGER PRIMARY KEY,
 started_at_us INTEGER NOT NULL,
 ended_at_us INTEGER NOT NULL,
 model TEXT NOT NULL CHECK(length(model) > 0),
 providers INTEGER NOT NULL CHECK(providers >= 0),
 delta_requests INTEGER NOT NULL CHECK(delta_requests >= 0),
 delta_tokens INTEGER NOT NULL CHECK(delta_tokens >= 0)
) STRICT;
CREATE INDEX network_rate_intervals_ended ON network_rate_intervals(ended_at_us);

CREATE TABLE network_rate_switches (
 id INTEGER PRIMARY KEY,
 at_us INTEGER NOT NULL,
 from_model TEXT NOT NULL,
 to_model TEXT NOT NULL,
 reason TEXT NOT NULL,
 status TEXT NOT NULL,
 error TEXT,
 job_id TEXT,
 summary_json TEXT NOT NULL CHECK(json_valid(summary_json))
) STRICT;
