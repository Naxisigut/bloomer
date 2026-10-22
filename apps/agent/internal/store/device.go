package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

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
	var earningAt, syncAt *int64
	if v.LastDeviceEarningAt != nil {
		earningAt = telemetry.Ptr(v.LastDeviceEarningAt.UnixMicro())
	}
	if v.LastEarningsSyncAt != nil {
		syncAt = telemetry.Ptr(v.LastEarningsSyncAt.UnixMicro())
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM device_state_samples WHERE device_id=?", v.DeviceID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO device_state_samples(device_id,collected_at_us,duration_ms,cpu_percent,provider_cpu_percent,memory_total_bytes,memory_available_bytes,memory_pressure,swap_used_bytes,gpu_percent,cpu_temperature_c,gpu_temperature_c,thermal_state,disk_available_bytes,provider_running,provider_connected,current_model,model_ready,dispatch_tier,provider_pid,provider_version,provider_state_at_us,quality_json,inference_active,request_work_pending,is_idle,last_device_earning_at_us,last_earnings_sync_at_us,earnings_provider_id,activity_error,trust_received_at,session_id)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.DeviceID, v.CollectedAt.UnixMicro(), v.DurationMS, v.CPUPercent, v.ProviderCPUPercent, v.MemoryTotalBytes, v.MemoryAvailableBytes, v.MemoryPressure, v.SwapUsedBytes, v.GPUPercent, v.CPUTemperatureC, v.GPUTemperatureC, v.ThermalState, v.DiskAvailableBytes, v.ProviderRunning, v.ProviderConnected, v.CurrentModel, v.ModelReady, v.DispatchTier, v.ProviderPID, v.ProviderVersion, stateAt, string(body), v.InferenceActive, v.RequestWorkPending, v.IsIdle, earningAt, syncAt, v.EarningsProviderID, v.ActivityError, v.TrustReceivedAt, v.SessionID)
		return err
	})
}

const sampleColumns = `id,device_id,collected_at_us,duration_ms,cpu_percent,provider_cpu_percent,memory_total_bytes,memory_available_bytes,memory_pressure,swap_used_bytes,gpu_percent,cpu_temperature_c,gpu_temperature_c,thermal_state,disk_available_bytes,provider_running,provider_connected,current_model,model_ready,dispatch_tier,provider_pid,provider_version,provider_state_at_us,quality_json,inference_active,request_work_pending,is_idle,last_device_earning_at_us,last_earnings_sync_at_us,earnings_provider_id,activity_error,trust_received_at,session_id`

func (s *Store) LatestDeviceSample(ctx context.Context) (*telemetry.Sample, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT "+sampleColumns+" FROM device_state_samples ORDER BY id DESC LIMIT 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var v telemetry.Sample
	var at int64
	var stateAt *int64
	var body string
	var earningAt, syncAt *int64
	if err = rows.Scan(&v.ID, &v.DeviceID, &at, &v.DurationMS, &v.CPUPercent, &v.ProviderCPUPercent, &v.MemoryTotalBytes, &v.MemoryAvailableBytes, &v.MemoryPressure, &v.SwapUsedBytes, &v.GPUPercent, &v.CPUTemperatureC, &v.GPUTemperatureC, &v.ThermalState, &v.DiskAvailableBytes, &v.ProviderRunning, &v.ProviderConnected, &v.CurrentModel, &v.ModelReady, &v.DispatchTier, &v.ProviderPID, &v.ProviderVersion, &stateAt, &body, &v.InferenceActive, &v.RequestWorkPending, &v.IsIdle, &earningAt, &syncAt, &v.EarningsProviderID, &v.ActivityError, &v.TrustReceivedAt, &v.SessionID); err != nil {
		return nil, err
	}
	v.CollectedAt = time.UnixMicro(at).UTC()
	if stateAt != nil {
		v.ProviderStateAt = telemetry.Ptr(time.UnixMicro(*stateAt).UTC())
	}
	if err = json.Unmarshal([]byte(body), &v.Quality); err != nil {
		return nil, err
	}
	if earningAt != nil {
		v.LastDeviceEarningAt = telemetry.Ptr(time.UnixMicro(*earningAt).UTC())
	}
	if syncAt != nil {
		v.LastEarningsSyncAt = telemetry.Ptr(time.UnixMicro(*syncAt).UTC())
	}
	delete(v.Quality, "routing_state")
	delete(v.Quality, "routing_priority")
	for _, field := range []string{"inference_active", "request_work_pending", "is_idle", "trust_received_at", "session_id"} {
		if v.Quality[field] == "" {
			v.Quality[field] = telemetry.Unsupported
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &v, nil
}
