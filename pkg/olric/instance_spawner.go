package olric

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

// InstanceSpawner manages multiple Olric instances for namespace clusters.
// Each namespace gets its own Olric cluster with dedicated ports and memberlist.
type InstanceSpawner struct {
	logger    *zap.Logger
	baseDir   string // Base directory for all namespace data (e.g., ~/.orama/data/namespaces)
	instances map[string]*OlricInstance
	mu        sync.RWMutex
}

// OlricInstance represents a running Olric instance for a namespace
type OlricInstance struct {
	Namespace        string
	NodeID           string
	HTTPPort         int
	MemberlistPort   int
	BindAddr         string
	AdvertiseAddr    string
	PeerAddresses    []string // Memberlist peer addresses for cluster discovery
	ConfigPath       string
	DataDir          string
	PID              int
	Status           InstanceNodeStatus
	StartedAt        time.Time
	LastHealthCheck  time.Time
	cmd              *exec.Cmd
	logger           *zap.Logger
}

// InstanceConfig holds configuration for spawning an Olric instance
type InstanceConfig struct {
	Namespace       string   // Namespace name (e.g., "alice")
	NodeID          string   // Physical node ID
	HTTPPort        int      // HTTP API port
	MemberlistPort  int      // Memberlist gossip port
	BindAddr        string   // Address to bind (e.g., "0.0.0.0")
	AdvertiseAddr   string   // Address to advertise (e.g., "192.168.1.10")
	PeerAddresses   []string // Memberlist peer addresses for initial cluster join
}

// OlricConfig represents the Olric YAML configuration structure
type OlricConfig struct {
	Server     OlricServerConfig     `yaml:"server"`
	Memberlist OlricMemberlistConfig `yaml:"memberlist"`
}

// OlricServerConfig represents the server section of Olric config
type OlricServerConfig struct {
	BindAddr string `yaml:"bindAddr"`
	BindPort int    `yaml:"bindPort"`
}

// OlricMemberlistConfig represents the memberlist section of Olric config
type OlricMemberlistConfig struct {
	Environment string   `yaml:"environment"`
	BindAddr    string   `yaml:"bindAddr"`
	BindPort    int      `yaml:"bindPort"`
	Peers       []string `yaml:"peers,omitempty"`
}

// NewInstanceSpawner creates a new Olric instance spawner
func NewInstanceSpawner(baseDir string, logger *zap.Logger) *InstanceSpawner {
	return &InstanceSpawner{
		logger:    logger.With(zap.String("component", "olric-instance-spawner")),
		baseDir:   baseDir,
		instances: make(map[string]*OlricInstance),
	}
}

// instanceKey generates a unique key for an instance based on namespace and node
func instanceKey(namespace, nodeID string) string {
	return fmt.Sprintf("%s:%s", namespace, nodeID)
}

// SpawnInstance starts a new Olric instance for a namespace on a specific node.
// Returns the instance info or an error if spawning fails.
func (is *InstanceSpawner) SpawnInstance(ctx context.Context, cfg InstanceConfig) (*OlricInstance, error) {
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

	// Create data and config directories
	dataDir := filepath.Join(is.baseDir, cfg.Namespace, "olric", cfg.NodeID)
	configDir := filepath.Join(is.baseDir, cfg.Namespace, "configs")
	logsDir := filepath.Join(is.baseDir, cfg.Namespace, "logs")

	for _, dir := range []string{dataDir, configDir, logsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, &InstanceError{
				Message: fmt.Sprintf("failed to create directory %s", dir),
				Cause:   err,
			}
		}
	}

	// Generate config file
	configPath := filepath.Join(configDir, fmt.Sprintf("olric-%s.yaml", cfg.NodeID))
	if err := is.generateConfig(configPath, cfg); err != nil {
		return nil, err
	}

	instance := &OlricInstance{
		Namespace:      cfg.Namespace,
		NodeID:         cfg.NodeID,
		HTTPPort:       cfg.HTTPPort,
		MemberlistPort: cfg.MemberlistPort,
		BindAddr:       cfg.BindAddr,
		AdvertiseAddr:  cfg.AdvertiseAddr,
		PeerAddresses:  cfg.PeerAddresses,
		ConfigPath:     configPath,
		DataDir:        dataDir,
		Status:         InstanceStatusStarting,
		logger:         is.logger.With(zap.String("namespace", cfg.Namespace), zap.String("node_id", cfg.NodeID)),
	}

	instance.logger.Info("Starting Olric instance",
		zap.Int("http_port", cfg.HTTPPort),
		zap.Int("memberlist_port", cfg.MemberlistPort),
		zap.Strings("peers", cfg.PeerAddresses),
	)

	// Create command with config environment variable
	cmd := exec.CommandContext(ctx, "olric-server")
	cmd.Env = append(os.Environ(), fmt.Sprintf("OLRIC_SERVER_CONFIG=%s", configPath))
	instance.cmd = cmd

	// Setup logging
	logPath := filepath.Join(logsDir, fmt.Sprintf("olric-%s.log", cfg.NodeID))
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
			Message: "failed to start Olric process",
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
			Message: "Olric instance did not become ready",
			Cause:   err,
		}
	}

	instance.Status = InstanceStatusRunning
	instance.LastHealthCheck = time.Now()

	instance.logger.Info("Olric instance started successfully",
		zap.Int("pid", instance.PID),
	)

	// Start background process monitor
	go is.monitorInstance(instance)

	return instance, nil
}

// generateConfig generates the Olric YAML configuration file
func (is *InstanceSpawner) generateConfig(configPath string, cfg InstanceConfig) error {
	// Use "lan" environment for namespace clusters (low latency expected)
	olricCfg := OlricConfig{
		Server: OlricServerConfig{
			BindAddr: cfg.BindAddr,
			BindPort: cfg.HTTPPort,
		},
		Memberlist: OlricMemberlistConfig{
			Environment: "lan",
			BindAddr:    cfg.BindAddr,
			BindPort:    cfg.MemberlistPort,
			Peers:       cfg.PeerAddresses,
		},
	}

	data, err := yaml.Marshal(olricCfg)
	if err != nil {
		return &InstanceError{
			Message: "failed to marshal Olric config",
			Cause:   err,
		}
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return &InstanceError{
			Message: "failed to write Olric config",
			Cause:   err,
		}
	}

	return nil
}

// StopInstance stops an Olric instance for a namespace on a specific node
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
		instance.logger.Info("Stopping Olric instance", zap.Int("pid", instance.PID))

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
			instance.logger.Info("Olric instance stopped gracefully")
		case <-time.After(10 * time.Second):
			instance.logger.Warn("Olric instance did not stop gracefully, killing")
			_ = instance.cmd.Process.Kill()
		case <-ctx.Done():
			_ = instance.cmd.Process.Kill()
			return ctx.Err()
		}
	}

	instance.Status = InstanceStatusStopped
	return nil
}

// StopAllInstances stops all Olric instances for a namespace
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
func (is *InstanceSpawner) GetInstance(ns, nodeID string) (*OlricInstance, bool) {
	is.mu.RLock()
	defer is.mu.RUnlock()

	instance, ok := is.instances[instanceKey(ns, nodeID)]
	return instance, ok
}

// GetNamespaceInstances returns all instances for a namespace
func (is *InstanceSpawner) GetNamespaceInstances(ns string) []*OlricInstance {
	is.mu.RLock()
	defer is.mu.RUnlock()

	var instances []*OlricInstance
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

// waitForInstanceReady waits for the Olric instance to be ready
func (is *InstanceSpawner) waitForInstanceReady(ctx context.Context, instance *OlricInstance) error {
	// Olric doesn't have a standard /ready endpoint, so we check if the process
	// is running and the memberlist port is accepting connections

	maxAttempts := 30 // 30 seconds
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}

		// Check if the process is still running
		if instance.cmd != nil && instance.cmd.ProcessState != nil && instance.cmd.ProcessState.Exited() {
			return fmt.Errorf("Olric process exited unexpectedly")
		}

		// Try to connect to the memberlist port to verify it's accepting connections
		// Use the advertise address since Olric may bind to a specific IP
		addr := fmt.Sprintf("%s:%d", instance.AdvertiseAddr, instance.MemberlistPort)
		if instance.AdvertiseAddr == "" {
			addr = fmt.Sprintf("localhost:%d", instance.MemberlistPort)
		}

		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			instance.logger.Debug("Waiting for Olric memberlist",
				zap.Int("attempt", i+1),
				zap.String("addr", addr),
				zap.Error(err),
			)
			continue
		}
		conn.Close()

		instance.logger.Debug("Olric instance ready",
			zap.Int("attempts", i+1),
			zap.String("addr", addr),
		)
		return nil
	}

	return fmt.Errorf("Olric did not become ready within timeout")
}

// monitorInstance monitors an instance and updates its status
func (is *InstanceSpawner) monitorInstance(instance *OlricInstance) {
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
			instance.logger.Warn("Olric instance health check failed")
		}
		is.mu.Unlock()

		// Check if process is still running
		if instance.cmd != nil && instance.cmd.ProcessState != nil && instance.cmd.ProcessState.Exited() {
			is.mu.Lock()
			instance.Status = InstanceStatusStopped
			is.mu.Unlock()
			instance.logger.Warn("Olric instance process exited unexpectedly")
			return
		}
	}
}

// IsHealthy checks if the Olric instance is healthy by verifying the memberlist port is accepting connections
func (oi *OlricInstance) IsHealthy(ctx context.Context) (bool, error) {
	// Olric doesn't have a standard /ready HTTP endpoint, so we check memberlist connectivity
	addr := fmt.Sprintf("%s:%d", oi.AdvertiseAddr, oi.MemberlistPort)
	if oi.AdvertiseAddr == "" || oi.AdvertiseAddr == "0.0.0.0" {
		addr = fmt.Sprintf("localhost:%d", oi.MemberlistPort)
	}

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false, err
	}
	conn.Close()
	return true, nil
}

// DSN returns the connection address for this Olric instance
func (oi *OlricInstance) DSN() string {
	return fmt.Sprintf("localhost:%d", oi.HTTPPort)
}

// AdvertisedDSN returns the advertised connection address
func (oi *OlricInstance) AdvertisedDSN() string {
	return fmt.Sprintf("%s:%d", oi.AdvertiseAddr, oi.HTTPPort)
}

// MemberlistAddress returns the memberlist address for cluster communication
func (oi *OlricInstance) MemberlistAddress() string {
	return fmt.Sprintf("%s:%d", oi.AdvertiseAddr, oi.MemberlistPort)
}
