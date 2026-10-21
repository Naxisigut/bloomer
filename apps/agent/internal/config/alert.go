package config

import (
	"errors"
	"net/url"
	"path/filepath"
	"strings"
)

// Alert contains paths and policy only; the Bark device key is never configuration data.
type Alert struct {
	Enabled                bool   `json:"enabled"`
	BarkKeyFile            string `json:"bark_key_file"`
	BarkServer             string `json:"bark_server"`
	BindInterface          string `json:"bind_interface"`
	RepeatMinutes          int    `json:"repeat_minutes"`
	DashboardURL           string `json:"dashboard_url"`
	DeviceName             string `json:"device_name"`
	DisconnectSeconds      int    `json:"disconnect_seconds"`
	ConsecutiveSamples     int    `json:"consecutive_samples"`
	FlapWindowMinutes      int    `json:"flap_window_minutes"`
	FlapChanges            int    `json:"flap_changes"`
	NoEarningsMinutes      int    `json:"no_earnings_minutes"`
	NightNoEarningsMinutes int    `json:"night_no_earnings_minutes"`
	NightStartHour         int    `json:"night_start_hour"`
	NightEndHour           int    `json:"night_end_hour"`
}

func DefaultAlert(keyFile string) Alert {
	return Alert{BarkKeyFile: keyFile, BarkServer: "https://api.day.app", BindInterface: "en1", RepeatMinutes: 30, DisconnectSeconds: 180, ConsecutiveSamples: 2, FlapWindowMinutes: 60, FlapChanges: 5, NoEarningsMinutes: 30, NightNoEarningsMinutes: 60, NightStartHour: 0, NightEndHour: 8}
}
func (a Alert) Validate() error {
	if !a.Enabled {
		return nil
	}
	if !filepath.IsAbs(a.BarkKeyFile) {
		return errors.New("alert.bark_key_file must be absolute and nonempty")
	}
	u, err := url.Parse(a.BarkServer)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("alert.bark_server must be an HTTP(S) URL without credentials, query or fragment")
	}
	if a.DashboardURL != "" {
		u, err = url.Parse(a.DashboardURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			return errors.New("alert.dashboard_url must be an HTTP(S) URL without credentials")
		}
	}
	if len(a.DeviceName) > 128 || strings.ContainsAny(a.DeviceName, "\r\n\x00") || len(a.BindInterface) > 128 || strings.ContainsAny(a.BindInterface, "\r\n\x00") {
		return errors.New("invalid alert device name or interface")
	}
	for _, n := range []int{a.RepeatMinutes, a.DisconnectSeconds, a.ConsecutiveSamples, a.FlapWindowMinutes, a.FlapChanges, a.NoEarningsMinutes, a.NightNoEarningsMinutes} {
		if n < 1 || n > 10080 {
			return errors.New("alert thresholds must be between 1 and 10080")
		}
	}
	if a.NightStartHour < 0 || a.NightStartHour > 23 || a.NightEndHour < 0 || a.NightEndHour > 23 {
		return errors.New("alert night hours must be between 0 and 23")
	}
	return nil
}
