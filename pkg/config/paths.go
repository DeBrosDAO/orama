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
// It checks both ~/.debros/ and ~/.debros/configs/ for backward compatibility.
// If component is already an absolute path, it returns it as-is.
func DefaultPath(component string) (string, error) {
	// If component is already an absolute path, return it directly
	if filepath.IsAbs(component) {
		return component, nil
	}

	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}

	// First check in ~/.debros/configs/ (production installer location)
	configsPath := filepath.Join(dir, "configs", component)
	if _, err := os.Stat(configsPath); err == nil {
		return configsPath, nil
	}

	// Fallback to ~/.debros/ (legacy/development location)
	legacyPath := filepath.Join(dir, component)
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath, nil
	}

	// Return configs path as default (even if it doesn't exist yet)
	// This allows the error message to show the expected production location
	return configsPath, nil
}
