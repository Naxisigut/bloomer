CREATE TABLE account_summary_current (
    account_id TEXT PRIMARY KEY CHECK(length(account_id) > 0),
    total_record_count INTEGER NOT NULL CHECK(total_record_count >= 0),
    total_micro_usd INTEGER NOT NULL,
    available_balance_micro_usd INTEGER NOT NULL,
    withdrawable_balance_micro_usd INTEGER NOT NULL,
    observed_at_us INTEGER NOT NULL
) STRICT;

CREATE TABLE earnings (
    account_id TEXT NOT NULL CHECK(length(account_id) > 0),
    record_id TEXT NOT NULL CHECK(length(record_id) > 0),
    provider_id TEXT NOT NULL,
    provider_key TEXT NOT NULL,
    job_id TEXT NOT NULL,
    model TEXT NOT NULL CHECK(length(model) > 0),
    amount_micro_usd INTEGER NOT NULL,
    prompt_tokens INTEGER NOT NULL CHECK(prompt_tokens >= 0),
    completion_tokens INTEGER NOT NULL CHECK(completion_tokens >= 0),
    created_at_us INTEGER NOT NULL,
    PRIMARY KEY(account_id, record_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX earnings_time ON earnings(account_id, created_at_us DESC, record_id DESC);
CREATE INDEX earnings_model_time ON earnings(account_id, model, created_at_us DESC, record_id DESC);

CREATE TABLE sync_state (
    account_id TEXT PRIMARY KEY CHECK(length(account_id) > 0),
    last_attempt_at_us INTEGER,
    last_successful_sync_at_us INTEGER,
    last_error_code TEXT,
    last_sync_mode TEXT CHECK(last_sync_mode IN ('initial', 'normal', 'recovery')),
    coverage_start_us INTEGER,
    coverage_status TEXT NOT NULL DEFAULT 'uninitialized'
        CHECK(coverage_status IN ('uninitialized', 'partial', 'possible_gap')),
    last_new_count INTEGER NOT NULL DEFAULT 0 CHECK(last_new_count >= 0),
    last_updated_count INTEGER NOT NULL DEFAULT 0 CHECK(last_updated_count >= 0)
) STRICT;

CREATE TABLE coverage_gaps (
    id INTEGER PRIMARY KEY,
    account_id TEXT NOT NULL CHECK(length(account_id) > 0),
    discovered_at_us INTEGER NOT NULL,
    interruption_start_us INTEGER NOT NULL,
    interruption_end_us INTEGER NOT NULL,
    reason TEXT NOT NULL,
    CHECK(interruption_end_us >= interruption_start_us)
) STRICT;
CREATE INDEX coverage_gaps_account_time ON coverage_gaps(account_id, interruption_start_us);

CREATE TABLE maintenance_state (
    id INTEGER PRIMARY KEY CHECK(id = 1),
    last_started_at_us INTEGER,
    last_clean_shutdown_at_us INTEGER,
    last_cleanup_success_at_us INTEGER,
    retry_not_before_us INTEGER
) STRICT;
INSERT INTO maintenance_state(id) VALUES(1);
