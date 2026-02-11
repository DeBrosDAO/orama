package namespace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// SystemdSpawner spawns namespace cluster processes using systemd services
type SystemdSpawner struct {
	systemdMgr    *systemd.Manager
	namespaceBase string
	logger        *zap.Logger
}

// NewSystemdSpawner creates a new systemd-based spawner
func NewSystemdSpawner(namespaceBase string, logger *zap.Logger) *SystemdSpawner {
	return &SystemdSpawner{
		systemdMgr:    systemd.NewManager(namespaceBase, logger),
		namespaceBase: namespaceBase,
		logger:        logger.With(zap.String("component", "systemd-spawner")),
	}
}

// SpawnRQLite starts a RQLite instance using systemd
func (s *SystemdSpawner) SpawnRQLite(ctx context.Context, namespace, nodeID string, cfg rqlite.InstanceConfig) error {
	s.logger.Info("Spawning RQLite via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Build join arguments
	joinArgs := ""
	if len(cfg.JoinAddresses) > 0 {
		joinArgs = fmt.Sprintf("-join %s", cfg.JoinAddresses[0])
		for _, addr := range cfg.JoinAddresses[1:] {
			joinArgs += fmt.Sprintf(",%s", addr)
		}
	}

	// Generate environment file
	envVars := map[string]string{
		"HTTP_ADDR":     fmt.Sprintf("0.0.0.0:%d", cfg.HTTPPort),
		"RAFT_ADDR":     fmt.Sprintf("0.0.0.0:%d", cfg.RaftPort),
		"HTTP_ADV_ADDR": cfg.HTTPAdvAddress,
		"RAFT_ADV_ADDR": cfg.RaftAdvAddress,
		"JOIN_ARGS":     joinArgs,
		"NODE_ID":       nodeID,
	}

	if err := s.systemdMgr.GenerateEnvFile(namespace, nodeID, systemd.ServiceTypeRQLite, envVars); err != nil {
		return fmt.Errorf("failed to generate RQLite env file: %w", err)
	}

	// Start the systemd service
	if err := s.systemdMgr.StartService(namespace, systemd.ServiceTypeRQLite); err != nil {
		return fmt.Errorf("failed to start RQLite service: %w", err)
	}

	// Wait for service to be active
	if err := s.waitForService(namespace, systemd.ServiceTypeRQLite, 30*time.Second); err != nil {
		return fmt.Errorf("RQLite service did not become active: %w", err)
	}

	s.logger.Info("RQLite spawned successfully via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return nil
}

// SpawnOlric starts an Olric instance using systemd
func (s *SystemdSpawner) SpawnOlric(ctx context.Context, namespace, nodeID string, cfg olric.InstanceConfig) error {
	s.logger.Info("Spawning Olric via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Validate BindAddr: 0.0.0.0 or empty causes IPv6 resolution on dual-stack hosts,
	// breaking memberlist UDP gossip over WireGuard. Resolve from wg0 as fallback.
	if cfg.BindAddr == "" || cfg.BindAddr == "0.0.0.0" {
		wgIP, err := getWireGuardIP()
		if err != nil {
			return fmt.Errorf("Olric BindAddr is %q and failed to detect WireGuard IP: %w", cfg.BindAddr, err)
		}
		s.logger.Warn("Olric BindAddr was invalid, resolved from wg0",
			zap.String("original", cfg.BindAddr),
			zap.String("resolved", wgIP),
			zap.String("namespace", namespace))
		cfg.BindAddr = wgIP
		if cfg.AdvertiseAddr == "" || cfg.AdvertiseAddr == "0.0.0.0" {
			cfg.AdvertiseAddr = wgIP
		}
	}

	// Create config directory
	configDir := filepath.Join(s.namespaceBase, namespace, "configs")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, fmt.Sprintf("olric-%s.yaml", nodeID))

	// Generate Olric YAML config
	type olricServerConfig struct {
		BindAddr string `yaml:"bindAddr"`
		BindPort int    `yaml:"bindPort"`
	}
	type olricMemberlistConfig struct {
		Environment string   `yaml:"environment"`
		BindAddr    string   `yaml:"bindAddr"`
		BindPort    int      `yaml:"bindPort"`
		Peers       []string `yaml:"peers,omitempty"`
	}
	type olricConfig struct {
		Server         olricServerConfig     `yaml:"server"`
		Memberlist     olricMemberlistConfig `yaml:"memberlist"`
		PartitionCount uint64                `yaml:"partitionCount"`
	}

	config := olricConfig{
		Server: olricServerConfig{
			BindAddr: cfg.BindAddr,
			BindPort: cfg.HTTPPort,
		},
		Memberlist: olricMemberlistConfig{
			Environment: "lan",
			BindAddr:    cfg.BindAddr,
			BindPort:    cfg.MemberlistPort,
			Peers:       cfg.PeerAddresses,
		},
		PartitionCount: 12, // Optimized for namespace clusters (vs 256 default)
	}

	configBytes, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal Olric config: %w", err)
	}

	if err := os.WriteFile(configPath, configBytes, 0644); err != nil {
		return fmt.Errorf("failed to write Olric config: %w", err)
	}

	s.logger.Info("Created Olric config file",
		zap.String("path", configPath),
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Generate environment file with Olric config path
	envVars := map[string]string{
		"OLRIC_SERVER_CONFIG": configPath,
	}

	if err := s.systemdMgr.GenerateEnvFile(namespace, nodeID, systemd.ServiceTypeOlric, envVars); err != nil {
		return fmt.Errorf("failed to generate Olric env file: %w", err)
	}

	// Start the systemd service
	if err := s.systemdMgr.StartService(namespace, systemd.ServiceTypeOlric); err != nil {
		return fmt.Errorf("failed to start Olric service: %w", err)
	}

	// Wait for service to be active
	if err := s.waitForService(namespace, systemd.ServiceTypeOlric, 30*time.Second); err != nil {
		return fmt.Errorf("Olric service did not become active: %w", err)
	}

	s.logger.Info("Olric spawned successfully via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return nil
}

// SpawnGateway starts a Gateway instance using systemd
func (s *SystemdSpawner) SpawnGateway(ctx context.Context, namespace, nodeID string, cfg gateway.InstanceConfig) error {
	s.logger.Info("Spawning Gateway via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Create config directory
	configDir := filepath.Join(s.namespaceBase, namespace, "configs")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, fmt.Sprintf("gateway-%s.yaml", nodeID))

	// Build Gateway YAML config
	type gatewayYAMLConfig struct {
		ListenAddr            string   `yaml:"listen_addr"`
		ClientNamespace       string   `yaml:"client_namespace"`
		RQLiteDSN             string   `yaml:"rqlite_dsn"`
		GlobalRQLiteDSN       string   `yaml:"global_rqlite_dsn,omitempty"`
		DomainName            string   `yaml:"domain_name"`
		OlricServers          []string `yaml:"olric_servers"`
		OlricTimeout          string   `yaml:"olric_timeout"`
		IPFSClusterAPIURL     string   `yaml:"ipfs_cluster_api_url"`
		IPFSAPIURL            string   `yaml:"ipfs_api_url"`
		IPFSTimeout           string   `yaml:"ipfs_timeout"`
		IPFSReplicationFactor int      `yaml:"ipfs_replication_factor"`
	}

	gatewayConfig := gatewayYAMLConfig{
		ListenAddr:            fmt.Sprintf(":%d", cfg.HTTPPort),
		ClientNamespace:       cfg.Namespace,
		RQLiteDSN:             cfg.RQLiteDSN,
		GlobalRQLiteDSN:       cfg.GlobalRQLiteDSN,
		DomainName:            cfg.BaseDomain,
		OlricServers:          cfg.OlricServers,
		OlricTimeout:          cfg.OlricTimeout.String(),
		IPFSClusterAPIURL:     cfg.IPFSClusterAPIURL,
		IPFSAPIURL:            cfg.IPFSAPIURL,
		IPFSTimeout:           cfg.IPFSTimeout.String(),
		IPFSReplicationFactor: cfg.IPFSReplicationFactor,
	}

	configBytes, err := yaml.Marshal(gatewayConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal Gateway config: %w", err)
	}

	if err := os.WriteFile(configPath, configBytes, 0644); err != nil {
		return fmt.Errorf("failed to write Gateway config: %w", err)
	}

	s.logger.Info("Created Gateway config file",
		zap.String("path", configPath),
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Generate environment file with Gateway config path
	envVars := map[string]string{
		"GATEWAY_CONFIG": configPath,
	}

	if err := s.systemdMgr.GenerateEnvFile(namespace, nodeID, systemd.ServiceTypeGateway, envVars); err != nil {
		return fmt.Errorf("failed to generate Gateway env file: %w", err)
	}

	// Start the systemd service
	if err := s.systemdMgr.StartService(namespace, systemd.ServiceTypeGateway); err != nil {
		return fmt.Errorf("failed to start Gateway service: %w", err)
	}

	// Wait for service to be active
	if err := s.waitForService(namespace, systemd.ServiceTypeGateway, 30*time.Second); err != nil {
		return fmt.Errorf("Gateway service did not become active: %w", err)
	}

	s.logger.Info("Gateway spawned successfully via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return nil
}

// StopRQLite stops a RQLite instance
func (s *SystemdSpawner) StopRQLite(ctx context.Context, namespace, nodeID string) error {
	s.logger.Info("Stopping RQLite via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return s.systemdMgr.StopService(namespace, systemd.ServiceTypeRQLite)
}

// StopOlric stops an Olric instance
func (s *SystemdSpawner) StopOlric(ctx context.Context, namespace, nodeID string) error {
	s.logger.Info("Stopping Olric via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return s.systemdMgr.StopService(namespace, systemd.ServiceTypeOlric)
}

// StopGateway stops a Gateway instance
func (s *SystemdSpawner) StopGateway(ctx context.Context, namespace, nodeID string) error {
	s.logger.Info("Stopping Gateway via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return s.systemdMgr.StopService(namespace, systemd.ServiceTypeGateway)
}

// SaveClusterState writes cluster state JSON to the namespace data directory.
// Used by the spawn handler to persist state received from the coordinator node.
func (s *SystemdSpawner) SaveClusterState(namespace string, data []byte) error {
	dir := filepath.Join(s.namespaceBase, namespace)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create namespace dir: %w", err)
	}
	path := filepath.Join(dir, "cluster-state.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write cluster state: %w", err)
	}
	s.logger.Info("Saved cluster state from coordinator",
		zap.String("namespace", namespace),
		zap.String("path", path))
	return nil
}

// StopAll stops all services for a namespace
func (s *SystemdSpawner) StopAll(ctx context.Context, namespace string) error {
	s.logger.Info("Stopping all namespace services via systemd",
		zap.String("namespace", namespace))

	return s.systemdMgr.StopAllNamespaceServices(namespace)
}

// waitForService waits for a systemd service to become active
func (s *SystemdSpawner) waitForService(namespace string, serviceType systemd.ServiceType, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		active, err := s.systemdMgr.IsServiceActive(namespace, serviceType)
		if err != nil {
			return fmt.Errorf("failed to check service status: %w", err)
		}

		if active {
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("service did not become active within %v", timeout)
}
