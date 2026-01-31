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
	Namespace      string
	NodeID         string
	HTTPPort       int
	MemberlistPort int
	BindAddr       string
	AdvertiseAddr  string
	PeerAddresses  []string // Memberlist peer addresses for cluster discovery
	ConfigPath     string
	DataDir        string
	PID            int
	Status         InstanceNodeStatus
	StartedAt      time.Time
	LastHealthCheck time.Time
	cmd            *exec.Cmd
	logFile        *os.File     // kept open for process lifetime
	waitDone       chan struct{} // closed when cmd.Wait() completes
	logger         *zap.Logger
}

// InstanceConfig holds configuration for spawning an Olric instance
type InstanceConfig struct {
	Namespace      string   // Namespace name (e.g., "alice")
	NodeID         string   // Physical node ID
	HTTPPort       int      // HTTP API port
	MemberlistPort int      // Memberlist gossip port
	BindAddr       string   // Address to bind (e.g., "0.0.0.0")
	AdvertiseAddr  string   // Address to advertise (e.g., "192.168.1.10")
	PeerAddresses  []string // Memberlist peer addresses for initial cluster join
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
// The process is decoupled from the caller's context — it runs independently until
// explicitly stopped. Only returns an error if the process fails to start or the
// memberlist port doesn't open within the timeout.
// Note: The memberlist port opening does NOT mean the cluster has formed — peers may
// still be joining. Use WaitForProcessRunning() after spawning all instances to verify.
func (is *InstanceSpawner) SpawnInstance(ctx context.Context, cfg InstanceConfig) (*OlricInstance, error) {
	key := instanceKey(cfg.Namespace, cfg.NodeID)

	is.mu.Lock()
	if existing, ok := is.instances[key]; ok {
		if existing.Status == InstanceStatusRunning || existing.Status == InstanceStatusStarting {
			is.mu.Unlock()
			return existing, nil
		}
		// Remove stale instance
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
		waitDone:       make(chan struct{}),
		logger:         is.logger.With(zap.String("namespace", cfg.Namespace), zap.String("node_id", cfg.NodeID)),
	}

	instance.logger.Info("Starting Olric instance",
		zap.Int("http_port", cfg.HTTPPort),
		zap.Int("memberlist_port", cfg.MemberlistPort),
		zap.Strings("peers", cfg.PeerAddresses),
	)

	// Use exec.Command (NOT exec.CommandContext) so the process is NOT killed
	// when the HTTP request context or provisioning context is cancelled.
	// The process lives until explicitly stopped via StopInstance().
	cmd := exec.Command("olric-server")
	cmd.Env = append(os.Environ(), fmt.Sprintf("OLRIC_SERVER_CONFIG=%s", configPath))
	instance.cmd = cmd

	// Setup logging — keep the file open for the process lifetime
	logPath := filepath.Join(logsDir, fmt.Sprintf("olric-%s.log", cfg.NodeID))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, &InstanceError{
			Message: "failed to open log file",
			Cause:   err,
		}
	}
	instance.logFile = logFile

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

	instance.PID = cmd.Process.Pid
	instance.StartedAt = time.Now()

	// Reap the child process in a background goroutine to prevent zombies.
	// This goroutine closes the log file and signals via waitDone when the process exits.
	go func() {
		_ = cmd.Wait()
		logFile.Close()
		close(instance.waitDone)
	}()

	// Store instance
	is.mu.Lock()
	is.instances[key] = instance
	is.mu.Unlock()

	// Wait for the memberlist port to accept TCP connections.
	// This confirms the process started and Olric initialized its network layer.
	// It does NOT guarantee peers have joined — that happens asynchronously.
	if err := is.waitForPortReady(ctx, instance); err != nil {
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

		// Wait for process to exit via the reaper goroutine
		select {
		case <-instance.waitDone:
			instance.logger.Info("Olric instance stopped gracefully")
		case <-time.After(10 * time.Second):
			instance.logger.Warn("Olric instance did not stop gracefully, killing")
			_ = instance.cmd.Process.Kill()
			<-instance.waitDone // wait for reaper to finish
		case <-ctx.Done():
			_ = instance.cmd.Process.Kill()
			<-instance.waitDone
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

// waitForPortReady waits for the Olric memberlist port to accept TCP connections.
// This is a lightweight check — it confirms the process started but does NOT
// guarantee that peers have joined the cluster.
func (is *InstanceSpawner) waitForPortReady(ctx context.Context, instance *OlricInstance) error {
	// Use BindAddr for the health check — this is the address the process actually listens on.
	// AdvertiseAddr may differ from BindAddr (e.g., 0.0.0.0 resolves to IPv6 on some hosts).
	checkAddr := instance.BindAddr
	if checkAddr == "" || checkAddr == "0.0.0.0" {
		checkAddr = "localhost"
	}
	addr := fmt.Sprintf("%s:%d", checkAddr, instance.MemberlistPort)

	maxAttempts := 30
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-instance.waitDone:
			// Process exited before becoming ready
			return fmt.Errorf("Olric process exited unexpectedly (pid %d)", instance.PID)
		case <-time.After(1 * time.Second):
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

		instance.logger.Debug("Olric memberlist port ready",
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

	for {
		select {
		case <-instance.waitDone:
			// Process exited — update status and stop monitoring
			is.mu.Lock()
			key := instanceKey(instance.Namespace, instance.NodeID)
			if _, exists := is.instances[key]; exists {
				instance.Status = InstanceStatusStopped
				instance.logger.Warn("Olric instance process exited unexpectedly")
			}
			is.mu.Unlock()
			return
		case <-ticker.C:
		}

		is.mu.RLock()
		key := instanceKey(instance.Namespace, instance.NodeID)
		_, exists := is.instances[key]
		is.mu.RUnlock()

		if !exists {
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
	}
}

// IsHealthy checks if the Olric instance is healthy by verifying the memberlist port is accepting connections
func (oi *OlricInstance) IsHealthy(ctx context.Context) (bool, error) {
	// Check if process has exited first
	select {
	case <-oi.waitDone:
		return false, fmt.Errorf("process has exited")
	default:
	}

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
