package cli

import (
	"encoding/json"
	"errors"
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
	// Network is the registry network (see pkg/netregistry) this gateway's
	// cluster runs on. Empty when the cluster belongs to none.
	Network string `json:"network,omitempty"`
	// CAFile is a PEM bundle trusted, in addition to the system roots, for
	// the gateway's domain and every name under it: a cluster on Let's
	// Encrypt staging or on a private CA. Only for this environment's domain.
	CAFile string `json:"ca_file,omitempty"`
	// Nodes are the machines `orama node setup` installed. Commands that
	// have to reach a node before the cluster's name resolves — delegation
	// is the one that has to — use this list. The gateway inventory replaces
	// it once the operator can log in.
	Nodes []EnvNode `json:"nodes,omitempty"`
	// Delegations are the results of the last `orama node dns delegation`
	// check, one per cluster domain.
	Delegations []DelegationStatus `json:"delegations,omitempty"`
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

// ErrNoActiveNetwork is wrapped by GetActiveEnvironment when no network is configured, or the one
// marked active is not in the list: nothing is selected, as against a configuration that cannot be
// read.
var ErrNoActiveNetwork = errors.New("no active network")

// noEnvironmentHelp is what a command says when this computer has no cluster
// configured. A fresh install does not point at anyone else's network.
const noEnvironmentHelp = "no network is configured; add the cluster you use with `orama network add <name> https://<gateway>`"

const (
	// environmentLockSuffix names the lock file beside environments.json.
	environmentLockSuffix = ".lock"
	// environmentFilePerm is the mode of environments.json and its lock: the
	// file sits beside credentials.json and names the clusters an operator uses.
	environmentFilePerm = 0o600
	environmentDirPerm  = 0o700
)

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

// writeEnvironmentConfig replaces environments.json with envConfig. The caller
// holds the environment lock.
//
// The file is written whole beside the config and renamed over it, so a reader
// sees the old file or the new one and never half of either, and a crash leaves
// the old one rather than a truncated file every later command refuses to parse.
func writeEnvironmentConfig(path string, envConfig *EnvironmentConfig) error {
	data, err := json.MarshalIndent(envConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal environment config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create a temporary environment config: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(environmentFilePerm); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to secure the temporary environment config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write environment config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to flush environment config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write environment config: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("failed to replace environment config: %w", err)
	}
	return nil
}

// updateEnvironmentConfig is the one way environments.json changes: under an
// exclusive lock shared by every process, it loads the file, lets mutate change
// it, and writes it back. Read-modify-write without the lock loses an
// environment whenever two commands overlap (a CI script adding several
// clusters in parallel). A mutate that returns an error leaves the file as it
// was.
func updateEnvironmentConfig(mutate func(*EnvironmentConfig) error) (err error) {
	path, err := GetEnvironmentConfigPath()
	if err != nil {
		return err
	}

	// The directory sits next to credentials.json and gets the same mode.
	configDir := filepath.Dir(path)
	if err := os.MkdirAll(configDir, environmentDirPerm); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	if err := os.Chmod(configDir, environmentDirPerm); err != nil {
		return fmt.Errorf("failed to secure config directory: %w", err)
	}

	unlock, err := lockEnvironmentConfig(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := unlock(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to release the environment lock: %w", closeErr)
		}
	}()

	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		return err
	}
	if err := mutate(envConfig); err != nil {
		return err
	}
	return writeEnvironmentConfig(path, envConfig)
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
	return updateEnvironmentConfig(func(cfg *EnvironmentConfig) error {
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
			return nil
		}
		return fmt.Errorf("network %q is not configured; add it with `orama network add` before recording a node", envName)
	})
}

// GetActiveEnvironment returns the currently active environment
func GetActiveEnvironment() (*Environment, error) {
	envConfig, err := LoadEnvironmentConfig()
	if err != nil {
		return nil, err
	}

	if len(envConfig.Environments) == 0 || envConfig.ActiveEnvironment == "" {
		return nil, fmt.Errorf("%w: %s", ErrNoActiveNetwork, noEnvironmentHelp)
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
	return nil, fmt.Errorf("%w: %q is not configured (configured: %s); choose one with `orama network use <name>` or pass --env",
		ErrNoActiveNetwork, envConfig.ActiveEnvironment, strings.Join(names, ", "))
}

// SwitchEnvironment switches to a different environment
func SwitchEnvironment(name string) error {
	return updateEnvironmentConfig(func(envConfig *EnvironmentConfig) error {
		for _, env := range envConfig.Environments {
			if env.Name == name {
				envConfig.ActiveEnvironment = name
				return nil
			}
		}
		return fmt.Errorf("network '%s' not found", name)
	})
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

	return nil, fmt.Errorf("network '%s' not found", name)
}

// AddEnvironment adds a new environment or updates an existing one.
// If an environment with the same name already exists, its gateway URL,
// description and, when one is given, registry network are updated in place.
func AddEnvironment(name, gatewayURL, description string) error {
	return AddEnvironmentOn(name, gatewayURL, description, "")
}

// AddEnvironmentOn is AddEnvironment for a cluster that runs on a registry
// network. An empty network leaves an existing environment's network as it is.
func AddEnvironmentOn(name, gatewayURL, description, network string) error {
	return updateEnvironmentConfig(func(envConfig *EnvironmentConfig) error {
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
				if network != "" {
					envConfig.Environments[i].Network = network
				}
				return nil
			}
		}

		envConfig.Environments = append(envConfig.Environments, Environment{
			Name:        name,
			GatewayURL:  gatewayURL,
			Description: description,
			Network:     network,
		})
		return nil
	})
}

// RemoveEnvironment removes an environment by name. If it was the active one,
// nothing else is selected: the next command asks for `orama network use`.
func RemoveEnvironment(name string) error {
	return updateEnvironmentConfig(func(envConfig *EnvironmentConfig) error {
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
		return nil
	})
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

	// A file another command wrote between the check and the lock is loaded
	// and written back as it is.
	return updateEnvironmentConfig(func(*EnvironmentConfig) error { return nil })
}

// SetEnvironmentCA records caFile as the CA trusted for the named
// environment's domain. The file is checked now, so a typo fails here rather
// than on the next command.
func SetEnvironmentCA(name, caFile string) error {
	abs, err := filepath.Abs(caFile)
	if err != nil {
		return fmt.Errorf("resolve CA file %s: %w", caFile, err)
	}
	return updateEnvironmentConfig(func(envConfig *EnvironmentConfig) error {
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
			return nil
		}
		return fmt.Errorf("network %q is not configured", name)
	})
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
			return fmt.Errorf("environment %q: %w (fix it with `orama network add %s %s --ca-file <file>`)",
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
