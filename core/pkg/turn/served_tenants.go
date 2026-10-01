package turn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

// DefaultServedTenantsPath is where the server publishes what it has loaded,
// after every successful tenant load, so a host can ask the LIVE server — not
// the config file — who it serves. It is under orama-turn.service's
// RuntimeDirectory, so systemd removes it whenever the unit stops and a dead
// server leaves no claim behind.
const DefaultServedTenantsPath = "/run/orama-turn/served-tenants.json"

// servedTenantsFileMode: the file names namespaces only, never a secret; the
// gateway (a different process, same orama user and group) reads it.
const servedTenantsFileMode = 0o640

// ServedTenants is what the running server last loaded: its tenant namespaces
// and the digest of the config bytes they came from. The digest lets a reader
// tell "serves the config I just wrote" from "serves an older one".
type ServedTenants struct {
	Namespaces   []string `json:"namespaces"`
	ConfigSHA256 string   `json:"config_sha256"`
}

// ConfigDigest is the digest recorded in ServedTenants for config bytes.
func ConfigDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ReadServedTenants reads what the running server last loaded. A missing file
// is an error: the server has not loaded the config yet.
func ReadServedTenants(path string) (*ServedTenants, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read served tenants %s: %w", path, err)
	}
	var st ServedTenants
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse served tenants %s: %w", path, err)
	}
	return &st, nil
}

// writeServedTenants publishes the live tenant set atomically.
func writeServedTenants(path string, st ServedTenants) error {
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("marshal served tenants: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, servedTenantsFileMode); err != nil {
		return fmt.Errorf("write served tenants %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("publish served tenants %s: %w", path, err)
	}
	return nil
}
