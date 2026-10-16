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
	for _, body := range []string{`null`, `{} {}`, `{"token":"secret"}`, `{"listen":"0.0.0.0:17890"}`, `{"listen":"localhost:17890"}`, `{"listen":"127.0.0.1:0"}`, `{"upstream_url":"https://user:secret@example.com/v1/provider/account-earnings"}`, `{"upstream_url":"http://example.com/v1/provider/account-earnings"}`, `{"data_dir":"~other/state"}`} {
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
