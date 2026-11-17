package workspace

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// Success markers are written only by Gateway after verifying hook provenance.
// Each install has its own marker: late events cannot mark a newer install done.
func hookSuccessPath(dir, agent, installationID string) string {
	sum := sha256.Sum256([]byte(agent + "\x00" + installationID))
	return filepath.Join(dir, "hook-success", fmt.Sprintf("%x", sum))
}

func RecordHookSuccess(dir, agent, installationID string) error {
	if dir == "" || agent != "codex" || installationID == "" {
		return nil
	}
	path := hookSuccessPath(dir, agent, installationID)
	if HookSucceeded(dir, agent, installationID) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("verified\n"), 0600)
}

func HookSucceeded(dir, agent, installationID string) bool {
	if dir == "" || agent != "codex" || installationID == "" {
		return false
	}
	raw, err := os.ReadFile(hookSuccessPath(dir, agent, installationID))
	return err == nil && string(raw) == "verified\n"
}
