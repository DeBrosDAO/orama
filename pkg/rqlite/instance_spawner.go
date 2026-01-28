package rqlite

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

// InstanceConfig contains configuration for spawning a RQLite instance
type InstanceConfig struct {
	Namespace      string   // Namespace this instance belongs to
	NodeID         string   // Node ID where this instance runs
	HTTPPort       int      // HTTP API port
	RaftPort       int      // Raft consensus port
	HTTPAdvAddress string   // Advertised HTTP address (e.g., "192.168.1.1:10000")
	RaftAdvAddress string   // Advertised Raft address (e.g., "192.168.1.1:10001")
	JoinAddresses  []string // Addresses to join (e.g., ["192.168.1.2:10001"])
	DataDir        string   // Data directory for this instance
	IsLeader       bool     // Whether this is the first node (creates cluster)
}

// Instance represents a running RQLite instance
type Instance struct {
	Config  InstanceConfig
	Process *os.Process
	PID     int
}

// InstanceSpawner manages RQLite instance lifecycle for namespaces
type InstanceSpawner struct {
	baseDataDir string // Base directory for namespace data (e.g., ~/.orama/data/namespaces)
	rqlitePath  string // Path to rqlited binary
	logger      *zap.Logger
}

// NewInstanceSpawner creates a new RQLite instance spawner
func NewInstanceSpawner(baseDataDir string, logger *zap.Logger) *InstanceSpawner {
	// Find rqlited binary
	rqlitePath := "rqlited" // Will use PATH
	if path, err := exec.LookPath("rqlited"); err == nil {
		rqlitePath = path
	}

	return &InstanceSpawner{
		baseDataDir: baseDataDir,
		rqlitePath:  rqlitePath,
		logger:      logger,
	}
}

// SpawnInstance starts a new RQLite instance with the given configuration
func (is *InstanceSpawner) SpawnInstance(ctx context.Context, cfg InstanceConfig) (*Instance, error) {
	// Create data directory
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(is.baseDataDir, cfg.Namespace, "rqlite", cfg.NodeID)
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Build command arguments
	// Note: All flags must come BEFORE the data directory argument
	args := []string{
		"-http-addr", fmt.Sprintf("0.0.0.0:%d", cfg.HTTPPort),
		"-raft-addr", fmt.Sprintf("0.0.0.0:%d", cfg.RaftPort),
		"-http-adv-addr", cfg.HTTPAdvAddress,
		"-raft-adv-addr", cfg.RaftAdvAddress,
	}

	// Add join addresses if not the leader (must be before data directory)
	if !cfg.IsLeader && len(cfg.JoinAddresses) > 0 {
		for _, addr := range cfg.JoinAddresses {
			args = append(args, "-join", addr)
		}
	}

	// Data directory must be the last argument
	args = append(args, dataDir)

	is.logger.Info("Spawning RQLite instance",
		zap.String("namespace", cfg.Namespace),
		zap.String("node_id", cfg.NodeID),
		zap.Int("http_port", cfg.HTTPPort),
		zap.Int("raft_port", cfg.RaftPort),
		zap.Bool("is_leader", cfg.IsLeader),
		zap.Strings("join_addresses", cfg.JoinAddresses),
	)

	// Start the process
	cmd := exec.CommandContext(ctx, is.rqlitePath, args...)
	cmd.Dir = dataDir

	// Log output
	logFile, err := os.OpenFile(
		filepath.Join(dataDir, "rqlite.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start rqlited: %w", err)
	}

	instance := &Instance{
		Config:  cfg,
		Process: cmd.Process,
		PID:     cmd.Process.Pid,
	}

	// Wait for the instance to be ready
	if err := is.waitForReady(ctx, cfg.HTTPPort); err != nil {
		// Kill the process if it didn't start properly
		cmd.Process.Kill()
		return nil, fmt.Errorf("instance failed to become ready: %w", err)
	}

	is.logger.Info("RQLite instance started successfully",
		zap.String("namespace", cfg.Namespace),
		zap.Int("pid", instance.PID),
	)

	return instance, nil
}

// waitForReady waits for the RQLite instance to be ready to accept connections
func (is *InstanceSpawner) waitForReady(ctx context.Context, httpPort int) error {
	url := fmt.Sprintf("http://localhost:%d/status", httpPort)
	client := &http.Client{Timeout: 2 * time.Second}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for RQLite to be ready on port %d", httpPort)
}

// StopInstance stops a running RQLite instance
func (is *InstanceSpawner) StopInstance(ctx context.Context, instance *Instance) error {
	if instance == nil || instance.Process == nil {
		return nil
	}

	is.logger.Info("Stopping RQLite instance",
		zap.String("namespace", instance.Config.Namespace),
		zap.Int("pid", instance.PID),
	)

	// Send SIGTERM for graceful shutdown
	if err := instance.Process.Signal(os.Interrupt); err != nil {
		// If SIGTERM fails, try SIGKILL
		if err := instance.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
	}

	// Wait for process to exit
	done := make(chan error, 1)
	go func() {
		_, err := instance.Process.Wait()
		done <- err
	}()

	select {
	case <-ctx.Done():
		instance.Process.Kill()
		return ctx.Err()
	case err := <-done:
		if err != nil {
			is.logger.Warn("Process exited with error", zap.Error(err))
		}
	case <-time.After(10 * time.Second):
		instance.Process.Kill()
	}

	is.logger.Info("RQLite instance stopped",
		zap.String("namespace", instance.Config.Namespace),
	)

	return nil
}

// StopInstanceByPID stops a RQLite instance by its PID
func (is *InstanceSpawner) StopInstanceByPID(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process not found: %w", err)
	}

	// Send SIGTERM
	if err := process.Signal(os.Interrupt); err != nil {
		// Try SIGKILL
		if err := process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
	}

	return nil
}

// IsInstanceRunning checks if a RQLite instance is running
func (is *InstanceSpawner) IsInstanceRunning(httpPort int) bool {
	url := fmt.Sprintf("http://localhost:%d/status", httpPort)
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// GetDataDir returns the data directory path for a namespace RQLite instance
func (is *InstanceSpawner) GetDataDir(namespace, nodeID string) string {
	return filepath.Join(is.baseDataDir, namespace, "rqlite", nodeID)
}

// CleanupDataDir removes the data directory for a namespace RQLite instance
func (is *InstanceSpawner) CleanupDataDir(namespace, nodeID string) error {
	dataDir := is.GetDataDir(namespace, nodeID)
	return os.RemoveAll(dataDir)
}
