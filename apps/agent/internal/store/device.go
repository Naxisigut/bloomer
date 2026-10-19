package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/telemetry"
)

type DeviceIdentity struct {
	DeviceID string             `json:"device_id"`
	Hardware telemetry.Hardware `json:"hardware"`
}

// This installation ID is independent of account/Provider identity. It persists
// across agent restarts and upgrades; no physical device mapping is implied.
func (s *Store) SaveDeviceIdentity(ctx context.Context, hardware telemetry.Hardware, now time.Time) (string, error) {
	var id string
	body, err := json.Marshal(hardware)
	if err != nil {
		return "", err
	}
	err = s.Write(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, "SELECT device_id FROM device_identity WHERE id=1").Scan(&id); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			b := make([]byte, 16)
			if _, err = rand.Read(b); err != nil {
				return err
			}
			id = hex.EncodeToString(b)
			_, err = tx.ExecContext(ctx, "INSERT INTO device_identity VALUES(1,?,?,?)", id, string(body), now.UnixMicro())
			return err
		}
		// No write when hardware did not change; partial readings after a failed
		// probe must not erase previously observed fixed hardware information.
		var previous telemetry.Hardware
		var previousJSON string
		if err := tx.QueryRowContext(ctx, "SELECT hardware_json FROM device_identity WHERE id=1").Scan(&previousJSON); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(previousJSON), &previous); err != nil {
			return err
		}
		if hardware.Model == nil {
			hardware.Model = previous.Model
		}
		if hardware.Chip == nil {
			hardware.Chip = previous.Chip
		}
		if hardware.MemoryTotalBytes == nil {
			hardware.MemoryTotalBytes = previous.MemoryTotalBytes
		}
		body, err := json.Marshal(hardware)
		if err != nil {
			return err
		}
		if string(body) == previousJSON {
			return nil
		}
		_, err = tx.ExecContext(ctx, "UPDATE device_identity SET hardware_json=?,updated_at_us=? WHERE id=1", string(body), now.UnixMicro())
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) DeviceIdentity(ctx context.Context) (*DeviceIdentity, error) {
	var d DeviceIdentity
	var body string
	err := s.reader.QueryRowContext(ctx, "SELECT device_id,hardware_json FROM device_identity WHERE id=1").Scan(&d.DeviceID, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(body), &d.Hardware); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) SaveDeviceSample(ctx context.Context, v telemetry.Sample) error {
	if v.DeviceID == "" || v.CollectedAt.IsZero() || v.DurationMS < 0 {
		return errors.New("invalid device sample")
	}
	for _, field := range telemetry.Fields {
		if v.Quality[field] == "" {
			return errors.New("missing sample quality")
		}
	}
	body, err := json.Marshal(v.Quality)
	if err != nil {
		return err
	}
	var stateAt *int64
	if v.ProviderStateAt != nil {
		stateAt = telemetry.Ptr(v.ProviderStateAt.UnixMicro())
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO device_state_samples(device_id,collected_at_us,duration_ms,cpu_percent,provider_cpu_percent,memory_total_bytes,memory_available_bytes,memory_pressure,swap_used_bytes,gpu_percent,cpu_temperature_c,gpu_temperature_c,thermal_state,disk_available_bytes,provider_running,provider_connected,current_model,model_ready,dispatch_tier,provider_pid,provider_version,provider_state_at_us,quality_json)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.DeviceID, v.CollectedAt.UnixMicro(), v.DurationMS, v.CPUPercent, v.ProviderCPUPercent, v.MemoryTotalBytes, v.MemoryAvailableBytes, v.MemoryPressure, v.SwapUsedBytes, v.GPUPercent, v.CPUTemperatureC, v.GPUTemperatureC, v.ThermalState, v.DiskAvailableBytes, v.ProviderRunning, v.ProviderConnected, v.CurrentModel, v.ModelReady, v.DispatchTier, v.ProviderPID, v.ProviderVersion, stateAt, string(body))
		return err
	})
}

const sampleColumns = `id,device_id,collected_at_us,duration_ms,cpu_percent,provider_cpu_percent,memory_total_bytes,memory_available_bytes,memory_pressure,swap_used_bytes,gpu_percent,cpu_temperature_c,gpu_temperature_c,thermal_state,disk_available_bytes,provider_running,provider_connected,current_model,model_ready,dispatch_tier,provider_pid,provider_version,provider_state_at_us,quality_json`

type DevicePage struct {
	Items        []telemetry.Sample `json:"items"`
	NextBeforeID *int64             `json:"next_before_id"`
}

// Queries are independent of earnings initialization and always enforce the
// rolling retention window, including before physical cleanup runs.
func (s *Store) DeviceSamples(ctx context.Context, now, from, to time.Time, before int64, limit int) (DevicePage, error) {
	page := DevicePage{Items: make([]telemetry.Sample, 0)}
	if limit < 1 || limit > 500 || !from.Before(to) || before < 0 {
		return page, errors.New("invalid device query")
	}
	cutoff := now.UTC().Add(-config.Retention)
	if from.Before(cutoff) {
		from = cutoff
	}
	// Sort IDs from the covering time index before fetching payloads. A broad
	// yearly range must not load every quality JSON/metric row just to page 500.
	query := "SELECT " + sampleColumns + ` FROM device_state_samples WHERE id IN (
 SELECT id FROM device_state_samples WHERE collected_at_us>=? AND collected_at_us<? AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?)
 ORDER BY id DESC`
	return s.readDeviceSamples(ctx, query, []any{from.UnixMicro(), to.UnixMicro(), before, before, limit + 1}, limit)
}

func (s *Store) LatestDeviceSample(ctx context.Context, now time.Time) (*telemetry.Sample, error) {
	// Walking newest row IDs finds the current sample without sorting a year
	// of history. Future observations after a clock rollback remain visible.
	page, err := s.readDeviceSamples(ctx, "SELECT "+sampleColumns+" FROM device_state_samples NOT INDEXED WHERE collected_at_us>=? ORDER BY id DESC LIMIT 1", []any{now.Add(-config.Retention).UnixMicro()}, 1)
	if err != nil || len(page.Items) == 0 {
		return nil, err
	}
	return &page.Items[0], nil
}

func (s *Store) readDeviceSamples(ctx context.Context, query string, args []any, limit int) (DevicePage, error) {
	page := DevicePage{Items: make([]telemetry.Sample, 0)}
	rows, err := s.reader.QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var v telemetry.Sample
		var at int64
		var stateAt *int64
		var body string
		if err = rows.Scan(&v.ID, &v.DeviceID, &at, &v.DurationMS, &v.CPUPercent, &v.ProviderCPUPercent, &v.MemoryTotalBytes, &v.MemoryAvailableBytes, &v.MemoryPressure, &v.SwapUsedBytes, &v.GPUPercent, &v.CPUTemperatureC, &v.GPUTemperatureC, &v.ThermalState, &v.DiskAvailableBytes, &v.ProviderRunning, &v.ProviderConnected, &v.CurrentModel, &v.ModelReady, &v.DispatchTier, &v.ProviderPID, &v.ProviderVersion, &stateAt, &body); err != nil {
			return page, err
		}
		v.CollectedAt = time.UnixMicro(at).UTC()
		if stateAt != nil {
			v.ProviderStateAt = telemetry.Ptr(time.UnixMicro(*stateAt).UTC())
		}
		if err = json.Unmarshal([]byte(body), &v.Quality); err != nil {
			return page, err
		}
		page.Items = append(page.Items, v)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextBeforeID = telemetry.Ptr(page.Items[len(page.Items)-1].ID)
	}
	return page, nil
}
