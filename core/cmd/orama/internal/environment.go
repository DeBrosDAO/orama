package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// Environment represents a Orama network environment
type Environment struct {
	Name        string `json:"name"`
	GatewayURL  string `json:"gateway_url"`
	Description string `json:"description"`
	IsActive    bool   `json:"is_active"`
	// CAFile is a PEM bundle trusted, in addition to the system roots, for
	// the gateway's domain and every name under it: a cluster on Let's
	// Encrypt staging or on a private CA. Only for this environment's domain.
	CAFile string `json:"ca_file,omitempty"`
	// Nodes are the machines `orama node setup` installed. Commands that
	// have to reach a node before the cluster's name resolves — delegation
	// is the one that has to — use this list. The gateway inventory replaces
	// it once the operator can log in.
	Nodes []EnvNode `json:"nodes,omitempty"`
}

// EnvNode is one machine recorded by setup: where to SSH, as whom, and
// whether it is a nameserver. Host is a public IPv4 address.
type EnvNode struct {
	Host string `json:"host"`
	User string `json:"user,omitempty"`
	Role string `json:"role,omitempty"`
}

// EnvironmentConfig stores all configured environments
type EnvironmentConfig struct {
	Environments      []Environment `json:"environments"`
	ActiveEnvironment string        `json:"active_environment"`
}

// noEnvironmentHelp is what a command says when this computer has no cluster
// configured. A fresh install does not point at anyone else's network.
const noEnvironmentHelp = "no environment is configured; add the cluster you use with `orama env add <name> https://<gateway>`"

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

	// A missing file is a computer that has not chosen a cluster. It is not
	// filled in with somebody else's networks.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &EnvironmentConfig{}, nil
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

	// Ensure config directory exists, with the same mode every other writer
	// uses. This directory sits next to credentials.json.
	configDir := filepath.Dir(path)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	if err := os.Chmod(configDir, 0700); err != nil {
		return fmt.Errorf("failed to secure config directory: %w", err)
	}

	data, err := json.MarshalIndent(envConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal environment config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write environment config: %w", err)
	}

	return nil
}

// UpsertEnvNode records a machine setup installed, replacing any earlier
// row for the same host. The environment must already exist.
func UpsertEnvNode(envName string, node EnvNode) error {
	node.Host = strings.TrimSpace(node.Host)
	node.User = strings.TrimSpace(node.User)
	node.Role = strings.TrimSpace(node.Role)
	if ip := net.ParseIP(node.Host); ip == nil || ip.To4() == nil {
		return fmt.Errorf("recorded node host %q is not a public IPv4 address", node.Host)
	}
	node.Host = net.ParseIP(node.Host).To4().String()
	if node.Role != "" && node.Role != "node" && node.Role != "nameserver" {
		return fmt.Errorf("role %q is not node or nameserver", node.Role)
	}
	cfg, err := LoadEnvironmentConfig()
	if err != nil {
		return err
	}
	for i := range cfg.Environments {
		if cfg.Environments[i].Name != envName {
			continue
		}
		nodes := cfg.Environments[i].Nodes
		replaced := false
		for j := range nodes {
			if nodes[j].Host == node.Host {
				nodes[j] = node
				replaced = true
				break
			}
		}
		if !replaced {
			nodes = append(nodes, node)
		}
		cfg.Environments[i].Nodes = nodes
		return SaveEnvironmentConfig(cfg)
	}
	return fmt.Errorf("environment %q is not configured; add it with `orama env add` before recording a node", envName)
}

// GetActiveEnvironment returns the currently active environment
func GetActiveEnvironment() (*Environment, error) {
	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		return nil, err
	}

	if len(envConfig.Environments) == 0 || envConfig.ActiveEnvironment == "" {
		return nil, fmt.Errorf("%s", noEnvironmentHelp)
	}

	for _, env := range envConfig.Environments {
		if env.Name == envConfig.ActiveEnvironment {
			return &env, nil
		}
	}

	// No fallback: silently using some other environment would send a
	// command to a cluster the operator did not choose.
	names := make([]string, 0, len(envConfig.Environments))
	for _, env := range envConfig.Environments {
		names = append(names, env.Name)
	}
	return nil, fmt.Errorf("active environment %q is not configured (configured: %s); choose one with `orama env use <name>` or pass --env",
		envConfig.ActiveEnvironment, strings.Join(names, ", "))
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
			// A CA is trusted for one domain. Pointing the environment at
			// another host drops it rather than carrying it to a domain
			// nobody chose to trust it for; pass --ca-file again to keep one.
			if !sameGatewayHost(env.GatewayURL, gatewayURL) {
				envConfig.Environments[i].CAFile = ""
			}
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

// RemoveEnvironment removes an environment by name. If it was the active one,
// nothing else is selected: the next command asks for `orama env use`.
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
		envConfig.ActiveEnvironment = ""
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

	envConfig := &EnvironmentConfig{}

	return SaveEnvironmentConfig(envConfig)
}

// SetEnvironmentCA records caFile as the CA trusted for the named
// environment's domain. The file is checked now, so a typo fails here rather
// than on the next command.
func SetEnvironmentCA(name, caFile string) error {
	abs, err := filepath.Abs(caFile)
	if err != nil {
		return fmt.Errorf("resolve CA file %s: %w", caFile, err)
	}
	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		return err
	}
	for i, env := range envConfig.Environments {
		if env.Name != name {
			continue
		}
		domain, err := gatewayDomain(env.GatewayURL)
		if err != nil {
			return err
		}
		if err := tlsutil.TrustCAForDomain(domain, abs); err != nil {
			return err
		}
		envConfig.Environments[i].CAFile = abs
		return SaveEnvironmentConfig(envConfig)
	}
	return fmt.Errorf("environment %q is not configured", name)
}

// TrustEnvironmentCAs trusts every configured environment's CA for that
// environment's domain, for all HTTPS this process makes. A CA file that has
// gone missing is an error naming the environment, not a silent downgrade to
// a connection that cannot verify.
func TrustEnvironmentCAs() error {
	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		return err
	}
	for _, env := range envConfig.Environments {
		if env.CAFile == "" {
			continue
		}
		domain, err := gatewayDomain(env.GatewayURL)
		if err != nil {
			return fmt.Errorf("environment %q: %w", env.Name, err)
		}
		if err := tlsutil.TrustCAForDomain(domain, env.CAFile); err != nil {
			return fmt.Errorf("environment %q: %w (fix it with `orama env add %s %s --ca-file <file>`)",
				env.Name, err, env.Name, env.GatewayURL)
		}
	}
	return tlsutil.InstallScopedRoots()
}

func gatewayDomain(gatewayURL string) (string, error) {
	u, err := url.Parse(gatewayURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("gateway URL %q has no host to scope a CA to", gatewayURL)
	}
	return u.Hostname(), nil
}

// sameGatewayHost reports whether two gateway URLs name the same host.
func sameGatewayHost(a, b string) bool {
	ha, errA := gatewayDomain(a)
	hb, errB := gatewayDomain(b)
	return errA == nil && errB == nil && strings.EqualFold(ha, hb)
}
