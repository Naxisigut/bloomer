CREATE TABLE device_identity (
    id INTEGER PRIMARY KEY CHECK(id = 1),
    device_id TEXT NOT NULL UNIQUE CHECK(length(device_id) > 0),
    hardware_json TEXT NOT NULL CHECK(json_valid(hardware_json)),
    updated_at_us INTEGER NOT NULL
) STRICT;

CREATE TABLE device_state_samples (
    id INTEGER PRIMARY KEY,
    device_id TEXT NOT NULL REFERENCES device_identity(device_id),
    collected_at_us INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL CHECK(duration_ms >= 0),
    cpu_percent REAL,
    provider_cpu_percent REAL,
    memory_total_bytes INTEGER,
    memory_available_bytes INTEGER,
    memory_pressure TEXT,
    swap_used_bytes INTEGER,
    gpu_percent REAL,
    cpu_temperature_c REAL,
    gpu_temperature_c REAL,
    thermal_state TEXT,
    disk_available_bytes INTEGER,
    provider_running INTEGER CHECK(provider_running IN (0,1)),
    provider_connected INTEGER CHECK(provider_connected IN (0,1)),
    current_model TEXT,
    model_ready INTEGER CHECK(model_ready IN (0,1)),
    dispatch_tier TEXT,
    provider_pid INTEGER,
    provider_version TEXT,
    provider_state_at_us INTEGER,
    quality_json TEXT NOT NULL CHECK(json_valid(quality_json))
) STRICT;
CREATE INDEX device_state_time ON device_state_samples(collected_at_us DESC, id DESC);
