package management

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"bloomer/agent/internal/localfs"
)

// This is the last installation attempt, separate from install-state.json,
// which continues to describe only the last fully successful installation.
type installProgress struct {
	Version   string `json:"version"`
	State     string `json:"state"`
	Phase     string `json:"phase"`
	UpdatedAt string `json:"updated_at"`
}

func (m *Manager) writeProgress(p *installProgress) error {
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(m.Base, "install-progress.json"), append(data, '\n'), 0600)
}

func (m *Manager) installController(directory string) error {
	managementDir := filepath.Join(m.Base, "management")
	if err := localfs.PrivateDir(managementDir); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(directory, "bloomerctl"))
	if err != nil {
		return err
	}
	// Keep the verified recovery tool available even if preflight or rollback
	// fails. Replacing it atomically is also safe while an older CLI is running.
	return atomicWrite(filepath.Join(managementDir, "bloomerctl"), data, 0700)
}

func (m *Manager) addInstallStatus(result Result) (Result, error) {
	data, err := optionalFile(filepath.Join(m.Base, "install-progress.json"))
	if err != nil {
		return nil, err
	}
	if data != nil {
		var progress installProgress
		if err = json.Unmarshal(data, &progress); err != nil {
			return nil, fmt.Errorf("invalid installation progress: %w", err)
		}
		result["installation"] = progress
	}
	return result, nil
}
