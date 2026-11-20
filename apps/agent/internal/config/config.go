// Package config defines the fixed collection rules and the local JSON configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	CollectionInterval = 60 * time.Second
	RecoveryThreshold  = 30 * time.Minute
	Retention          = 365 * 24 * time.Hour
	CleanupInterval    = 7 * 24 * time.Hour
	StaleAfter         = 120 * time.Second
	RequestTimeout     = 10 * time.Second
	RoundTimeout       = 50 * time.Second
	QueryTimeout       = 10 * time.Second
	MaxResponseBytes   = 2 << 20
)

// Windows returns a fresh copy; expansion never changes the next round's default.
func Windows() []int { return []int{50, 100, 300, 1000} }

type Config struct {
	Alert                  Alert        `json:"alert"`
	NetworkRates           NetworkRates `json:"network_rates"`
	RemoteControlEnabled   bool         `json:"remote_control_enabled"`
	LongTaskRestartEnabled bool         `json:"long_task_restart_enabled"`
	NoTaskRestartEnabled   bool         `json:"no_task_restart_enabled"`
	DarkbloomBinary        string       `json:"darkbloom_binary"`
	CollectionEnabled      bool         `json:"collection_enabled"`
	DeviceStateEnabled     bool         `json:"device_state_enabled"`
	ProviderStateFile      string       `json:"provider_state_file"`
	DataDir                string       `json:"data_dir"`
	LogDir                 string       `json:"log_dir"`
	Listen                 string       `json:"listen"`
	UpstreamURL            string       `json:"upstream_url"`
	UpstreamCredentialFile string       `json:"upstream_credential_file"`
	AccessTokenFile        string       `json:"access_token_file"`
	AccountID              string       `json:"account_id,omitempty"`
}

func Defaults() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, errors.New("cannot resolve user home directory")
	}
	data := filepath.Join(home, "Library", "Application Support", "Bloomer", "agent")
	return Config{
		Alert:                  DefaultAlert(filepath.Join(data, "bark.key")),
		NetworkRates:           DefaultNetworkRates(),
		RemoteControlEnabled:   true,
		LongTaskRestartEnabled: false,
		NoTaskRestartEnabled:   false,
		DarkbloomBinary:        filepath.Join(home, ".darkbloom", "bin", "darkbloom"),
		CollectionEnabled:      true,
		DeviceStateEnabled:     true,
		ProviderStateFile:      filepath.Join(home, ".darkbloom", "daemon-state.json"),
		DataDir:                data, LogDir: filepath.Join(home, "Library", "Logs", "Bloomer", "agent"),
		Listen: "127.0.0.1:17890", UpstreamURL: "https://api.darkbloom.dev/v1/provider/account-earnings",
		UpstreamCredentialFile: filepath.Join(home, ".darkbloom", "auth_token"),
	}, nil
}

func Load(path string) (Config, error) {
	c, err := Defaults()
	if err != nil {
		return c, err
	}
	base, err := os.Getwd()
	if err != nil {
		return c, err
	}
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return c, err
		}
		base = filepath.Dir(abs)
		f, err := os.Open(abs)
		if err != nil {
			return c, fmt.Errorf("open configuration: %w", err)
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 65537))
		if err != nil {
			return c, errors.New("read configuration failed")
		}
		if len(data) > 65536 {
			return c, errors.New("configuration exceeds 64 KiB")
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&struct {
			*Config
			LegacyAccountToken json.RawMessage `json:"provider_account_token_file"`
			LegacyProviderID   json.RawMessage `json:"provider_id"`
		}{Config: &c}); err != nil {
			return c, errors.New("invalid configuration JSON or unknown field")
		}
		if err = dec.Decode(new(any)); err != io.EOF {
			return c, errors.New("configuration must contain one JSON object")
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return c, errors.New("configuration must be an object")
		}
	}
	for _, p := range []*string{&c.DataDir, &c.LogDir, &c.UpstreamCredentialFile, &c.AccessTokenFile, &c.ProviderStateFile, &c.DarkbloomBinary, &c.Alert.BarkKeyFile} {
		if *p == "" {
			continue
		}
		*p, err = resolve(*p, base)
		if err != nil {
			return c, err
		}
	}
	if c.AccessTokenFile == "" {
		c.AccessTokenFile = filepath.Join(c.DataDir, "access.token")
	}
	return c, c.Validate()
}

func resolve(p, base string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	} else if strings.HasPrefix(p, "~") {
		return "", errors.New("only ~/ user paths are supported")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p), nil
}

func (c Config) Validate() error {
	if err := c.Alert.Validate(); err != nil {
		return err
	}
	if err := c.NetworkRates.Validate(); err != nil {
		return err
	}
	if c.Alert.Enabled {
		for _, path := range []string{c.DatabasePath(), c.LockPath(), filepath.Join(c.LogDir, "agent.log"), c.ProviderStateFile, c.UpstreamCredentialFile, c.AccessTokenFile, c.DarkbloomBinary} {
			if c.Alert.BarkKeyFile == path {
				return errors.New("Bark key must use a separate credential file")
			}
		}
	}
	for _, p := range []string{c.DataDir, c.LogDir, c.AccessTokenFile, c.UpstreamCredentialFile, c.ProviderStateFile, c.DarkbloomBinary} {
		if p == "" || !filepath.IsAbs(p) {
			return errors.New("runtime and credential paths must be absolute and nonempty")
		}
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return errors.New("listen must contain a literal loopback IP and port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("listen address must be a literal loopback IP")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("listen port must be between 1 and 65535")
	}
	u, err := url.Parse(c.UpstreamURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/v1/provider/account-earnings" {
		return errors.New("upstream_url must be an HTTPS account-earnings URL without userinfo, query or fragment")
	}
	if filepath.Clean(c.AccessTokenFile) == filepath.Clean(c.UpstreamCredentialFile) {
		return errors.New("dashboard and upstream credentials must use separate files")
	}
	if c.ProviderStateFile == c.AccessTokenFile || c.ProviderStateFile == c.UpstreamCredentialFile {
		return errors.New("provider state must not point at a credential file")
	}
	for _, path := range []string{c.DatabasePath(), c.LockPath(), filepath.Join(c.LogDir, "agent.log")} {
		if c.AccessTokenFile == path || c.UpstreamCredentialFile == path {
			return errors.New("credential paths must not overlap database, lock or log files")
		}
	}
	return nil
}

func (c Config) DatabasePath() string { return filepath.Join(c.DataDir, "agent.db") }
func (c Config) LockPath() string     { return filepath.Join(c.DataDir, "agent.lock") }
