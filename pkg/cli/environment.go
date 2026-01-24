package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/config"
)

// Environment represents a DeBros network environment
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
		Name:        "local",
		GatewayURL:  "http://localhost:6001",
		Description: "Local development environment (node-1)",
		IsActive:    true,
	},
	{
		Name:        "production",
		GatewayURL:  "http://dbrs.space",
		Description: "Production network (dbrs.space)",
		IsActive:    false,
	},
	{
		Name:        "devnet",
		GatewayURL:  "https://devnet.orama.network",
		Description: "Development network (testnet)",
		IsActive:    false,
	},
	{
		Name:        "testnet",
		GatewayURL:  "https://testnet.orama.network",
		Description: "Test network (staging)",
		IsActive:    false,
	},
}

// GetEnvironmentConfigPath returns the path to the environment config file
func GetEnvironmentConfigPath() (string, error) {
	configDir, err := config.ConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config directory: %w", err)
	}
	return filepath.Join(configDir, "environments.json"), nil
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
			ActiveEnvironment: "local",
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

	// Fallback to local if active environment not found
	for _, env := range envConfig.Environments {
		if env.Name == "local" {
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
		ActiveEnvironment: "local",
	}

	return SaveEnvironmentConfig(envConfig)
}
