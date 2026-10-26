ALTER TABLE network_rate_intervals ADD COLUMN scope TEXT NOT NULL DEFAULT '' CHECK(scope IN ('', 'tier'));
ALTER TABLE network_rate_intervals ADD COLUMN busy_providers INTEGER NOT NULL DEFAULT -1 CHECK(busy_providers >= -1);
ALTER TABLE network_rate_intervals ADD COLUMN max_provider_tokens INTEGER NOT NULL DEFAULT -1 CHECK(max_provider_tokens >= -1);
