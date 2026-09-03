package sandbox

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds sandbox configuration, stored at ~/.orama/sandbox.yaml.
type Config struct {
	HetznerAPIToken string       `yaml:"hetzner_api_token"`
	Domain          string       `yaml:"domain"`
	Location        string       `yaml:"location"`    // Hetzner datacenter (default: fsn1)
	ServerType      string       `yaml:"server_type"` // Hetzner server type (default: cx22)
	FloatingIPs     []FloatIP    `yaml:"floating_ips"`
	SSHKey          SSHKeyConfig `yaml:"ssh_key"`
	FirewallID      int64        `yaml:"firewall_id,omitempty"` // Hetzner firewall resource ID
}

// FloatIP holds a Hetzner floating IP reference.
type FloatIP struct {
	ID int64  `yaml:"id"`
	IP string `yaml:"ip"`
}

// SSHKeyConfig holds the wallet vault target and Hetzner resource ID.
type SSHKeyConfig struct {
	HetznerID   int64  `yaml:"hetzner_id"`
	VaultTarget string `yaml:"vault_target"` // e.g. "sandbox/root"
}

// configDir returns ~/.orama/, creating it if needed.
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	dir := filepath.Join(home, ".orama")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	return dir, nil
}

// configPath returns the full path to ~/.orama/sandbox.yaml.
func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sandbox.yaml"), nil
}

// LoadConfig reads the sandbox config from ~/.orama/sandbox.yaml.
// Returns an error if the file doesn't exist (user must run setup first).
func LoadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("sandbox not configured, run: orama sandbox setup")
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	cfg.Defaults()

	return &cfg, nil
}

// SaveConfig writes the sandbox config to ~/.orama/sandbox.yaml.
func SaveConfig(cfg *Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// validate checks that required fields are present.
func (c *Config) validate() error {
	if c.HetznerAPIToken == "" {
		return fmt.Errorf("hetzner_api_token is required")
	}
	if c.Domain == "" {
		return fmt.Errorf("domain is required")
	}
	if len(c.FloatingIPs) < 2 {
		return fmt.Errorf("2 floating IPs required, got %d", len(c.FloatingIPs))
	}
	if c.SSHKey.VaultTarget == "" {
		return fmt.Errorf("ssh_key.vault_target is required (run: orama sandbox setup)")
	}
	return nil
}

// Defaults fills in default values for optional fields.
func (c *Config) Defaults() {
	if c.Location == "" {
		c.Location = "nbg1"
	}
	if c.ServerType == "" {
		c.ServerType = "cx23"
	}
	if c.SSHKey.VaultTarget == "" {
		c.SSHKey.VaultTarget = "sandbox/root"
	}
}
