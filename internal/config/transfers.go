package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// PendingTransfer is an in-progress download persisted across runs so rtr can
// auto-resume it on the next launch. It is stored as transfers.json beside the
// config file.
type PendingTransfer struct {
	Bookmark Bookmark `json:"bookmark"`
	Sources  []string `json:"sources"`
	Dest     string   `json:"dest"`
}

// TransfersPath returns the resume file located beside the given config file.
// An empty configPath (e.g. an in-memory default config) yields "", which the
// load/save helpers treat as a no-op so tests never touch the filesystem.
func TransfersPath(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "transfers.json")
}

// LoadPendingTransfers reads the resume file; a missing file is not an error.
func LoadPendingTransfers(path string) ([]PendingTransfer, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ts []PendingTransfer
	if err := json.Unmarshal(data, &ts); err != nil {
		return nil, err
	}
	return ts, nil
}

// SavePendingTransfers writes the resume file atomically, removing it when there
// is nothing left to resume.
func SavePendingTransfers(path string, ts []PendingTransfer) error {
	if path == "" {
		return nil
	}
	if len(ts) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ts, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
