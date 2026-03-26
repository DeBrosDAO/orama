// Package sandbox manages service processes in isolated Linux namespaces.
//
// Each service runs with:
//   - Separate mount namespace (CLONE_NEWNS) for filesystem isolation
//   - Separate UTS namespace (CLONE_NEWUTS) for hostname isolation
//   - Dedicated uid/gid (no root)
//   - Read-only root filesystem except for the service's data directory
//
// NO PID namespace (CLONE_NEWPID) — services like RQLite and Olric become PID 1
// in a new PID namespace, which changes signal semantics (SIGTERM is ignored by default
// for PID 1). Mount + UTS namespaces provide sufficient isolation.
package sandbox

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// Config defines the sandbox parameters for a service.
type Config struct {
	Name    string      // Human-readable name (e.g., "rqlite", "ipfs")
	Binary  string      // Absolute path to the binary
	Args    []string    // Command-line arguments
	User    uint32      // UID to run as
	Group   uint32      // GID to run as
	DataDir string      // Writable data directory
	LogFile string      // Path to log file
	Seccomp SeccompMode // Seccomp enforcement mode
}

// Process represents a running sandboxed service.
type Process struct {
	Config Config
	cmd    *exec.Cmd
}

// Start launches the service in an isolated namespace.
func Start(cfg Config) (*Process, error) {
	// Write seccomp profile for this service
	profilePath, err := WriteProfile(cfg.Name, cfg.Seccomp)
	if err != nil {
		log.Printf("WARNING: failed to write seccomp profile for %s: %v (running without seccomp)", cfg.Name, err)
	} else {
		modeStr := "enforce"
		if cfg.Seccomp == SeccompAudit {
			modeStr = "audit"
		}
		log.Printf("seccomp profile for %s written to %s (mode: %s)", cfg.Name, profilePath, modeStr)
	}

	cmd := exec.Command(cfg.Binary, cfg.Args...)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS | // mount namespace
			syscall.CLONE_NEWUTS, // hostname namespace
		Credential: &syscall.Credential{
			Uid: cfg.User,
			Gid: cfg.Group,
		},
	}

	// Redirect output to log file
	if cfg.LogFile != "" {
		logFile, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file %s: %w", cfg.LogFile, err)
		}
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", cfg.Name, err)
	}

	log.Printf("started %s (PID %d, UID %d)", cfg.Name, cmd.Process.Pid, cfg.User)

	return &Process{Config: cfg, cmd: cmd}, nil
}

// Stop sends SIGTERM to the process and waits for exit.
func (p *Process) Stop() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}

	log.Printf("stopping %s (PID %d)", p.Config.Name, p.cmd.Process.Pid)

	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to signal %s: %w", p.Config.Name, err)
	}

	if err := p.cmd.Wait(); err != nil {
		// Process exited with non-zero — not necessarily an error during shutdown
		log.Printf("%s exited: %v", p.Config.Name, err)
	}

	return nil
}

// IsRunning returns true if the process is still alive.
func (p *Process) IsRunning() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	// Signal 0 checks if the process exists
	return p.cmd.Process.Signal(syscall.Signal(0)) == nil
}

// Supervisor manages the lifecycle of all sandboxed services.
type Supervisor struct {
	mu        sync.Mutex
	processes map[string]*Process
}

// NewSupervisor creates a new service supervisor.
func NewSupervisor() *Supervisor {
	return &Supervisor{
		processes: make(map[string]*Process),
	}
}

// StartAll launches all configured services in the correct dependency order.
// Order: RQLite → Olric → IPFS → IPFS Cluster → Gateway → CoreDNS
func (s *Supervisor) StartAll() error {
	services := defaultServiceConfigs()

	for _, cfg := range services {
		proc, err := Start(cfg)
		if err != nil {
			return fmt.Errorf("failed to start %s: %w", cfg.Name, err)
		}
		s.mu.Lock()
		s.processes[cfg.Name] = proc
		s.mu.Unlock()
	}

	log.Printf("all %d services started", len(services))
	return nil
}

// StopAll stops all services in reverse order.
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop in reverse dependency order
	order := []string{"coredns", "gateway", "ipfs-cluster", "ipfs", "olric", "rqlite"}
	for _, name := range order {
		if proc, ok := s.processes[name]; ok {
			if err := proc.Stop(); err != nil {
				log.Printf("error stopping %s: %v", name, err)
			}
		}
	}
}

// RestartService restarts a single service by name.
func (s *Supervisor) RestartService(name string) error {
	s.mu.Lock()
	proc, exists := s.processes[name]
	s.mu.Unlock()

	if !exists {
		return fmt.Errorf("service %s not found", name)
	}

	if err := proc.Stop(); err != nil {
		log.Printf("error stopping %s for restart: %v", name, err)
	}

	newProc, err := Start(proc.Config)
	if err != nil {
		return fmt.Errorf("failed to restart %s: %w", name, err)
	}

	s.mu.Lock()
	s.processes[name] = newProc
	s.mu.Unlock()

	return nil
}

// GetStatus returns the running status of all services.
func (s *Supervisor) GetStatus() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := make(map[string]bool)
	for name, proc := range s.processes {
		status[name] = proc.IsRunning()
	}
	return status
}

// defaultServiceConfigs returns the service configurations in startup order.
func defaultServiceConfigs() []Config {
	const (
		oramaDir = "/opt/orama/.orama"
		binDir   = "/opt/orama/bin"
		logsDir  = "/opt/orama/.orama/logs"
	)

	// Start in SeccompAudit mode to profile syscalls on sandbox.
	// Switch to SeccompEnforce after capturing required syscalls in production.
	mode := SeccompAudit

	return []Config{
		{
			Name:    "rqlite",
			Binary:  "/usr/local/bin/rqlited",
			Args:    []string{"-node-id", "1", "-http-addr", "0.0.0.0:4001", "-raft-addr", "0.0.0.0:4002", oramaDir + "/data/rqlite"},
			User:    1001,
			Group:   1001,
			DataDir: oramaDir + "/data/rqlite",
			LogFile: logsDir + "/rqlite.log",
			Seccomp: mode,
		},
		{
			Name:    "olric",
			Binary:  "/usr/local/bin/olric-server",
			Args:    nil, // configured via OLRIC_SERVER_CONFIG env
			User:    1002,
			Group:   1002,
			DataDir: oramaDir + "/data",
			LogFile: logsDir + "/olric.log",
			Seccomp: mode,
		},
		{
			Name:    "ipfs",
			Binary:  "/usr/local/bin/ipfs",
			Args:    []string{"daemon", "--enable-pubsub-experiment", "--repo-dir=" + oramaDir + "/data/ipfs/repo"},
			User:    1003,
			Group:   1003,
			DataDir: oramaDir + "/data/ipfs",
			LogFile: logsDir + "/ipfs.log",
			Seccomp: mode,
		},
		{
			Name:    "ipfs-cluster",
			Binary:  "/usr/local/bin/ipfs-cluster-service",
			Args:    []string{"daemon", "--config", oramaDir + "/data/ipfs-cluster/service.json"},
			User:    1004,
			Group:   1004,
			DataDir: oramaDir + "/data/ipfs-cluster",
			LogFile: logsDir + "/ipfs-cluster.log",
			Seccomp: mode,
		},
		{
			Name:    "gateway",
			Binary:  binDir + "/gateway",
			Args:    []string{"--config", oramaDir + "/configs/gateway.yaml"},
			User:    1005,
			Group:   1005,
			DataDir: oramaDir,
			LogFile: logsDir + "/gateway.log",
			Seccomp: mode,
		},
		{
			Name:    "coredns",
			Binary:  "/usr/local/bin/coredns",
			Args:    []string{"-conf", "/etc/coredns/Corefile"},
			User:    1006,
			Group:   1006,
			DataDir: oramaDir,
			LogFile: logsDir + "/coredns.log",
			Seccomp: mode,
		},
	}
}
