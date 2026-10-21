package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPathsAndValidation(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(dir, "example.json")
	if err := os.WriteFile(path, []byte(`{"data_dir":"state","log_dir":"~/logs","upstream_credential_file":"~/official.token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != filepath.Join(dir, "state") || c.LogDir != filepath.Join(home, "logs") || c.AccessTokenFile != filepath.Join(dir, "state", "access.token") {
		t.Fatalf("incorrect path resolution: %+v", c)
	}
	if !c.RemoteControlEnabled || c.DarkbloomBinary != filepath.Join(home, ".darkbloom", "bin", "darkbloom") {
		t.Fatal("remote control defaults not resolved")
	}
	if !c.DeviceStateEnabled || c.ProviderStateFile != filepath.Join(home, ".darkbloom", "daemon-state.json") {
		t.Fatal("old configuration did not enable device collection by default")
	}
	for _, body := range []string{`null`, `{} {}`, `{"allowed_origins":[]}`, `{"token":"secret"}`, `{"listen":"0.0.0.0:17890"}`, `{"listen":"localhost:17890"}`, `{"listen":"127.0.0.1:0"}`, `{"upstream_url":"https://user:secret@example.com/v1/provider/account-earnings"}`, `{"upstream_url":"http://example.com/v1/provider/account-earnings"}`, `{"data_dir":"~other/state"}`} {
		os.WriteFile(path, []byte(body), 0600)
		if _, err = Load(path); err == nil {
			t.Fatalf("accepted invalid config: %s", body)
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatal("config error leaks input")
		}
	}
}
func TestIndependentPrivateAccessToken(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.token")
	a, err := EnsureAccessToken(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EnsureAccessToken(p)
	if err != nil || a != b || len(a) != 64 {
		t.Fatal("token was not persistent")
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0600 {
		t.Fatal("token is not private")
	}
	os.Chmod(p, 0644)
	if _, err = ReadToken(p); err == nil {
		t.Fatal("world-readable token accepted")
	}
	os.Chmod(p, 0600)
	link := filepath.Join(t.TempDir(), "linked.token")
	if err = os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, err = EnsureAccessToken(link); err == nil {
		t.Fatal("symlink token accepted")
	}
}

func TestRemovedOwnerConfigurationIsIgnoredOnUpgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"provider_account_token_file":"official.token","provider_id":"device-a"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal("legacy owner settings prevented upgrade:", err)
	}
}

func TestControlConfigurationOptOutAndCustomCLI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"remote_control_enabled":false,"darkbloom_binary":"tools/darkbloom"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil || c.RemoteControlEnabled || c.DarkbloomBinary != filepath.Join(dir, "tools", "darkbloom") {
		t.Fatal(c, err)
	}
}

func TestAlertDefaultsPartialConfigurationAndValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{}`), 0600)
	c, err := Load(path)
	if err != nil || c.Alert.Enabled || c.Alert.DisconnectSeconds != 180 || c.Alert.ConsecutiveSamples != 2 || c.Alert.FlapChanges != 5 || c.Alert.NightEndHour != 8 {
		t.Fatal(c.Alert, err)
	}
	os.WriteFile(path, []byte(`{"alert":{"enabled":true,"bark_key_file":"private/bark.key","repeat_minutes":45}}`), 0600)
	c, err = Load(path)
	if err != nil || c.Alert.BarkKeyFile != filepath.Join(dir, "private", "bark.key") || c.Alert.RepeatMinutes != 45 || c.Alert.FlapWindowMinutes != 60 {
		t.Fatal(c.Alert, err)
	}
	for _, body := range []string{
		`{"alert":{"enabled":true,"repeat_minutes":0}}`, `{"alert":{"enabled":true,"consecutive_samples":-1}}`,
		`{"alert":{"enabled":true,"night_end_hour":24}}`, `{"alert":{"enabled":true,"bark_server":"https://user:secret@example.com"}}`,
		`{"alert":{"enabled":true,"dashboard_url":"javascript:secret"}}`, `{"alert":{"device_key":"secret"}}`,
		`{"alert":{"enabled":true,"bark_key_file":"~other/key"}}`,
	} {
		os.WriteFile(path, []byte(body), 0600)
		_, err = Load(path)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe alert configuration", err)
		}
	}
}
