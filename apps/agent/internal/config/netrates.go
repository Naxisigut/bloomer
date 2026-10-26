package config

import (
	"errors"
	"fmt"
	"regexp"

	"bloomer/agent/internal/control"
)

var chipFamilyID = regexp.MustCompile(`^[A-Za-z0-9]{1,16}$`)

type NetworkRates struct {
	Enabled                  bool     `json:"enabled"`
	IntervalMinutes          int      `json:"interval_minutes"`
	WindowSamples            int      `json:"window_samples"`
	AutoSwitch               bool     `json:"auto_switch"`
	SwitchMinIntervalMinutes int      `json:"switch_min_interval_minutes"`
	SwitchAdvantageRatio     float64  `json:"switch_advantage_ratio"`
	FallbackModel            string   `json:"fallback_model"`
	ChipFamilies             []string `json:"chip_families"`
	MemoryGB                 int      `json:"memory_gb"`
	MinProviders             int      `json:"min_providers"`
	NearZeroMicroUSDPerHour  int64    `json:"near_zero_micro_usd_per_hour"`
}

func DefaultNetworkRates() NetworkRates {
	return NetworkRates{
		Enabled:                  true,
		IntervalMinutes:          2,
		WindowSamples:            6,
		AutoSwitch:               false,
		SwitchMinIntervalMinutes: 30,
		SwitchAdvantageRatio:     0.2,
		FallbackModel:            "gemma-4-26b-qat-4bit",
		ChipFamilies:             []string{"M4", "M5", "M6"},
		MemoryGB:                 64,
		MinProviders:             3,
		NearZeroMicroUSDPerHour:  1000,
	}
}

func (n NetworkRates) Validate() error {
	if n.IntervalMinutes < 1 || n.IntervalMinutes > 60 {
		return errors.New("network_rates.interval_minutes must be between 1 and 60")
	}
	if n.WindowSamples < 2 || n.WindowSamples > 48 {
		return errors.New("network_rates.window_samples must be between 2 and 48")
	}
	if n.SwitchMinIntervalMinutes < 1 || n.SwitchMinIntervalMinutes > 1440 {
		return errors.New("network_rates.switch_min_interval_minutes must be between 1 and 1440")
	}
	if n.SwitchAdvantageRatio < 0 || n.SwitchAdvantageRatio > 10 {
		return errors.New("network_rates.switch_advantage_ratio must be between 0 and 10")
	}
	if len(n.ChipFamilies) < 1 || len(n.ChipFamilies) > 8 {
		return errors.New("network_rates.chip_families must contain 1 to 8 entries")
	}
	seen := map[string]bool{}
	for _, f := range n.ChipFamilies {
		if !chipFamilyID.MatchString(f) {
			return errors.New("network_rates.chip_families entries must be 1 to 16 alphanumeric characters")
		}
		if seen[f] {
			return fmt.Errorf("network_rates.chip_families contains duplicate %q", f)
		}
		seen[f] = true
	}
	if n.MemoryGB < 1 || n.MemoryGB > 1024 {
		return errors.New("network_rates.memory_gb must be between 1 and 1024")
	}
	if n.MinProviders < 1 || n.MinProviders > 1000 {
		return errors.New("network_rates.min_providers must be between 1 and 1000")
	}
	if n.NearZeroMicroUSDPerHour < 0 || n.NearZeroMicroUSDPerHour > 1_000_000_000 {
		return errors.New("network_rates.near_zero_micro_usd_per_hour must be between 0 and 1000000000")
	}
	if !control.ValidModelID(n.FallbackModel) {
		return errors.New("network_rates.fallback_model is invalid")
	}
	return nil
}
