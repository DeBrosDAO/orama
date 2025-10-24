package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfigDir returns the path to the DeBros config directory (~/.debros).
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to determine home directory: %w", err)
	}
	return filepath.Join(home, ".debros"), nil
}

// EnsureConfigDir creates the config directory if it does not exist.
func EnsureConfigDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create config directory %s: %w", dir, err)
	}
	return dir, nil
}

// DefaultPath returns the path to the config file for the given component name.
// component should be e.g., "node.yaml", "bootstrap.yaml", "gateway.yaml"
func DefaultPath(component string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, component), nil
}
