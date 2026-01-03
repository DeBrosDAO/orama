package development

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ProcessManager manages all dev environment processes
type ProcessManager struct {
	oramaDir  string
	pidsDir   string
	processes map[string]*ManagedProcess
	mutex     sync.Mutex
	logWriter io.Writer
}

// ManagedProcess tracks a running process
type ManagedProcess struct {
	Name      string
	PID       int
	StartTime time.Time
	LogPath   string
}

// NewProcessManager creates a new process manager
func NewProcessManager(oramaDir string, logWriter io.Writer) *ProcessManager {
	pidsDir := filepath.Join(oramaDir, ".pids")
	os.MkdirAll(pidsDir, 0755)

	return &ProcessManager{
		oramaDir:  oramaDir,
		pidsDir:   pidsDir,
		processes: make(map[string]*ManagedProcess),
		logWriter: logWriter,
	}
}

// StartAll starts all development services
func (pm *ProcessManager) StartAll(ctx context.Context) error {
	fmt.Fprintf(pm.logWriter, "\n🚀 Starting development environment...\n")
	fmt.Fprintf(pm.logWriter, "═══════════════════════════════════════\n\n")

	topology := DefaultTopology()

	// Build IPFS node info from topology
	ipfsNodes := pm.buildIPFSNodes(topology)

	// Start in order of dependencies
	services := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"IPFS", pm.startIPFS},
		{"IPFS Cluster", pm.startIPFSCluster},
		{"Olric", pm.startOlric},
		{"Anon", pm.startAnon},
		{"Nodes (Network)", pm.startNodes},
		{"Rqlite MCP", pm.startMCP},
	}

	for _, svc := range services {
		if err := svc.fn(ctx); err != nil {
			fmt.Fprintf(pm.logWriter, "⚠️  Failed to start %s: %v\n", svc.name, err)
		}
	}

	fmt.Fprintf(pm.logWriter, "\n")

	// Run health checks with retries before declaring success
	const (
		healthCheckRetries  = 20
		healthCheckInterval = 3 * time.Second
		healthCheckTimeout  = 70 * time.Second
	)

	if !pm.HealthCheckWithRetry(ctx, ipfsNodes, healthCheckRetries, healthCheckInterval, healthCheckTimeout) {
		fmt.Fprintf(pm.logWriter, "\n❌ Health checks failed - stopping all services\n")
		pm.StopAll(ctx)
		return fmt.Errorf("cluster health checks failed - services stopped")
	}

	// Print success and key endpoints
	pm.printStartupSummary(topology)
	return nil
}

// StopAll stops all running processes
func (pm *ProcessManager) StopAll(ctx context.Context) error {
	fmt.Fprintf(pm.logWriter, "\n🛑 Stopping development environment...\n\n")

	topology := DefaultTopology()
	var services []string

	// Build service list from topology (in reverse order)
	services = append(services, "gateway")
	for i := len(topology.Nodes) - 1; i >= 0; i-- {
		node := topology.Nodes[i]
		services = append(services, node.Name)
	}
	for i := len(topology.Nodes) - 1; i >= 0; i-- {
		node := topology.Nodes[i]
		services = append(services, fmt.Sprintf("ipfs-cluster-%s", node.Name))
	}
	for i := len(topology.Nodes) - 1; i >= 0; i-- {
		node := topology.Nodes[i]
		services = append(services, fmt.Sprintf("ipfs-%s", node.Name))
	}
	services = append(services, "olric", "anon", "rqlite-mcp")

	fmt.Fprintf(pm.logWriter, "Stopping %d services...\n\n", len(services))

	stoppedCount := 0
	for _, svc := range services {
		if err := pm.stopProcess(svc); err != nil {
			fmt.Fprintf(pm.logWriter, "⚠️  Error stopping %s: %v\n", svc, err)
		} else {
			stoppedCount++
		}
		fmt.Fprintf(pm.logWriter, "  [%d/%d] stopped\n", stoppedCount, len(services))
	}

	fmt.Fprintf(pm.logWriter, "\n✅ All %d services have been stopped\n\n", stoppedCount)
	return nil
}

// Status reports the status of all services
func (pm *ProcessManager) Status(ctx context.Context) {
	fmt.Fprintf(pm.logWriter, "\n📊 Development Environment Status\n")
	fmt.Fprintf(pm.logWriter, "================================\n\n")

	topology := DefaultTopology()

	// Build service list from topology
	var services []struct {
		name  string
		ports []int
	}

	for _, node := range topology.Nodes {
		services = append(services, struct {
			name  string
			ports []int
		}{
			fmt.Sprintf("%s IPFS", node.Name),
			[]int{node.IPFSAPIPort, node.IPFSSwarmPort},
		})
		services = append(services, struct {
			name  string
			ports []int
		}{
			fmt.Sprintf("%s Cluster", node.Name),
			[]int{node.ClusterAPIPort},
		})
		services = append(services, struct {
			name  string
			ports []int
		}{
			fmt.Sprintf("%s Node (P2P)", node.Name),
			[]int{node.P2PPort},
		})
	}

	services = append(services, struct {
		name  string
		ports []int
	}{"Gateway", []int{topology.GatewayPort}})
	services = append(services, struct {
		name  string
		ports []int
	}{"Olric", []int{topology.OlricHTTPPort, topology.OlricMemberPort}})
	services = append(services, struct {
		name  string
		ports []int
	}{"Anon SOCKS", []int{topology.AnonSOCKSPort}})
	services = append(services, struct {
		name  string
		ports []int
	}{"Rqlite MCP", []int{topology.MCPPort}})

	for _, svc := range services {
		pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("%s.pid", svc.name))
		running := false
		if pidBytes, err := os.ReadFile(pidPath); err == nil {
			var pid int
			fmt.Sscanf(string(pidBytes), "%d", &pid)
			if checkProcessRunning(pid) {
				running = true
			}
		}

		status := "❌ stopped"
		if running {
			status = "✅ running"
		}

		portStr := fmt.Sprintf("ports: %v", svc.ports)
		fmt.Fprintf(pm.logWriter, "  %-25s %s (%s)\n", svc.name, status, portStr)
	}

	fmt.Fprintf(pm.logWriter, "\nConfiguration files in %s:\n", pm.oramaDir)
	configFiles := []string{"node-1.yaml", "node-2.yaml", "node-3.yaml", "node-4.yaml", "node-5.yaml", "olric-config.yaml"}
	for _, f := range configFiles {
		path := filepath.Join(pm.oramaDir, f)
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(pm.logWriter, "  ✓ %s\n", f)
		} else {
			fmt.Fprintf(pm.logWriter, "  ✗ %s\n", f)
		}
	}

	fmt.Fprintf(pm.logWriter, "\nLogs directory: %s/logs\n\n", pm.oramaDir)
}
