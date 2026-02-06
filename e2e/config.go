//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v2"
)

// E2EConfig holds the configuration for E2E tests
type E2EConfig struct {
	// Mode can be "local" or "production"
	Mode string `yaml:"mode"`

	// BaseDomain is the domain used for deployment routing (e.g., "dbrs.space" or "orama.network")
	BaseDomain string `yaml:"base_domain"`

	// Servers is a list of production servers (only used when mode=production)
	Servers []ServerConfig `yaml:"servers"`

	// Nameservers is a list of nameserver hostnames (e.g., ["ns1.dbrs.space", "ns2.dbrs.space"])
	Nameservers []string `yaml:"nameservers"`

	// APIKey is the API key for production testing (auto-discovered if empty)
	APIKey string `yaml:"api_key"`
}

// ServerConfig holds configuration for a single production server
type ServerConfig struct {
	Name         string `yaml:"name"`
	IP           string `yaml:"ip"`
	User         string `yaml:"user"`
	Password     string `yaml:"password"`
	IsNameserver bool   `yaml:"is_nameserver"`
}

// DefaultConfig returns the default configuration for local development
func DefaultConfig() *E2EConfig {
	return &E2EConfig{
		Mode:        "local",
		BaseDomain:  "orama.network",
		Servers:     []ServerConfig{},
		Nameservers: []string{},
		APIKey:      "",
	}
}

// LoadE2EConfig loads the E2E test configuration from e2e/config.yaml
// Falls back to defaults if the file doesn't exist
func LoadE2EConfig() (*E2EConfig, error) {
	// Try multiple locations for the config file
	configPaths := []string{
		"config.yaml",       // Relative to e2e directory (when running from e2e/)
		"e2e/config.yaml",   // Relative to project root
		"../e2e/config.yaml", // From subdirectory within e2e/
	}

	// Also try absolute path based on working directory
	if cwd, err := os.Getwd(); err == nil {
		configPaths = append(configPaths, filepath.Join(cwd, "config.yaml"))
		configPaths = append(configPaths, filepath.Join(cwd, "e2e", "config.yaml"))
		// Go up one level if we're in a subdirectory
		configPaths = append(configPaths, filepath.Join(cwd, "..", "config.yaml"))
	}

	var configData []byte
	var readErr error

	for _, path := range configPaths {
		data, err := os.ReadFile(path)
		if err == nil {
			configData = data
			break
		}
		readErr = err
	}

	// If no config file found, return defaults
	if configData == nil {
		// Check if running in production mode via environment variable
		if os.Getenv("E2E_MODE") == "production" {
			return nil, readErr // Config file required for production mode
		}
		return DefaultConfig(), nil
	}

	var cfg E2EConfig
	if err := yaml.Unmarshal(configData, &cfg); err != nil {
		return nil, err
	}

	// Apply defaults for empty values
	if cfg.Mode == "" {
		cfg.Mode = "local"
	}
	if cfg.BaseDomain == "" {
		cfg.BaseDomain = "orama.network"
	}

	return &cfg, nil
}

// IsProductionMode returns true if running in production mode
func IsProductionMode() bool {
	// Check environment variable first
	if os.Getenv("E2E_MODE") == "production" {
		return true
	}

	cfg, err := LoadE2EConfig()
	if err != nil {
		return false
	}
	return cfg.Mode == "production"
}

// IsLocalMode returns true if running in local mode
func IsLocalMode() bool {
	return !IsProductionMode()
}

// SkipIfLocal skips the test if running in local mode
// Use this for tests that require real production infrastructure
func SkipIfLocal(t *testing.T) {
	t.Helper()
	if IsLocalMode() {
		t.Skip("Skipping: requires production environment (set mode: production in e2e/config.yaml)")
	}
}

// SkipIfProduction skips the test if running in production mode
// Use this for tests that should only run locally
func SkipIfProduction(t *testing.T) {
	t.Helper()
	if IsProductionMode() {
		t.Skip("Skipping: local-only test")
	}
}

// GetServerIPs returns a list of all server IP addresses from config
func GetServerIPs(cfg *E2EConfig) []string {
	if cfg == nil {
		return nil
	}

	ips := make([]string, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		if server.IP != "" {
			ips = append(ips, server.IP)
		}
	}
	return ips
}

// GetNameserverServers returns servers configured as nameservers
func GetNameserverServers(cfg *E2EConfig) []ServerConfig {
	if cfg == nil {
		return nil
	}

	var nameservers []ServerConfig
	for _, server := range cfg.Servers {
		if server.IsNameserver {
			nameservers = append(nameservers, server)
		}
	}
	return nameservers
}
