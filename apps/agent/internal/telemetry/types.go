// Package telemetry collects read-only host and provider observations. It never
// calls an earnings endpoint or changes the provider's configuration.
package telemetry

import "time"

const Interval = 60 * time.Second

// Quality distinguishes an observed zero/false from a missing observation.
// Sources and measurement semantics are documented in docs/device-state.md.
const (
	OK               = "ok"
	Unsupported      = "unsupported"
	Unavailable      = "unavailable"
	PermissionDenied = "permission_denied"
	Invalid          = "invalid"
	Stale            = "stale"
	NotRunning       = "not_running"
)

var Fields = []string{
	"cpu_percent", "provider_cpu_percent", "memory_total_bytes", "memory_available_bytes",
	"memory_pressure", "swap_used_bytes", "gpu_percent", "cpu_temperature_c", "gpu_temperature_c",
	"thermal_state", "disk_available_bytes", "provider_running", "provider_connected",
	"current_model", "model_ready", "dispatch_tier",
	"inference_active", "request_work_pending", "is_idle",
}

type Hardware struct {
	OS               string  `json:"os"`
	Architecture     string  `json:"architecture"`
	Model            *string `json:"model"`
	Chip             *string `json:"chip"`
	LogicalCPUs      int     `json:"logical_cpus"`
	MemoryTotalBytes *int64  `json:"memory_total_bytes"`
}

type Sample struct {
	ID                   int64      `json:"id"`
	DeviceID             string     `json:"device_id"`
	CollectedAt          time.Time  `json:"collected_at"`
	DurationMS           int64      `json:"duration_ms"`
	CPUPercent           *float64   `json:"cpu_percent"`
	ProviderCPUPercent   *float64   `json:"provider_cpu_percent"`
	MemoryTotalBytes     *int64     `json:"memory_total_bytes"`
	MemoryAvailableBytes *int64     `json:"memory_available_bytes"`
	MemoryPressure       *string    `json:"memory_pressure"`
	SwapUsedBytes        *int64     `json:"swap_used_bytes"`
	GPUPercent           *float64   `json:"gpu_percent"`
	CPUTemperatureC      *float64   `json:"cpu_temperature_c"`
	GPUTemperatureC      *float64   `json:"gpu_temperature_c"`
	ThermalState         *string    `json:"thermal_state"`
	DiskAvailableBytes   *int64     `json:"disk_available_bytes"`
	ProviderRunning      *bool      `json:"provider_running"`
	ProviderConnected    *bool      `json:"provider_connected"`
	CurrentModel         *string    `json:"current_model"`
	ModelReady           *bool      `json:"model_ready"`
	DispatchTier         *string    `json:"dispatch_tier"`
	InferenceActive      *bool      `json:"inference_active"`
	RequestWorkPending   *bool      `json:"request_work_pending"`
	IsIdle               *bool      `json:"is_idle"`
	LastDeviceEarningAt  *time.Time `json:"last_device_earning_at"`
	LastEarningsSyncAt   *time.Time `json:"last_earnings_sync_at"`
	EarningsProviderID   *string    `json:"earnings_provider_id"`
	ActivityError        *string    `json:"activity_error"`
	providerPublicKey    string
	ProviderPID          *int              `json:"provider_pid"`
	ProviderVersion      *string           `json:"provider_version"`
	ProviderStateAt      *time.Time        `json:"provider_state_at"`
	Quality              map[string]string `json:"quality"`
}

func NewSample(at time.Time) Sample {
	s := Sample{CollectedAt: at.UTC().Truncate(time.Microsecond), Quality: make(map[string]string)}
	for _, name := range Fields {
		s.Quality[name] = Unsupported
	}
	return s
}

func Ptr[T any](v T) *T { return &v }
