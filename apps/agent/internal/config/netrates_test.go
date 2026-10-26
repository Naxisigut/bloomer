package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultNetworkRates(t *testing.T) {
	n := DefaultNetworkRates()
	if !n.Enabled || n.IntervalMinutes != 2 || n.WindowSamples != 6 || n.AutoSwitch || n.SwitchMinIntervalMinutes != 30 ||
		n.SwitchAdvantageRatio != 0.2 || n.FallbackModel != "gemma-4-26b-qat-4bit" || n.MemoryGB != 64 ||
		n.MinProviders != 3 || n.NearZeroMicroUSDPerHour != 1000 {
		t.Fatal(n)
	}
	if len(n.ChipFamilies) != 3 || n.ChipFamilies[0] != "M4" {
		t.Fatal(n.ChipFamilies)
	}
	if len(n.ChipTiers) != 1 || n.ChipTiers[0] != "Pro" {
		t.Fatal(n.ChipTiers)
	}
	noTier := n
	noTier.ChipTiers = []string{}
	if err := noTier.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkRatesPartialConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"network_rates":{"interval_minutes":10,"chip_families":["M5"]}}`), 0600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.NetworkRates.IntervalMinutes != 10 || c.NetworkRates.WindowSamples != 6 || !c.NetworkRates.Enabled ||
		c.NetworkRates.FallbackModel != "gemma-4-26b-qat-4bit" || len(c.NetworkRates.ChipFamilies) != 1 || c.NetworkRates.ChipFamilies[0] != "M5" {
		t.Fatal(c.NetworkRates)
	}
}

func TestNetworkRatesValidation(t *testing.T) {
	n := DefaultNetworkRates()
	for _, tc := range []struct {
		mutate func(*NetworkRates)
	}{
		{func(n *NetworkRates) { n.IntervalMinutes = 0 }},
		{func(n *NetworkRates) { n.WindowSamples = 1 }},
		{func(n *NetworkRates) { n.SwitchMinIntervalMinutes = 0 }},
		{func(n *NetworkRates) { n.SwitchAdvantageRatio = -0.1 }},
		{func(n *NetworkRates) { n.ChipFamilies = nil }},
		{func(n *NetworkRates) { n.ChipFamilies = []string{"bad family"} }},
		{func(n *NetworkRates) { n.ChipTiers = []string{"Pro", "Pro"} }},
		{func(n *NetworkRates) { n.ChipTiers = []string{"bad tier"} }},
		{func(n *NetworkRates) { n.MemoryGB = 0 }},
		{func(n *NetworkRates) { n.MinProviders = 0 }},
		{func(n *NetworkRates) { n.NearZeroMicroUSDPerHour = -1 }},
		{func(n *NetworkRates) { n.FallbackModel = "../escape" }},
	} {
		copy := n
		tc.mutate(&copy)
		if err := copy.Validate(); err == nil {
			t.Fatal("accepted invalid network rates")
		}
	}
}
