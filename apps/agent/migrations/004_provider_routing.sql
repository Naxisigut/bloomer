-- Preserve schema-3 observations as unknown: no backfilled routing verdicts.
ALTER TABLE device_state_samples ADD COLUMN routing_state TEXT CHECK(routing_state IN ('routable','degraded','blocked','offline'));
ALTER TABLE device_state_samples ADD COLUMN routing_priority TEXT CHECK(routing_priority IN ('normal','reduced','none'));
ALTER TABLE device_state_samples ADD COLUMN routing_reasons_json TEXT;
ALTER TABLE device_state_samples ADD COLUMN routing_observed_at_us INTEGER;
ALTER TABLE device_state_samples ADD COLUMN routing_provider_id TEXT;
ALTER TABLE device_state_samples ADD COLUMN routing_rule_version TEXT;
ALTER TABLE device_state_samples ADD COLUMN routing_error TEXT;
