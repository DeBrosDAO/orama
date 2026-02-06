package process

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"go.uber.org/zap"
)

// Manager manages deployment processes via systemd (Linux) or direct process spawning (macOS/other)
type Manager struct {
	logger    *zap.Logger
	useSystemd bool

	// For non-systemd mode: track running processes
	processes   map[string]*exec.Cmd
	processesMu sync.RWMutex
}

// NewManager creates a new process manager
func NewManager(logger *zap.Logger) *Manager {
	// Use systemd only on Linux
	useSystemd := runtime.GOOS == "linux"

	return &Manager{
		logger:     logger,
		useSystemd: useSystemd,
		processes:  make(map[string]*exec.Cmd),
	}
}

// Start starts a deployment process
func (m *Manager) Start(ctx context.Context, deployment *deployments.Deployment, workDir string) error {
	serviceName := m.getServiceName(deployment)

	m.logger.Info("Starting deployment process",
		zap.String("deployment", deployment.Name),
		zap.String("namespace", deployment.Namespace),
		zap.String("service", serviceName),
		zap.Bool("systemd", m.useSystemd),
	)

	if !m.useSystemd {
		return m.startDirect(ctx, deployment, workDir)
	}

	// Create systemd service file
	if err := m.createSystemdService(deployment, workDir); err != nil {
		return fmt.Errorf("failed to create systemd service: %w", err)
	}

	// Reload systemd
	if err := m.systemdReload(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}

	// Enable service
	if err := m.systemdEnable(serviceName); err != nil {
		return fmt.Errorf("failed to enable service: %w", err)
	}

	// Start service
	if err := m.systemdStart(serviceName); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	m.logger.Info("Deployment process started",
		zap.String("deployment", deployment.Name),
		zap.String("service", serviceName),
	)

	return nil
}

// startDirect starts a process directly without systemd (for macOS/local dev)
func (m *Manager) startDirect(ctx context.Context, deployment *deployments.Deployment, workDir string) error {
	serviceName := m.getServiceName(deployment)
	startCmd := m.getStartCommand(deployment, workDir)

	// Parse command
	parts := strings.Fields(startCmd)
	if len(parts) == 0 {
		return fmt.Errorf("empty start command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workDir

	// Set environment
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", deployment.Port))
	for key, value := range deployment.Environment {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	// Create log file for output
	logDir := filepath.Join(os.Getenv("HOME"), ".orama", "logs", "deployments")
	os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(
		filepath.Join(logDir, serviceName+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		m.logger.Warn("Failed to create log file", zap.Error(err))
	} else {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	// Start process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	// Track process
	m.processesMu.Lock()
	m.processes[serviceName] = cmd
	m.processesMu.Unlock()

	// Monitor process in background
	go func() {
		err := cmd.Wait()
		m.processesMu.Lock()
		delete(m.processes, serviceName)
		m.processesMu.Unlock()
		if err != nil {
			m.logger.Warn("Process exited with error",
				zap.String("service", serviceName),
				zap.Error(err),
			)
		}
		if logFile != nil {
			logFile.Close()
		}
	}()

	m.logger.Info("Deployment process started (direct)",
		zap.String("deployment", deployment.Name),
		zap.String("service", serviceName),
		zap.Int("pid", cmd.Process.Pid),
	)

	return nil
}

// Stop stops a deployment process
func (m *Manager) Stop(ctx context.Context, deployment *deployments.Deployment) error {
	serviceName := m.getServiceName(deployment)

	m.logger.Info("Stopping deployment process",
		zap.String("deployment", deployment.Name),
		zap.String("service", serviceName),
	)

	if !m.useSystemd {
		return m.stopDirect(serviceName)
	}

	// Stop service
	if err := m.systemdStop(serviceName); err != nil {
		m.logger.Warn("Failed to stop service", zap.Error(err))
	}

	// Disable service
	if err := m.systemdDisable(serviceName); err != nil {
		m.logger.Warn("Failed to disable service", zap.Error(err))
	}

	// Remove service file using sudo
	serviceFile := filepath.Join("/etc/systemd/system", serviceName+".service")
	cmd := exec.Command("sudo", "rm", "-f", serviceFile)
	if err := cmd.Run(); err != nil {
		m.logger.Warn("Failed to remove service file", zap.Error(err))
	}

	// Reload systemd
	m.systemdReload()

	return nil
}

// stopDirect stops a directly spawned process
func (m *Manager) stopDirect(serviceName string) error {
	m.processesMu.Lock()
	cmd, exists := m.processes[serviceName]
	m.processesMu.Unlock()

	if !exists || cmd.Process == nil {
		return nil // Already stopped
	}

	// Send SIGTERM
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		// Try SIGKILL if SIGTERM fails
		cmd.Process.Kill()
	}

	return nil
}

// Restart restarts a deployment process
func (m *Manager) Restart(ctx context.Context, deployment *deployments.Deployment) error {
	serviceName := m.getServiceName(deployment)

	m.logger.Info("Restarting deployment process",
		zap.String("deployment", deployment.Name),
		zap.String("service", serviceName),
	)

	if !m.useSystemd {
		// For direct mode, stop and start
		m.stopDirect(serviceName)
		// Note: Would need workDir to restart, which we don't have here
		// For now, just log a warning
		m.logger.Warn("Restart not fully supported in direct mode")
		return nil
	}

	return m.systemdRestart(serviceName)
}

// Status gets the status of a deployment process
func (m *Manager) Status(ctx context.Context, deployment *deployments.Deployment) (string, error) {
	serviceName := m.getServiceName(deployment)

	if !m.useSystemd {
		m.processesMu.RLock()
		_, exists := m.processes[serviceName]
		m.processesMu.RUnlock()
		if exists {
			return "active", nil
		}
		return "inactive", nil
	}

	cmd := exec.CommandContext(ctx, "systemctl", "is-active", serviceName)
	output, err := cmd.Output()
	if err != nil {
		return "unknown", err
	}

	return strings.TrimSpace(string(output)), nil
}

// GetLogs retrieves logs for a deployment
func (m *Manager) GetLogs(ctx context.Context, deployment *deployments.Deployment, lines int, follow bool) ([]byte, error) {
	serviceName := m.getServiceName(deployment)

	if !m.useSystemd {
		// Read from log file in direct mode
		logFile := filepath.Join(os.Getenv("HOME"), ".orama", "logs", "deployments", serviceName+".log")
		data, err := os.ReadFile(logFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read log file: %w", err)
		}
		// Return last N lines if specified
		if lines > 0 {
			logLines := strings.Split(string(data), "\n")
			if len(logLines) > lines {
				logLines = logLines[len(logLines)-lines:]
			}
			return []byte(strings.Join(logLines, "\n")), nil
		}
		return data, nil
	}

	args := []string{"-u", serviceName, "--no-pager"}
	if lines > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", lines))
	}
	if follow {
		args = append(args, "-f")
	}

	cmd := exec.CommandContext(ctx, "journalctl", args...)
	return cmd.Output()
}

// createSystemdService creates a systemd service file
func (m *Manager) createSystemdService(deployment *deployments.Deployment, workDir string) error {
	serviceName := m.getServiceName(deployment)
	serviceFile := filepath.Join("/etc/systemd/system", serviceName+".service")

	// Determine the start command based on deployment type
	startCmd := m.getStartCommand(deployment, workDir)

	// Build environment variables
	envVars := make([]string, 0)
	envVars = append(envVars, fmt.Sprintf("PORT=%d", deployment.Port))
	for key, value := range deployment.Environment {
		envVars = append(envVars, fmt.Sprintf("%s=%s", key, value))
	}

	// Create service from template
	tmpl := `[Unit]
Description=Orama Deployment - {{.Namespace}}/{{.Name}}
After=network.target

[Service]
Type=simple
User=debros
Group=debros
WorkingDirectory={{.WorkDir}}

{{range .Env}}Environment="{{.}}"
{{end}}

ExecStart={{.StartCmd}}

Restart={{.RestartPolicy}}
RestartSec=5s

# Resource limits
MemoryLimit={{.MemoryLimitMB}}M
CPUQuota={{.CPULimitPercent}}%

# Security - minimal restrictions for deployments in home directory
PrivateTmp=true
ProtectSystem=full
ProtectHome=read-only
ReadWritePaths={{.WorkDir}}

StandardOutput=journal
StandardError=journal
SyslogIdentifier={{.ServiceName}}

[Install]
WantedBy=multi-user.target
`

	t, err := template.New("service").Parse(tmpl)
	if err != nil {
		return err
	}

	data := struct {
		Namespace       string
		Name            string
		ServiceName     string
		WorkDir         string
		StartCmd        string
		Env             []string
		RestartPolicy   string
		MemoryLimitMB   int
		CPULimitPercent int
	}{
		Namespace:       deployment.Namespace,
		Name:            deployment.Name,
		ServiceName:     serviceName,
		WorkDir:         workDir,
		StartCmd:        startCmd,
		Env:             envVars,
		RestartPolicy:   m.mapRestartPolicy(deployment.RestartPolicy),
		MemoryLimitMB:   deployment.MemoryLimitMB,
		CPULimitPercent: deployment.CPULimitPercent,
	}

	// Execute template to buffer
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}

	// Use sudo tee to write to systemd directory (debros user needs sudo access)
	cmd := exec.Command("sudo", "tee", serviceFile)
	cmd.Stdin = &buf
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to write service file: %s: %w", string(output), err)
	}

	return nil
}

// getStartCommand determines the start command for a deployment
func (m *Manager) getStartCommand(deployment *deployments.Deployment, workDir string) string {
	// For systemd (Linux), use full paths. For direct mode, use PATH resolution.
	nodePath := "node"
	npmPath := "npm"
	if m.useSystemd {
		nodePath = "/usr/bin/node"
		npmPath = "/usr/bin/npm"
	}

	switch deployment.Type {
	case deployments.DeploymentTypeNextJS:
		// CLI tarballs the standalone output directly, so server.js is at the root
		return nodePath + " server.js"
	case deployments.DeploymentTypeNodeJSBackend:
		// Check if ENTRY_POINT is set in environment
		if entryPoint, ok := deployment.Environment["ENTRY_POINT"]; ok {
			if entryPoint == "npm:start" {
				return npmPath + " start"
			}
			return nodePath + " " + entryPoint
		}
		return nodePath + " index.js"
	case deployments.DeploymentTypeGoBackend:
		return filepath.Join(workDir, "app")
	default:
		return "echo 'Unknown deployment type'"
	}
}

// mapRestartPolicy maps deployment restart policy to systemd restart policy
func (m *Manager) mapRestartPolicy(policy deployments.RestartPolicy) string {
	switch policy {
	case deployments.RestartPolicyAlways:
		return "always"
	case deployments.RestartPolicyOnFailure:
		return "on-failure"
	case deployments.RestartPolicyNever:
		return "no"
	default:
		return "on-failure"
	}
}

// getServiceName generates a systemd service name
func (m *Manager) getServiceName(deployment *deployments.Deployment) string {
	// Sanitize namespace and name for service name
	namespace := strings.ReplaceAll(deployment.Namespace, ".", "-")
	name := strings.ReplaceAll(deployment.Name, ".", "-")
	return fmt.Sprintf("orama-deploy-%s-%s", namespace, name)
}

// systemd helper methods (use sudo for non-root execution)
func (m *Manager) systemdReload() error {
	cmd := exec.Command("sudo", "systemctl", "daemon-reload")
	return cmd.Run()
}

func (m *Manager) systemdEnable(serviceName string) error {
	cmd := exec.Command("sudo", "systemctl", "enable", serviceName)
	return cmd.Run()
}

func (m *Manager) systemdDisable(serviceName string) error {
	cmd := exec.Command("sudo", "systemctl", "disable", serviceName)
	return cmd.Run()
}

func (m *Manager) systemdStart(serviceName string) error {
	cmd := exec.Command("sudo", "systemctl", "start", serviceName)
	return cmd.Run()
}

func (m *Manager) systemdStop(serviceName string) error {
	cmd := exec.Command("sudo", "systemctl", "stop", serviceName)
	return cmd.Run()
}

func (m *Manager) systemdRestart(serviceName string) error {
	cmd := exec.Command("sudo", "systemctl", "restart", serviceName)
	return cmd.Run()
}

// WaitForHealthy waits for a deployment to become healthy
func (m *Manager) WaitForHealthy(ctx context.Context, deployment *deployments.Deployment, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		status, err := m.Status(ctx, deployment)
		if err == nil && status == "active" {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
			// Continue checking
		}
	}

	return fmt.Errorf("deployment did not become healthy within %v", timeout)
}

// DeploymentStats holds on-demand resource usage for a deployment process
type DeploymentStats struct {
	PID        int     `json:"pid"`
	CPUPercent float64 `json:"cpu_percent"`
	MemoryRSS  int64   `json:"memory_rss_bytes"`
	DiskBytes  int64   `json:"disk_bytes"`
	UptimeSecs float64 `json:"uptime_seconds"`
}

// GetStats returns on-demand resource usage stats for a deployment.
// deployPath is the directory on disk for disk usage calculation.
func (m *Manager) GetStats(ctx context.Context, deployment *deployments.Deployment, deployPath string) (*DeploymentStats, error) {
	stats := &DeploymentStats{}

	// Disk usage (works on all platforms)
	if deployPath != "" {
		stats.DiskBytes = dirSize(deployPath)
	}

	if !m.useSystemd {
		// Direct mode (macOS) — only disk, no /proc
		serviceName := m.getServiceName(deployment)
		m.processesMu.RLock()
		cmd, exists := m.processes[serviceName]
		m.processesMu.RUnlock()
		if exists && cmd.Process != nil {
			stats.PID = cmd.Process.Pid
		}
		return stats, nil
	}

	// Systemd mode (Linux) — get PID, CPU, RAM, uptime
	serviceName := m.getServiceName(deployment)

	// Get MainPID and ActiveEnterTimestamp
	cmd := exec.CommandContext(ctx, "systemctl", "show", serviceName,
		"--property=MainPID,ActiveEnterTimestamp")
	output, err := cmd.Output()
	if err != nil {
		return stats, fmt.Errorf("systemctl show failed: %w", err)
	}

	props := parseSystemctlShow(string(output))
	pid, _ := strconv.Atoi(props["MainPID"])
	stats.PID = pid

	if pid <= 0 {
		return stats, nil // Process not running
	}

	// Uptime from ActiveEnterTimestamp
	if ts := props["ActiveEnterTimestamp"]; ts != "" {
		// Format: "Mon 2026-01-29 10:00:00 UTC"
		if t, err := parseSystemdTimestamp(ts); err == nil {
			stats.UptimeSecs = time.Since(t).Seconds()
		}
	}

	// Memory RSS from /proc/[pid]/status
	stats.MemoryRSS = readProcMemoryRSS(pid)

	// CPU % — sample /proc/[pid]/stat twice with 1s gap
	stats.CPUPercent = sampleCPUPercent(pid)

	return stats, nil
}

// parseSystemctlShow parses "Key=Value\n" output into a map
func parseSystemctlShow(output string) map[string]string {
	props := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		if idx := strings.IndexByte(line, '='); idx > 0 {
			props[line[:idx]] = strings.TrimSpace(line[idx+1:])
		}
	}
	return props
}

// parseSystemdTimestamp parses systemd timestamp like "Mon 2026-01-29 10:00:00 UTC"
func parseSystemdTimestamp(ts string) (time.Time, error) {
	// Try common systemd formats
	for _, layout := range []string{
		"Mon 2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05 MST",
	} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse timestamp: %s", ts)
}

// readProcMemoryRSS reads VmRSS from /proc/[pid]/status (Linux only)
func readProcMemoryRSS(pid int) int64 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseInt(fields[1], 10, 64)
				return kb * 1024 // Convert KB to bytes
			}
		}
	}
	return 0
}

// sampleCPUPercent reads /proc/[pid]/stat twice with a 1s gap to compute CPU %
func sampleCPUPercent(pid int) float64 {
	readCPUTicks := func() (utime, stime int64, ok bool) {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return 0, 0, false
		}
		// Fields after the comm (in parens): state(3), ppid(4), ... utime(14), stime(15)
		// Find closing paren to skip comm field which may contain spaces
		closeParen := strings.LastIndexByte(string(data), ')')
		if closeParen < 0 {
			return 0, 0, false
		}
		fields := strings.Fields(string(data)[closeParen+2:])
		if len(fields) < 13 {
			return 0, 0, false
		}
		u, _ := strconv.ParseInt(fields[11], 10, 64) // utime is field 14, index 11 after paren
		s, _ := strconv.ParseInt(fields[12], 10, 64) // stime is field 15, index 12 after paren
		return u, s, true
	}

	u1, s1, ok1 := readCPUTicks()
	if !ok1 {
		return 0
	}
	time.Sleep(1 * time.Second)
	u2, s2, ok2 := readCPUTicks()
	if !ok2 {
		return 0
	}

	// Clock ticks per second (usually 100 on Linux)
	clkTck := 100.0
	totalDelta := float64((u2 + s2) - (u1 + s1))
	cpuPct := (totalDelta / clkTck) * 100.0

	return cpuPct
}

// dirSize calculates total size of a directory
func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}
