package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

var errUnsupported = errors.New("unsupported platform")

// Only known diagnostic fields are decoded. Identity keys, configuration paths,
// prompts, and raw error messages from the provider never enter our database.
type daemonState struct {
	Trust                json.RawMessage `json:"trust"`
	InferenceActive      *bool           `json:"inference_active"`
	RequestWorkPending   *bool           `json:"request_work_pending"`
	AttestationPublicKey string          `json:"attestation_public_key"`
	Schema               *int            `json:"schema"`
	PID                  *int            `json:"pid"`
	Version              string          `json:"version"`
	WrittenAt            *float64        `json:"written_at"`
	CurrentModel         *string         `json:"current_model"`
	WarmModels           *[]string       `json:"warm_models"`
	LoadTransitionActive *bool           `json:"load_transition_active"`
	ProcessIdentity      *struct {
		PID             int   `json:"pid"`
		StartTimeMicros int64 `json:"start_time_micros"`
	} `json:"process_identity"`
	Slots *[]struct {
		Model     string  `json:"model"`
		KVBackend *string `json:"kv_backend"`
		LoadError *string `json:"load_error"`
	} `json:"slots"`
	Stats json.RawMessage `json:"stats"`
}

func (p *Sampler) provider(ctx context.Context, s *Sample) {
	names := []string{"provider_running", "provider_cpu_percent", "current_model", "model_ready", "inference_active", "request_work_pending", "trust_received_at", "session_id"}
	mark := func(status string) {
		for _, name := range names {
			s.Quality[name] = status
		}
	}
	body, err := p.read(p.StatePath)
	if err != nil {
		mark(quality(err))
		return
	}
	var d daemonState
	if len(body) > 1<<20 || json.Unmarshal(body, &d) != nil || d.Schema == nil {
		mark(Invalid)
		return
	}
	if *d.Schema != 1 {
		mark(Unsupported)
		return
	}
	if d.PID == nil || *d.PID <= 0 || int64(*d.PID) > math.MaxInt32 || d.WrittenAt == nil || math.IsNaN(*d.WrittenAt) || math.IsInf(*d.WrittenAt, 0) || *d.WrittenAt <= 0 || *d.WrittenAt > 253402300799 {
		mark(Invalid)
		return
	}
	var identity *int64
	if d.ProcessIdentity != nil {
		if d.ProcessIdentity.PID != *d.PID || d.ProcessIdentity.StartTimeMicros <= 0 {
			mark(Invalid)
			return
		}
		identity = &d.ProcessIdentity.StartTimeMicros
	}
	alive, err := p.process(*d.PID, identity)
	if err != nil {
		if errors.Is(err, errUnsupported) {
			mark(Unsupported)
		} else {
			mark(quality(err))
		}
		return
	}
	s.ProviderPID = d.PID
	s.ProviderRunning = &alive
	s.Quality["provider_running"] = OK
	if identity != nil {
		s.ProcessStartMicros = Ptr(*identity)
	}
	seconds, fraction := math.Modf(*d.WrittenAt)
	at := time.Unix(int64(seconds), int64(fraction*1e9)).UTC().Truncate(time.Microsecond)
	s.ProviderStateAt = &at
	if d.Version != "" && len(d.Version) <= 128 && !strings.ContainsAny(d.Version, "\n\r\x00") {
		s.ProviderVersion = &d.Version
	}
	if !alive {
		for _, name := range names[1:] {
			s.Quality[name] = NotRunning
		}
		return
	}
	s.ProviderCPUPercent, s.Quality["provider_cpu_percent"] = p.processCPU(ctx, *d.PID)
	// A live process and an old state file do not prove a live model/connection.
	age := s.CollectedAt.Sub(at)
	if age < -5*time.Second || age > 90*time.Second {
		status := Stale
		if age < -5*time.Second {
			status = Invalid
		}
		s.Quality["current_model"] = status
		s.Quality["model_ready"] = status
		s.Quality["inference_active"], s.Quality["request_work_pending"] = status, status
		s.Quality["trust_received_at"], s.Quality["session_id"] = status, status
		return
	}
	readTrust(s, d.Trust)

	if d.InferenceActive != nil {
		s.InferenceActive = d.InferenceActive
		s.Quality["inference_active"] = OK
	}
	s.RequestsServed = requestsServed(d.Stats)
	if d.RequestWorkPending != nil {
		s.RequestWorkPending = d.RequestWorkPending
		s.Quality["request_work_pending"] = OK
	}
	if len(d.AttestationPublicKey) <= 4096 && !strings.ContainsAny(d.AttestationPublicKey, "\r\n\x00") {
		s.providerPublicKey = d.AttestationPublicKey
	}
	if d.CurrentModel == nil || *d.CurrentModel == "" {
		s.Quality["current_model"] = OK
		return
	}
	if len(*d.CurrentModel) > 1024 || strings.ContainsAny(*d.CurrentModel, "\n\r\x00") {
		s.Quality["current_model"] = Invalid
		s.Quality["model_ready"] = Invalid
		return
	}
	s.CurrentModel = d.CurrentModel
	s.Quality["current_model"] = OK
	if d.WarmModels == nil {
		return
	}
	ready := false
	for _, model := range *d.WarmModels {
		if model == *d.CurrentModel {
			ready = true
		}
	}
	if d.LoadTransitionActive != nil && *d.LoadTransitionActive {
		ready = false
	}
	if d.Slots != nil {
		for _, slot := range *d.Slots {
			if slot.Model == *d.CurrentModel && (slot.LoadError != nil || slot.KVBackend == nil) {
				ready = false
			}
		}
	}
	s.ModelReady = &ready
	s.Quality["model_ready"] = OK
	// Schema 1 has no authoritative connected flag or dispatch tier. In
	// particular, trust.status=online is a trust verdict, not a socket probe.
}

func requestsServed(raw json.RawMessage) *uint64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var stats struct {
		RequestsServed *uint64 `json:"requests_served"`
	}
	if json.Unmarshal(raw, &stats) != nil {
		return nil
	}
	return stats.RequestsServed
}

// Optional alert evidence must not invalidate the existing process/model fields.
func readTrust(s *Sample, raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	var trust struct {
		ReceivedAt    json.RawMessage `json:"received_at"`
		Authorization json.RawMessage `json:"authorization"`
	}
	if json.Unmarshal(raw, &trust) != nil {
		s.Quality["trust_received_at"], s.Quality["session_id"] = Invalid, Invalid
		return
	}
	if len(trust.ReceivedAt) > 0 && string(trust.ReceivedAt) != "null" {
		s.Quality["trust_received_at"] = Invalid
		var at float64
		if json.Unmarshal(trust.ReceivedAt, &at) == nil && !math.IsNaN(at) && !math.IsInf(at, 0) && at > 0 && at <= 253402300799 && at <= float64(s.CollectedAt.Unix())+float64(s.CollectedAt.Nanosecond())/1e9+5 {
			s.TrustReceivedAt, s.Quality["trust_received_at"] = Ptr(at), OK
		}
	}
	if len(trust.Authorization) > 0 && string(trust.Authorization) != "null" {
		var authorization struct {
			SessionID json.RawMessage `json:"session_id"`
		}
		if json.Unmarshal(trust.Authorization, &authorization) != nil {
			s.Quality["session_id"] = Invalid
			return
		}
		if len(authorization.SessionID) > 0 && string(authorization.SessionID) != "null" {
			s.Quality["session_id"] = Invalid
			var id string
			if json.Unmarshal(authorization.SessionID, &id) == nil && len(id) > 0 && len(id) <= 1024 && !strings.ContainsAny(id, "\r\n\x00") {
				s.SessionID, s.Quality["session_id"] = Ptr(id), OK
			}
		}
	}
}
