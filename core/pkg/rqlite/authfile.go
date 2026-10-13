package rqlite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AuthFileName is the auth file's name inside an instance data directory.
const AuthFileName = "rqlite-auth.json"

// InstallAuthFile copies the cluster rqlite auth JSON into the instance data
// directory so rqlited can read it. The systemd unit sets InaccessiblePaths on
// secrets/, so -auth must not point at that tree. Empty or missing source is
// a start error: rqlited is not allowed to come up without credentials.
func InstallAuthFile(src, dataDir string) (dest string, err error) {
	if strings.TrimSpace(src) == "" {
		return "", fmt.Errorf("rqlite auth file is required")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("read rqlite auth file %s: %w", src, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return "", fmt.Errorf("rqlite auth file %s is empty", src)
	}
	if err := os.MkdirAll(dataDir, 0750); err != nil {
		return "", fmt.Errorf("create rqlite data dir %s: %w", dataDir, err)
	}
	dest = filepath.Join(dataDir, AuthFileName)
	if err := os.WriteFile(dest, data, 0600); err != nil {
		return "", fmt.Errorf("write rqlite auth file %s: %w", dest, err)
	}
	return dest, nil
}

// JoinUser returns the user in the auth file that rqlited should join as: the
// first one allowed to join ("join" or "all"). rqlited started with -auth
// refuses an anonymous join, so -join without -join-as is always
// "unauthorized".
func JoinUser(authFile string) (string, error) {
	raw, err := os.ReadFile(authFile)
	if err != nil {
		return "", fmt.Errorf("read rqlite auth file %s: %w", authFile, err)
	}
	var entries []struct {
		Username string   `json:"username"`
		Perms    []string `json:"perms"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", fmt.Errorf("parse rqlite auth file %s: %w", authFile, err)
	}
	for _, e := range entries {
		if e.Username == "" {
			continue
		}
		for _, p := range e.Perms {
			if p == "all" || p == "join" {
				return e.Username, nil
			}
		}
	}
	return "", fmt.Errorf("rqlite auth file %s has no user with the join permission; a joining node would be refused", authFile)
}
