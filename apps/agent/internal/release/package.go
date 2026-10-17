// Package release defines the files and checksums shared by the DMG builder and installer.
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var Files = []string{"bloomer-agent", "bloomerctl", "config.example.json", "dev.bloomer.agent.plist.template", "openapi.yaml", "INSTALL.md"}
var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$`)

type Manifest struct {
	Version  string            `json:"version"`
	Build    string            `json:"build"`
	Platform string            `json:"platform"`
	SHA256   map[string]string `json:"sha256"`
}

func ValidVersion(version string) bool { return versionPattern.MatchString(version) }

func Hash(path string) (string, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", errors.New("release file must be regular and not a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func Write(directory, version, build string) error {
	if !ValidVersion(version) {
		return errors.New("invalid release version")
	}
	m := Manifest{version, build, "darwin/arm64", make(map[string]string)}
	sums := ""
	for _, name := range Files {
		digest, err := Hash(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		m.SHA256[name] = digest
		sums += digest + "  " + name + "\n"
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(directory, "manifest.json"), append(b, '\n'), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(sums), 0644)
}

func Verify(directory string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return m, errors.New("invalid release manifest")
	}
	if !ValidVersion(m.Version) || m.Platform != "darwin/arm64" || len(m.SHA256) != len(Files) {
		return m, errors.New("invalid or incomplete release manifest")
	}
	for _, name := range Files {
		digest, ok := m.SHA256[name]
		if !ok {
			return m, fmt.Errorf("missing release file: %s", name)
		}
		actual, err := Hash(filepath.Join(directory, name))
		if err != nil {
			return m, err
		}
		if actual != digest {
			return m, fmt.Errorf("package checksum mismatch: %s", name)
		}
	}
	return m, nil
}
