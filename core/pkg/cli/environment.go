package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/config"
)

// Environment represents a Orama network environment
type Environment struct {
	Name        string `json:"name"`
	GatewayURL  string `json:"gateway_url"`
	Description string `json:"description"`
	IsActive    bool   `json:"is_active"`
}

// EnvironmentConfig stores all configured environments
type EnvironmentConfig struct {
	Environments      []Environment `json:"environments"`
	ActiveEnvironment string        `json:"active_environment"`
}

// Default environments
var DefaultEnvironments = []Environment{
	{
		Name:        "sandbox",
		GatewayURL:  "https://dbrs.space",
		Description: "Sandbox cluster (dbrs.space)",
		IsActive:    true,
	},
	{
		Name:        "devnet",
		GatewayURL:  "https://orama-devnet.network",
		Description: "Development network",
		IsActive:    false,
	},
	{
		Name:        "testnet",
		GatewayURL:  "https://orama-testnet.network",
		Description: "Test network (staging)",
		IsActive:    false,
	},
}

// getEnvironmentConfigPathFn is the function used to resolve the config path.
// Tests override this to point at a temp file.
var getEnvironmentConfigPathFn = getEnvironmentConfigPathDefault

func getEnvironmentConfigPathDefault() (string, error) {
	configDir, err := config.ConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config directory: %w", err)
	}
	return filepath.Join(configDir, "environments.json"), nil
}

// GetEnvironmentConfigPath returns the path to the environment config file
func GetEnvironmentConfigPath() (string, error) {
	return getEnvironmentConfigPathFn()
}

// LoadEnvironmentConfig loads the environment configuration
func LoadEnvironmentConfig() (*EnvironmentConfig, error) {
	path, err := GetEnvironmentConfigPath()
	if err != nil {
		return nil, err
	}

	// If file doesn't exist, return default config
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &EnvironmentConfig{
			Environments:      DefaultEnvironments,
			ActiveEnvironment: "sandbox",
		}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read environment config: %w", err)
	}

	var envConfig EnvironmentConfig
	if err := json.Unmarshal(data, &envConfig); err != nil {
		return nil, fmt.Errorf("failed to parse environment config: %w", err)
	}

	return &envConfig, nil
}

// SaveEnvironmentConfig saves the environment configuration
func SaveEnvironmentConfig(envConfig *EnvironmentConfig) error {
	path, err := GetEnvironmentConfigPath()
	if err != nil {
		return err
	}

	// Ensure config directory exists
	configDir := filepath.Dir(path)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(envConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal environment config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write environment config: %w", err)
	}

	return nil
}

// GetActiveEnvironment returns the currently active environment
func GetActiveEnvironment() (*Environment, error) {
	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		return nil, err
	}

	for _, env := range envConfig.Environments {
		if env.Name == envConfig.ActiveEnvironment {
			return &env, nil
		}
	}

	// Fallback to sandbox if active environment not found
	for _, env := range envConfig.Environments {
		if env.Name == "sandbox" {
			return &env, nil
		}
	}

	return nil, fmt.Errorf("no active environment found")
}

// SwitchEnvironment switches to a different environment
func SwitchEnvironment(name string) error {
	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		return err
	}

	// Check if environment exists
	found := false
	for _, env := range envConfig.Environments {
		if env.Name == name {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("environment '%s' not found", name)
	}

	envConfig.ActiveEnvironment = name
	return SaveEnvironmentConfig(envConfig)
}

// GetEnvironmentByName returns an environment by name
func GetEnvironmentByName(name string) (*Environment, error) {
	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		return nil, err
	}

	for _, env := range envConfig.Environments {
		if env.Name == name {
			return &env, nil
		}
	}

	return nil, fmt.Errorf("environment '%s' not found", name)
}

// AddEnvironment adds a new environment or updates an existing one.
// If an environment with the same name already exists, its gateway URL and
// description are updated in place.
func AddEnvironment(name, gatewayURL, description string) error {
	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		return err
	}

	for i, env := range envConfig.Environments {
		if env.Name == name {
			envConfig.Environments[i].GatewayURL = gatewayURL
			envConfig.Environments[i].Description = description
			return SaveEnvironmentConfig(envConfig)
		}
	}

	envConfig.Environments = append(envConfig.Environments, Environment{
		Name:        name,
		GatewayURL:  gatewayURL,
		Description: description,
	})

	return SaveEnvironmentConfig(envConfig)
}

// RemoveEnvironment removes an environment by name. If the removed environment
// was active, the active environment falls back to "devnet".
func RemoveEnvironment(name string) error {
	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		return err
	}

	newEnvs := make([]Environment, 0, len(envConfig.Environments))
	found := false
	for _, env := range envConfig.Environments {
		if env.Name == name {
			found = true
			continue
		}
		newEnvs = append(newEnvs, env)
	}

	if !found {
		return nil // already absent, nothing to do
	}

	envConfig.Environments = newEnvs

	if envConfig.ActiveEnvironment == name {
		envConfig.ActiveEnvironment = "devnet"
	}

	return SaveEnvironmentConfig(envConfig)
}

// InitializeEnvironments initializes the environment config with defaults
func InitializeEnvironments() error {
	path, err := GetEnvironmentConfigPath()
	if err != nil {
		return err
	}

	// Don't overwrite existing config
	if _, err := os.Stat(path); err == nil {
		return nil
	}

	envConfig := &EnvironmentConfig{
		Environments:      DefaultEnvironments,
		ActiveEnvironment: "sandbox",
	}

	return SaveEnvironmentConfig(envConfig)
}
