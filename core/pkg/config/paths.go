package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandPath expands environment variables and ~ in a path.
func ExpandPath(path string) (string, error) {
	path = os.ExpandEnv(path)
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to determine home directory: %w", err)
		}
		path = filepath.Join(home, path[1:])
	}
	return path, nil
}

// ConfigDir returns the path to the Orama config directory (~/.orama).
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to determine home directory: %w", err)
	}
	return filepath.Join(home, ".orama"), nil
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
	// MkdirAll leaves an existing directory's mode alone, and an older release
	// created this one 0755 from the `orama env` path. The directory holds
	// credentials, so repair the mode rather than depending on whichever
	// command happened to create it first.
	if err := os.Chmod(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to secure config directory %s: %w", dir, err)
	}
	return dir, nil
}

// DefaultPath returns the path to the config file for the given component name.
// component should be e.g., "node.yaml", "gateway.yaml"
// It checks ~/.orama/data/, ~/.orama/configs/, and ~/.orama/ for backward compatibility.
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

	var gatewayDefault string
	// For gateway.yaml, check data/ directory first (production location)
	if component == "gateway.yaml" {
		dataPath := filepath.Join(dir, "data", component)
		if _, err := os.Stat(dataPath); err == nil {
			return dataPath, nil
		}
		// Remember the preferred default so we can still fall back to legacy paths
		gatewayDefault = dataPath
	}

	// First check in ~/.orama/configs/ (production installer location)
	configsPath := filepath.Join(dir, "configs", component)
	if _, err := os.Stat(configsPath); err == nil {
		return configsPath, nil
	}

	// Fallback to ~/.orama/ (legacy/development location)
	legacyPath := filepath.Join(dir, component)
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath, nil
	}

	if gatewayDefault != "" {
		// If we preferred the data path (gateway.yaml) but didn't find it anywhere else,
		// return the data path so error messages point to the production location.
		return gatewayDefault, nil
	}

	// Return configs path as default (even if it doesn't exist yet)
	// This allows the error message to show the expected production location
	return configsPath, nil
}
