package gateway

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// InstanceNodeStatus represents the status of an instance (local type to avoid import cycle)
type InstanceNodeStatus string

const (
	InstanceStatusPending  InstanceNodeStatus = "pending"
	InstanceStatusStarting InstanceNodeStatus = "starting"
	InstanceStatusRunning  InstanceNodeStatus = "running"
	InstanceStatusStopped  InstanceNodeStatus = "stopped"
	InstanceStatusFailed   InstanceNodeStatus = "failed"
)

// InstanceError represents an error during instance operations (local type to avoid import cycle)
type InstanceError struct {
	Message string
	Cause   error
}

func (e *InstanceError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *InstanceError) Unwrap() error {
	return e.Cause
}

// InstanceSpawner manages multiple Gateway instances for namespace clusters.
// Each namespace gets its own gateway instances that connect to its dedicated RQLite and Olric clusters.
type InstanceSpawner struct {
	logger    *zap.Logger
	baseDir   string // Base directory for all namespace data (e.g., ~/.orama/data/namespaces)
	instances map[string]*GatewayInstance
	mu        sync.RWMutex
}

// GatewayInstance represents a running Gateway instance for a namespace
type GatewayInstance struct {
	Namespace      string
	NodeID         string
	HTTPPort       int
	BaseDomain     string
	RQLiteDSN      string   // Connection to namespace RQLite
	OlricServers   []string // Connection to namespace Olric
	ConfigPath     string
	PID            int
	Status         InstanceNodeStatus
	StartedAt      time.Time
	LastHealthCheck time.Time
	cmd            *exec.Cmd
	logger         *zap.Logger
}

// InstanceConfig holds configuration for spawning a Gateway instance
type InstanceConfig struct {
	Namespace      string   // Namespace name (e.g., "alice")
	NodeID         string   // Physical node ID
	HTTPPort       int      // HTTP API port
	BaseDomain     string   // Base domain (e.g., "devnet-orama.network")
	RQLiteDSN      string   // RQLite connection DSN (e.g., "http://localhost:10000")
	OlricServers   []string // Olric server addresses
	NodePeerID     string   // Physical node's peer ID for home node management
	DataDir        string   // Data directory for deployments, SQLite, etc.
}

// GatewayYAMLConfig represents the gateway YAML configuration structure
// This must match the yamlCfg struct in cmd/gateway/config.go exactly
// because the gateway uses strict YAML decoding that rejects unknown fields
type GatewayYAMLConfig struct {
	ListenAddr            string   `yaml:"listen_addr"`
	ClientNamespace       string   `yaml:"client_namespace"`
	RQLiteDSN             string   `yaml:"rqlite_dsn"`
	BootstrapPeers        []string `yaml:"bootstrap_peers,omitempty"`
	EnableHTTPS           bool     `yaml:"enable_https,omitempty"`
	DomainName            string   `yaml:"domain_name,omitempty"`
	TLSCacheDir           string   `yaml:"tls_cache_dir,omitempty"`
	OlricServers          []string `yaml:"olric_servers"`
	OlricTimeout          string   `yaml:"olric_timeout,omitempty"`
	IPFSClusterAPIURL     string   `yaml:"ipfs_cluster_api_url,omitempty"`
	IPFSAPIURL            string   `yaml:"ipfs_api_url,omitempty"`
	IPFSTimeout           string   `yaml:"ipfs_timeout,omitempty"`
	IPFSReplicationFactor int      `yaml:"ipfs_replication_factor,omitempty"`
}

// NewInstanceSpawner creates a new Gateway instance spawner
func NewInstanceSpawner(baseDir string, logger *zap.Logger) *InstanceSpawner {
	return &InstanceSpawner{
		logger:    logger.With(zap.String("component", "gateway-instance-spawner")),
		baseDir:   baseDir,
		instances: make(map[string]*GatewayInstance),
	}
}

// instanceKey generates a unique key for an instance based on namespace and node
func instanceKey(ns, nodeID string) string {
	return fmt.Sprintf("%s:%s", ns, nodeID)
}

// SpawnInstance starts a new Gateway instance for a namespace on a specific node.
// Returns the instance info or an error if spawning fails.
func (is *InstanceSpawner) SpawnInstance(ctx context.Context, cfg InstanceConfig) (*GatewayInstance, error) {
	key := instanceKey(cfg.Namespace, cfg.NodeID)

	is.mu.Lock()
	if existing, ok := is.instances[key]; ok {
		is.mu.Unlock()
		// Instance already exists, return it if running
		if existing.Status == InstanceStatusRunning {
			return existing, nil
		}
		// Otherwise, remove it and start fresh
		is.mu.Lock()
		delete(is.instances, key)
	}
	is.mu.Unlock()

	// Create config and logs directories
	configDir := filepath.Join(is.baseDir, cfg.Namespace, "configs")
	logsDir := filepath.Join(is.baseDir, cfg.Namespace, "logs")
	dataDir := filepath.Join(is.baseDir, cfg.Namespace, "data")

	for _, dir := range []string{configDir, logsDir, dataDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, &InstanceError{
				Message: fmt.Sprintf("failed to create directory %s", dir),
				Cause:   err,
			}
		}
	}

	// Generate config file
	configPath := filepath.Join(configDir, fmt.Sprintf("gateway-%s.yaml", cfg.NodeID))
	if err := is.generateConfig(configPath, cfg, dataDir); err != nil {
		return nil, err
	}

	instance := &GatewayInstance{
		Namespace:    cfg.Namespace,
		NodeID:       cfg.NodeID,
		HTTPPort:     cfg.HTTPPort,
		BaseDomain:   cfg.BaseDomain,
		RQLiteDSN:    cfg.RQLiteDSN,
		OlricServers: cfg.OlricServers,
		ConfigPath:   configPath,
		Status:       InstanceStatusStarting,
		logger:       is.logger.With(zap.String("namespace", cfg.Namespace), zap.String("node_id", cfg.NodeID)),
	}

	instance.logger.Info("Starting Gateway instance",
		zap.Int("http_port", cfg.HTTPPort),
		zap.String("rqlite_dsn", cfg.RQLiteDSN),
		zap.Strings("olric_servers", cfg.OlricServers),
	)

	// Find the gateway binary - look in common locations
	var gatewayBinary string
	possiblePaths := []string{
		"./bin/gateway",                    // Development build
		"/usr/local/bin/orama-gateway",     // System-wide install
		"/opt/orama/bin/gateway",           // Package install
	}

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			gatewayBinary = path
			break
		}
	}

	// Also check PATH
	if gatewayBinary == "" {
		if path, err := exec.LookPath("orama-gateway"); err == nil {
			gatewayBinary = path
		}
	}

	if gatewayBinary == "" {
		return nil, &InstanceError{
			Message: "gateway binary not found (checked ./bin/gateway, /usr/local/bin/orama-gateway, /opt/orama/bin/gateway, PATH)",
			Cause:   nil,
		}
	}

	instance.logger.Info("Found gateway binary", zap.String("path", gatewayBinary))

	// Create command
	cmd := exec.CommandContext(ctx, gatewayBinary, "--config", configPath)
	instance.cmd = cmd

	// Setup logging
	logPath := filepath.Join(logsDir, fmt.Sprintf("gateway-%s.log", cfg.NodeID))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, &InstanceError{
			Message: "failed to open log file",
			Cause:   err,
		}
	}

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Start the process
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, &InstanceError{
			Message: "failed to start Gateway process",
			Cause:   err,
		}
	}

	logFile.Close()

	instance.PID = cmd.Process.Pid
	instance.StartedAt = time.Now()

	// Store instance
	is.mu.Lock()
	is.instances[key] = instance
	is.mu.Unlock()

	// Wait for instance to be ready
	if err := is.waitForInstanceReady(ctx, instance); err != nil {
		// Kill the process on failure
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		is.mu.Lock()
		delete(is.instances, key)
		is.mu.Unlock()
		return nil, &InstanceError{
			Message: "Gateway instance did not become ready",
			Cause:   err,
		}
	}

	instance.Status = InstanceStatusRunning
	instance.LastHealthCheck = time.Now()

	instance.logger.Info("Gateway instance started successfully",
		zap.Int("pid", instance.PID),
	)

	// Start background process monitor
	go is.monitorInstance(instance)

	return instance, nil
}

// generateConfig generates the Gateway YAML configuration file
func (is *InstanceSpawner) generateConfig(configPath string, cfg InstanceConfig, dataDir string) error {
	gatewayCfg := GatewayYAMLConfig{
		ListenAddr:      fmt.Sprintf(":%d", cfg.HTTPPort),
		ClientNamespace: cfg.Namespace,
		RQLiteDSN:       cfg.RQLiteDSN,
		OlricServers:    cfg.OlricServers,
		// Note: DomainName is used for HTTPS/TLS, not needed for namespace gateways in dev mode
		DomainName: cfg.BaseDomain,
	}

	data, err := yaml.Marshal(gatewayCfg)
	if err != nil {
		return &InstanceError{
			Message: "failed to marshal Gateway config",
			Cause:   err,
		}
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return &InstanceError{
			Message: "failed to write Gateway config",
			Cause:   err,
		}
	}

	return nil
}

// StopInstance stops a Gateway instance for a namespace on a specific node
func (is *InstanceSpawner) StopInstance(ctx context.Context, ns, nodeID string) error {
	key := instanceKey(ns, nodeID)

	is.mu.Lock()
	instance, ok := is.instances[key]
	if !ok {
		is.mu.Unlock()
		return nil // Already stopped
	}
	delete(is.instances, key)
	is.mu.Unlock()

	if instance.cmd != nil && instance.cmd.Process != nil {
		instance.logger.Info("Stopping Gateway instance", zap.Int("pid", instance.PID))

		// Send SIGTERM for graceful shutdown
		if err := instance.cmd.Process.Signal(os.Interrupt); err != nil {
			// If SIGTERM fails, kill it
			_ = instance.cmd.Process.Kill()
		}

		// Wait for process to exit with timeout
		done := make(chan error, 1)
		go func() {
			done <- instance.cmd.Wait()
		}()

		select {
		case <-done:
			instance.logger.Info("Gateway instance stopped gracefully")
		case <-time.After(10 * time.Second):
			instance.logger.Warn("Gateway instance did not stop gracefully, killing")
			_ = instance.cmd.Process.Kill()
		case <-ctx.Done():
			_ = instance.cmd.Process.Kill()
			return ctx.Err()
		}
	}

	instance.Status = InstanceStatusStopped
	return nil
}

// StopAllInstances stops all Gateway instances for a namespace
func (is *InstanceSpawner) StopAllInstances(ctx context.Context, ns string) error {
	is.mu.RLock()
	var keys []string
	for key, inst := range is.instances {
		if inst.Namespace == ns {
			keys = append(keys, key)
		}
	}
	is.mu.RUnlock()

	var lastErr error
	for _, key := range keys {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) == 2 {
			if err := is.StopInstance(ctx, parts[0], parts[1]); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

// GetInstance returns the instance for a namespace on a specific node
func (is *InstanceSpawner) GetInstance(ns, nodeID string) (*GatewayInstance, bool) {
	is.mu.RLock()
	defer is.mu.RUnlock()

	instance, ok := is.instances[instanceKey(ns, nodeID)]
	return instance, ok
}

// GetNamespaceInstances returns all instances for a namespace
func (is *InstanceSpawner) GetNamespaceInstances(ns string) []*GatewayInstance {
	is.mu.RLock()
	defer is.mu.RUnlock()

	var instances []*GatewayInstance
	for _, inst := range is.instances {
		if inst.Namespace == ns {
			instances = append(instances, inst)
		}
	}
	return instances
}

// HealthCheck checks if an instance is healthy
func (is *InstanceSpawner) HealthCheck(ctx context.Context, ns, nodeID string) (bool, error) {
	instance, ok := is.GetInstance(ns, nodeID)
	if !ok {
		return false, &InstanceError{Message: "instance not found"}
	}

	healthy, err := instance.IsHealthy(ctx)
	if healthy {
		is.mu.Lock()
		instance.LastHealthCheck = time.Now()
		is.mu.Unlock()
	}
	return healthy, err
}

// waitForInstanceReady waits for the Gateway instance to be ready
func (is *InstanceSpawner) waitForInstanceReady(ctx context.Context, instance *GatewayInstance) error {
	client := tlsutil.NewHTTPClient(2 * time.Second)

	// Gateway health check endpoint
	url := fmt.Sprintf("http://localhost:%d/v1/health", instance.HTTPPort)

	maxAttempts := 120 // 2 minutes
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}

		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			instance.logger.Debug("Gateway instance ready",
				zap.Int("attempts", i+1),
			)
			return nil
		}
	}

	return fmt.Errorf("Gateway did not become ready within timeout")
}

// monitorInstance monitors an instance and updates its status
func (is *InstanceSpawner) monitorInstance(instance *GatewayInstance) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		is.mu.RLock()
		key := instanceKey(instance.Namespace, instance.NodeID)
		_, exists := is.instances[key]
		is.mu.RUnlock()

		if !exists {
			// Instance was removed
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		healthy, _ := instance.IsHealthy(ctx)
		cancel()

		is.mu.Lock()
		if healthy {
			instance.Status = InstanceStatusRunning
			instance.LastHealthCheck = time.Now()
		} else {
			instance.Status = InstanceStatusFailed
			instance.logger.Warn("Gateway instance health check failed")
		}
		is.mu.Unlock()

		// Check if process is still running
		if instance.cmd != nil && instance.cmd.ProcessState != nil && instance.cmd.ProcessState.Exited() {
			is.mu.Lock()
			instance.Status = InstanceStatusStopped
			is.mu.Unlock()
			instance.logger.Warn("Gateway instance process exited unexpectedly")
			return
		}
	}
}

// IsHealthy checks if the Gateway instance is healthy
func (gi *GatewayInstance) IsHealthy(ctx context.Context) (bool, error) {
	url := fmt.Sprintf("http://localhost:%d/v1/health", gi.HTTPPort)
	client := tlsutil.NewHTTPClient(5 * time.Second)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

// DSN returns the local connection address for this Gateway instance
func (gi *GatewayInstance) DSN() string {
	return fmt.Sprintf("http://localhost:%d", gi.HTTPPort)
}

// ExternalURL returns the external URL for accessing this namespace's gateway
func (gi *GatewayInstance) ExternalURL() string {
	return fmt.Sprintf("https://ns-%s.%s", gi.Namespace, gi.BaseDomain)
}
