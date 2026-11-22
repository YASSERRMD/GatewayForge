package syncer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// SaveLKG atomically writes a verified snapshot to path using temp-file + rename.
func SaveLKG(path string, snap snapshot.Snapshot) error {
	if path == "" {
		return nil
	}
	if err := snap.VerifyChecksum(); err != nil {
		return fmt.Errorf("refusing to persist unverified LKG snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create LKG dir: %w", err)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal LKG snapshot: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write LKG temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename LKG file: %w", err)
	}
	return nil
}

// LoadLKG reads and strictly checksum-verifies a persisted last-known-good snapshot from disk.
func LoadLKG(path string) (snapshot.Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	var snap snapshot.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return snapshot.Snapshot{}, fmt.Errorf("decode LKG snapshot: %w", err)
	}
	if err := snap.VerifyChecksum(); err != nil {
		return snapshot.Snapshot{}, fmt.Errorf("verify LKG snapshot: %w", err)
	}
	return snap, nil
}
