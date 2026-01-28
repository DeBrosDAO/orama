package rqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	"go.uber.org/zap"
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

// InstanceSpawner manages multiple RQLite instances for namespace clusters.
// Each namespace gets its own RQLite cluster with dedicated ports and data directories.
type InstanceSpawner struct {
	logger    *zap.Logger
	baseDir   string // Base directory for all namespace data (e.g., ~/.orama/data/namespaces)
	instances map[string]*RQLiteInstance
	mu        sync.RWMutex
}

// RQLiteInstance represents a running RQLite instance for a namespace
type RQLiteInstance struct {
	Namespace         string
	NodeID            string
	HTTPPort          int
	RaftPort          int
	HTTPAdvAddress    string
	RaftAdvAddress    string
	JoinAddresses     []string
	DataDir           string
	IsLeader          bool
	PID               int
	Status            InstanceNodeStatus
	StartedAt         time.Time
	LastHealthCheck   time.Time
	cmd               *exec.Cmd
	logger            *zap.Logger
}

// InstanceConfig holds configuration for spawning an RQLite instance
type InstanceConfig struct {
	Namespace       string   // Namespace name (e.g., "alice")
	NodeID          string   // Physical node ID
	HTTPPort        int      // HTTP API port
	RaftPort        int      // Raft consensus port
	HTTPAdvAddress  string   // Advertised HTTP address (e.g., "192.168.1.10:10000")
	RaftAdvAddress  string   // Advertised Raft address (e.g., "192.168.1.10:10001")
	JoinAddresses   []string // Addresses of existing cluster members to join
	IsLeader        bool     // Whether this is the initial leader node
}

// NewInstanceSpawner creates a new RQLite instance spawner
func NewInstanceSpawner(baseDir string, logger *zap.Logger) *InstanceSpawner {
	return &InstanceSpawner{
		logger:    logger.With(zap.String("component", "rqlite-instance-spawner")),
		baseDir:   baseDir,
		instances: make(map[string]*RQLiteInstance),
	}
}

// instanceKey generates a unique key for an instance based on namespace and node
func instanceKey(namespace, nodeID string) string {
	return fmt.Sprintf("%s:%s", namespace, nodeID)
}

// SpawnInstance starts a new RQLite instance for a namespace on a specific node.
// Returns the instance info or an error if spawning fails.
func (is *InstanceSpawner) SpawnInstance(ctx context.Context, cfg InstanceConfig) (*RQLiteInstance, error) {
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

	// Create data directory
	dataDir := filepath.Join(is.baseDir, cfg.Namespace, "rqlite", cfg.NodeID)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, &InstanceError{
			Message: "failed to create data directory",
			Cause:   err,
		}
	}

	// Create logs directory
	logsDir := filepath.Join(is.baseDir, cfg.Namespace, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, &InstanceError{
			Message: "failed to create logs directory",
			Cause:   err,
		}
	}

	instance := &RQLiteInstance{
		Namespace:      cfg.Namespace,
		NodeID:         cfg.NodeID,
		HTTPPort:       cfg.HTTPPort,
		RaftPort:       cfg.RaftPort,
		HTTPAdvAddress: cfg.HTTPAdvAddress,
		RaftAdvAddress: cfg.RaftAdvAddress,
		JoinAddresses:  cfg.JoinAddresses,
		DataDir:        dataDir,
		IsLeader:       cfg.IsLeader,
		Status:         InstanceStatusStarting,
		logger:         is.logger.With(zap.String("namespace", cfg.Namespace), zap.String("node_id", cfg.NodeID)),
	}

	// Build command arguments
	args := []string{
		"-http-addr", fmt.Sprintf("0.0.0.0:%d", cfg.HTTPPort),
		"-http-adv-addr", cfg.HTTPAdvAddress,
		"-raft-addr", fmt.Sprintf("0.0.0.0:%d", cfg.RaftPort),
		"-raft-adv-addr", cfg.RaftAdvAddress,
	}

	// Handle cluster joining
	if len(cfg.JoinAddresses) > 0 && !cfg.IsLeader {
		// Remove peers.json if it exists to avoid stale cluster state
		peersJSONPath := filepath.Join(dataDir, "raft", "peers.json")
		if _, err := os.Stat(peersJSONPath); err == nil {
			instance.logger.Debug("Removing existing peers.json before joining cluster",
				zap.String("path", peersJSONPath))
			_ = os.Remove(peersJSONPath)
		}

		// Prepare join addresses (strip http:// prefix if present)
		joinAddrs := make([]string, 0, len(cfg.JoinAddresses))
		for _, addr := range cfg.JoinAddresses {
			addr = strings.TrimPrefix(addr, "http://")
			addr = strings.TrimPrefix(addr, "https://")
			joinAddrs = append(joinAddrs, addr)
		}

		// Wait for join targets to be available
		if err := is.waitForJoinTargets(ctx, cfg.JoinAddresses); err != nil {
			instance.logger.Warn("Join targets not all reachable, will still attempt join",
				zap.Error(err))
		}

		args = append(args,
			"-join", strings.Join(joinAddrs, ","),
			"-join-as", cfg.RaftAdvAddress,
			"-join-attempts", "30",
			"-join-interval", "10s",
		)
	}

	// Add data directory as final argument
	args = append(args, dataDir)

	instance.logger.Info("Starting RQLite instance",
		zap.Int("http_port", cfg.HTTPPort),
		zap.Int("raft_port", cfg.RaftPort),
		zap.Strings("join_addresses", cfg.JoinAddresses),
		zap.Bool("is_leader", cfg.IsLeader),
	)

	// Create command
	cmd := exec.CommandContext(ctx, "rqlited", args...)
	instance.cmd = cmd

	// Setup logging
	logPath := filepath.Join(logsDir, fmt.Sprintf("rqlite-%s.log", cfg.NodeID))
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
			Message: "failed to start RQLite process",
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
			Message: "RQLite instance did not become ready",
			Cause:   err,
		}
	}

	instance.Status = InstanceStatusRunning
	instance.LastHealthCheck = time.Now()

	instance.logger.Info("RQLite instance started successfully",
		zap.Int("pid", instance.PID),
	)

	// Start background process monitor
	go is.monitorInstance(instance)

	return instance, nil
}

// StopInstance stops an RQLite instance for a namespace on a specific node
func (is *InstanceSpawner) StopInstance(ctx context.Context, namespace, nodeID string) error {
	key := instanceKey(namespace, nodeID)

	is.mu.Lock()
	instance, ok := is.instances[key]
	if !ok {
		is.mu.Unlock()
		return nil // Already stopped
	}
	delete(is.instances, key)
	is.mu.Unlock()

	if instance.cmd != nil && instance.cmd.Process != nil {
		instance.logger.Info("Stopping RQLite instance", zap.Int("pid", instance.PID))

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
			instance.logger.Info("RQLite instance stopped gracefully")
		case <-time.After(10 * time.Second):
			instance.logger.Warn("RQLite instance did not stop gracefully, killing")
			_ = instance.cmd.Process.Kill()
		case <-ctx.Done():
			_ = instance.cmd.Process.Kill()
			return ctx.Err()
		}
	}

	instance.Status = InstanceStatusStopped
	return nil
}

// StopAllInstances stops all RQLite instances for a namespace
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
func (is *InstanceSpawner) GetInstance(namespace, nodeID string) (*RQLiteInstance, bool) {
	is.mu.RLock()
	defer is.mu.RUnlock()

	instance, ok := is.instances[instanceKey(namespace, nodeID)]
	return instance, ok
}

// GetNamespaceInstances returns all instances for a namespace
func (is *InstanceSpawner) GetNamespaceInstances(ns string) []*RQLiteInstance {
	is.mu.RLock()
	defer is.mu.RUnlock()

	var instances []*RQLiteInstance
	for _, inst := range is.instances {
		if inst.Namespace == ns {
			instances = append(instances, inst)
		}
	}
	return instances
}

// HealthCheck checks if an instance is healthy
func (is *InstanceSpawner) HealthCheck(ctx context.Context, namespace, nodeID string) (bool, error) {
	instance, ok := is.GetInstance(namespace, nodeID)
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

// waitForJoinTargets waits for join target nodes to be reachable
func (is *InstanceSpawner) waitForJoinTargets(ctx context.Context, joinAddresses []string) error {
	timeout := 2 * time.Minute
	deadline := time.Now().Add(timeout)
	client := tlsutil.NewHTTPClient(5 * time.Second)

	for time.Now().Before(deadline) {
		allReachable := true
		for _, addr := range joinAddresses {
			statusURL := addr
			if !strings.HasPrefix(addr, "http") {
				statusURL = "http://" + addr
			}
			statusURL = strings.TrimRight(statusURL, "/") + "/status"

			resp, err := client.Get(statusURL)
			if err != nil {
				allReachable = false
				break
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				allReachable = false
				break
			}
		}

		if allReachable {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	return fmt.Errorf("join targets not reachable within timeout")
}

// waitForInstanceReady waits for the RQLite instance to be ready
func (is *InstanceSpawner) waitForInstanceReady(ctx context.Context, instance *RQLiteInstance) error {
	url := fmt.Sprintf("http://localhost:%d/status", instance.HTTPPort)
	client := tlsutil.NewHTTPClient(2 * time.Second)

	// Longer timeout for joining nodes as they need to sync
	maxAttempts := 180 // 3 minutes
	if len(instance.JoinAddresses) > 0 {
		maxAttempts = 300 // 5 minutes for joiners
	}

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

		if resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var statusResp map[string]interface{}
			if err := json.Unmarshal(body, &statusResp); err == nil {
				if raft, ok := statusResp["raft"].(map[string]interface{}); ok {
					state, _ := raft["state"].(string)
					if state == "leader" || state == "follower" {
						instance.logger.Debug("RQLite instance ready",
							zap.String("state", state),
							zap.Int("attempts", i+1),
						)
						return nil
					}
				} else {
					// Backwards compatibility - if no raft status, consider ready
					return nil
				}
			}
		}
		resp.Body.Close()
	}

	return fmt.Errorf("RQLite did not become ready within timeout")
}

// monitorInstance monitors an instance and updates its status
func (is *InstanceSpawner) monitorInstance(instance *RQLiteInstance) {
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
			instance.logger.Warn("RQLite instance health check failed")
		}
		is.mu.Unlock()

		// Check if process is still running
		if instance.cmd != nil && instance.cmd.ProcessState != nil && instance.cmd.ProcessState.Exited() {
			is.mu.Lock()
			instance.Status = InstanceStatusStopped
			is.mu.Unlock()
			instance.logger.Warn("RQLite instance process exited unexpectedly")
			return
		}
	}
}

// IsHealthy checks if the RQLite instance is healthy
func (ri *RQLiteInstance) IsHealthy(ctx context.Context) (bool, error) {
	url := fmt.Sprintf("http://localhost:%d/status", ri.HTTPPort)
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

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("status endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	var statusResp map[string]interface{}
	if err := json.Unmarshal(body, &statusResp); err != nil {
		return false, err
	}

	if raft, ok := statusResp["raft"].(map[string]interface{}); ok {
		state, _ := raft["state"].(string)
		return state == "leader" || state == "follower", nil
	}

	// Backwards compatibility
	return true, nil
}

// GetLeaderAddress returns the leader's address for the cluster
func (ri *RQLiteInstance) GetLeaderAddress(ctx context.Context) (string, error) {
	url := fmt.Sprintf("http://localhost:%d/status", ri.HTTPPort)
	client := tlsutil.NewHTTPClient(5 * time.Second)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var statusResp map[string]interface{}
	if err := json.Unmarshal(body, &statusResp); err != nil {
		return "", err
	}

	if raft, ok := statusResp["raft"].(map[string]interface{}); ok {
		if leader, ok := raft["leader_addr"].(string); ok {
			return leader, nil
		}
	}

	return "", fmt.Errorf("leader address not found in status response")
}

// DSN returns the connection string for this RQLite instance
func (ri *RQLiteInstance) DSN() string {
	return fmt.Sprintf("http://localhost:%d", ri.HTTPPort)
}

// AdvertisedDSN returns the advertised connection string for cluster communication
func (ri *RQLiteInstance) AdvertisedDSN() string {
	return fmt.Sprintf("http://%s", ri.HTTPAdvAddress)
}
