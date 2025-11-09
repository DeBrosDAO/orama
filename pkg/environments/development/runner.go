package development

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProcessManager manages all dev environment processes
type ProcessManager struct {
	debrosDir string
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
func NewProcessManager(debrosDir string, logWriter io.Writer) *ProcessManager {
	pidsDir := filepath.Join(debrosDir, ".pids")
	os.MkdirAll(pidsDir, 0755)

	return &ProcessManager{
		debrosDir: debrosDir,
		pidsDir:   pidsDir,
		processes: make(map[string]*ManagedProcess),
		logWriter: logWriter,
	}
}

// StartAll starts all development services
func (pm *ProcessManager) StartAll(ctx context.Context) error {
	fmt.Fprintf(pm.logWriter, "\n🚀 Starting development environment...\n\n")

	// Define IPFS nodes for later use in health checks
	ipfsNodes := []ipfsNodeInfo{
		{"bootstrap", filepath.Join(pm.debrosDir, "bootstrap/ipfs/repo"), 4501, 4101, 7501, ""},
		{"node2", filepath.Join(pm.debrosDir, "node2/ipfs/repo"), 4502, 4102, 7502, ""},
		{"node3", filepath.Join(pm.debrosDir, "node3/ipfs/repo"), 4503, 4103, 7503, ""},
	}

	// Start in order of dependencies
	services := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"IPFS", pm.startIPFS},
		{"IPFS Cluster", pm.startIPFSCluster},
		{"Olric", pm.startOlric},
		{"Anon", pm.startAnon},
		{"Bootstrap Node", pm.startBootstrapNode},
		{"Node2", pm.startNode2},
		{"Node3", pm.startNode3},
		{"Gateway", pm.startGateway},
	}

	for _, svc := range services {
		if err := svc.fn(ctx); err != nil {
			fmt.Fprintf(pm.logWriter, "⚠️  Failed to start %s: %v\n", svc.name, err)
			// Continue starting others, don't fail
		}
	}

	// Run health checks with retries before declaring success
	const (
		healthCheckRetries  = 20
		healthCheckInterval = 3 * time.Second
		healthCheckTimeout  = 70 * time.Second
	)

	if !pm.HealthCheckWithRetry(ctx, ipfsNodes, healthCheckRetries, healthCheckInterval, healthCheckTimeout) {
		fmt.Fprintf(pm.logWriter, "\n❌ Development environment failed health checks - stopping all services\n")
		pm.StopAll(ctx)
		return fmt.Errorf("cluster health checks failed - services stopped")
	}

	fmt.Fprintf(pm.logWriter, "\n✅ Development environment started!\n\n")
	return nil
}

// StopAll stops all running processes
func (pm *ProcessManager) StopAll(ctx context.Context) error {
	fmt.Fprintf(pm.logWriter, "\n🛑 Stopping development environment...\n")

	services := []string{
		"gateway",
		"node3",
		"node2",
		"bootstrap",
		"olric",
		"ipfs-cluster-node3",
		"ipfs-cluster-node2",
		"ipfs-cluster-bootstrap",
		"rqlite-node3",
		"rqlite-node2",
		"rqlite-bootstrap",
		"ipfs-node3",
		"ipfs-node2",
		"ipfs-bootstrap",
		"anon",
	}

	for _, svc := range services {
		pm.stopProcess(svc)
	}

	fmt.Fprintf(pm.logWriter, "✓ All services stopped\n\n")
	return nil
}

// Status reports the status of all services
func (pm *ProcessManager) Status(ctx context.Context) {
	fmt.Fprintf(pm.logWriter, "\n📊 Development Environment Status\n")
	fmt.Fprintf(pm.logWriter, "================================\n\n")

	services := []struct {
		name  string
		ports []int
	}{
		{"Bootstrap IPFS", []int{4501, 4101}},
		{"Bootstrap RQLite", []int{5001, 7001}},
		{"Node2 IPFS", []int{4502, 4102}},
		{"Node2 RQLite", []int{5002, 7002}},
		{"Node3 IPFS", []int{4503, 4103}},
		{"Node3 RQLite", []int{5003, 7003}},
		{"Bootstrap Cluster", []int{9094}},
		{"Node2 Cluster", []int{9104}},
		{"Node3 Cluster", []int{9114}},
		{"Bootstrap Node (P2P)", []int{4001}},
		{"Node2 (P2P)", []int{4002}},
		{"Node3 (P2P)", []int{4003}},
		{"Gateway", []int{6001}},
		{"Olric", []int{3320, 3322}},
		{"Anon SOCKS", []int{9050}},
	}

	for _, svc := range services {
		pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("%s.pid", svc.name))
		running := false
		if pidBytes, err := os.ReadFile(pidPath); err == nil {
			pid, _ := strconv.Atoi(string(pidBytes))
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

	fmt.Fprintf(pm.logWriter, "\nConfiguration files in %s:\n", pm.debrosDir)
	files := []string{"bootstrap.yaml", "node2.yaml", "node3.yaml", "gateway.yaml", "olric-config.yaml"}
	for _, f := range files {
		path := filepath.Join(pm.debrosDir, f)
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(pm.logWriter, "  ✓ %s\n", f)
		} else {
			fmt.Fprintf(pm.logWriter, "  ✗ %s\n", f)
		}
	}

	fmt.Fprintf(pm.logWriter, "\nLogs directory: %s/logs\n\n", pm.debrosDir)
}

// Helper functions for starting individual services

// ipfsNodeInfo holds information about an IPFS node for peer discovery
type ipfsNodeInfo struct {
	name        string
	ipfsPath    string
	apiPort     int
	swarmPort   int
	gatewayPort int
	peerID      string
}

// readIPFSConfigValue reads a single config value from IPFS repo without daemon running
func readIPFSConfigValue(ctx context.Context, repoPath string, key string) (string, error) {
	configPath := filepath.Join(repoPath, "config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to read IPFS config: %w", err)
	}

	// Simple JSON parse to extract the value - only works for string values
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, key) {
			// Extract the value after the colon
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				value := strings.TrimSpace(parts[1])
				value = strings.Trim(value, `",`)
				if value != "" {
					return value, nil
				}
			}
		}
	}

	return "", fmt.Errorf("key %s not found in IPFS config", key)
}

// seedIPFSPeersWithHTTP configures each IPFS node to bootstrap with its local peers using HTTP API
func (pm *ProcessManager) seedIPFSPeersWithHTTP(ctx context.Context, nodes []ipfsNodeInfo) error {
	fmt.Fprintf(pm.logWriter, "  Seeding IPFS local bootstrap peers via HTTP API...\n")

	// Wait for all IPFS daemons to be ready before trying to configure them
	for _, node := range nodes {
		if err := pm.waitIPFSReady(ctx, node); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to wait for IPFS readiness for %s: %v\n", node.name, err)
		}
	}

	// For each node, clear default bootstrap and add local peers via HTTP
	for i, node := range nodes {
		// Clear bootstrap peers
		httpURL := fmt.Sprintf("http://127.0.0.1:%d/api/v0/bootstrap/rm?all=true", node.apiPort)
		if err := pm.ipfsHTTPCall(ctx, httpURL, "POST"); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to clear bootstrap for %s: %v\n", node.name, err)
		}

		// Add other nodes as bootstrap peers
		for j, otherNode := range nodes {
			if i == j {
				continue // Skip self
			}

			multiaddr := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", otherNode.swarmPort, otherNode.peerID)
			httpURL := fmt.Sprintf("http://127.0.0.1:%d/api/v0/bootstrap/add?arg=%s", node.apiPort, url.QueryEscape(multiaddr))
			if err := pm.ipfsHTTPCall(ctx, httpURL, "POST"); err != nil {
				fmt.Fprintf(pm.logWriter, "    Warning: failed to add bootstrap peer for %s: %v\n", node.name, err)
			}
		}
	}

	return nil
}

// waitIPFSReady polls the IPFS daemon's HTTP API until it's ready
func (pm *ProcessManager) waitIPFSReady(ctx context.Context, node ipfsNodeInfo) error {
	maxRetries := 30
	retryInterval := 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		httpURL := fmt.Sprintf("http://127.0.0.1:%d/api/v0/version", node.apiPort)
		if err := pm.ipfsHTTPCall(ctx, httpURL, "POST"); err == nil {
			return nil // IPFS is ready
		}

		select {
		case <-time.After(retryInterval):
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("IPFS daemon %s did not become ready after %d seconds", node.name, (maxRetries * int(retryInterval.Seconds())))
}

// ipfsHTTPCall makes an HTTP call to IPFS API
func (pm *ProcessManager) ipfsHTTPCall(ctx context.Context, urlStr string, method string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (pm *ProcessManager) startIPFS(ctx context.Context) error {
	nodes := []ipfsNodeInfo{
		{"bootstrap", filepath.Join(pm.debrosDir, "bootstrap/ipfs/repo"), 4501, 4101, 7501, ""},
		{"node2", filepath.Join(pm.debrosDir, "node2/ipfs/repo"), 4502, 4102, 7502, ""},
		{"node3", filepath.Join(pm.debrosDir, "node3/ipfs/repo"), 4503, 4103, 7503, ""},
	}

	// Phase 1: Initialize repos and configure addresses
	for i := range nodes {
		os.MkdirAll(nodes[i].ipfsPath, 0755)

		// Initialize IPFS if needed
		if _, err := os.Stat(filepath.Join(nodes[i].ipfsPath, "config")); os.IsNotExist(err) {
			fmt.Fprintf(pm.logWriter, "  Initializing IPFS (%s)...\n", nodes[i].name)
			cmd := exec.CommandContext(ctx, "ipfs", "init", "--profile=server", "--repo-dir="+nodes[i].ipfsPath)
			if _, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintf(pm.logWriter, "    Warning: ipfs init failed: %v\n", err)
			}

			// Copy swarm key
			swarmKeyPath := filepath.Join(pm.debrosDir, "swarm.key")
			if data, err := os.ReadFile(swarmKeyPath); err == nil {
				os.WriteFile(filepath.Join(nodes[i].ipfsPath, "swarm.key"), data, 0600)
			}
		}

		// Always reapply address settings to ensure correct ports (before daemon starts)
		apiAddr := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", nodes[i].apiPort)
		gatewayAddr := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", nodes[i].gatewayPort)
		swarmAddrs := fmt.Sprintf("[\"/ip4/0.0.0.0/tcp/%d\", \"/ip6/::/tcp/%d\"]", nodes[i].swarmPort, nodes[i].swarmPort)

		if err := exec.CommandContext(ctx, "ipfs", "config", "--repo-dir="+nodes[i].ipfsPath, "Addresses.API", apiAddr).Run(); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to set API address: %v\n", err)
		}
		if err := exec.CommandContext(ctx, "ipfs", "config", "--repo-dir="+nodes[i].ipfsPath, "Addresses.Gateway", gatewayAddr).Run(); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to set Gateway address: %v\n", err)
		}
		if err := exec.CommandContext(ctx, "ipfs", "config", "--repo-dir="+nodes[i].ipfsPath, "--json", "Addresses.Swarm", swarmAddrs).Run(); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to set Swarm addresses: %v\n", err)
		}

		// Read peer ID from config BEFORE daemon starts
		peerID, err := readIPFSConfigValue(ctx, nodes[i].ipfsPath, "PeerID")
		if err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to read peer ID for %s: %v\n", nodes[i].name, err)
		} else {
			nodes[i].peerID = peerID
			fmt.Fprintf(pm.logWriter, "    Peer ID for %s: %s\n", nodes[i].name, peerID)
		}
	}

	// Phase 2: Start all IPFS daemons
	for i := range nodes {
		pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("ipfs-%s.pid", nodes[i].name))
		logPath := filepath.Join(pm.debrosDir, "logs", fmt.Sprintf("ipfs-%s.log", nodes[i].name))

		cmd := exec.CommandContext(ctx, "ipfs", "daemon", "--enable-pubsub-experiment", "--repo-dir="+nodes[i].ipfsPath)
		logFile, _ := os.Create(logPath)
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start ipfs-%s: %w", nodes[i].name, err)
		}

		os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
		pm.processes[fmt.Sprintf("ipfs-%s", nodes[i].name)] = &ManagedProcess{
			Name:      fmt.Sprintf("ipfs-%s", nodes[i].name),
			PID:       cmd.Process.Pid,
			StartTime: time.Now(),
			LogPath:   logPath,
		}

		fmt.Fprintf(pm.logWriter, "✓ IPFS (%s) started (PID: %d, API: %d, Swarm: %d)\n", nodes[i].name, cmd.Process.Pid, nodes[i].apiPort, nodes[i].swarmPort)
	}

	time.Sleep(2 * time.Second)

	// Phase 3: Seed IPFS peers via HTTP API after all daemons are running
	if err := pm.seedIPFSPeersWithHTTP(ctx, nodes); err != nil {
		fmt.Fprintf(pm.logWriter, "⚠️  Failed to seed IPFS peers: %v\n", err)
	}

	return nil
}

func (pm *ProcessManager) startIPFSCluster(ctx context.Context) error {
	nodes := []struct {
		name        string
		clusterPath string
		restAPIPort int
		clusterPort int
		ipfsPort    int
	}{
		{"bootstrap", filepath.Join(pm.debrosDir, "bootstrap/ipfs-cluster"), 9094, 9096, 4501},
		{"node2", filepath.Join(pm.debrosDir, "node2/ipfs-cluster"), 9104, 9106, 4502},
		{"node3", filepath.Join(pm.debrosDir, "node3/ipfs-cluster"), 9114, 9116, 4503},
	}

	// Wait for all IPFS daemons to be ready before starting cluster services
	fmt.Fprintf(pm.logWriter, "  Waiting for IPFS daemons to be ready...\n")
	ipfsNodes := []ipfsNodeInfo{
		{"bootstrap", filepath.Join(pm.debrosDir, "bootstrap/ipfs/repo"), 4501, 4101, 7501, ""},
		{"node2", filepath.Join(pm.debrosDir, "node2/ipfs/repo"), 4502, 4102, 7502, ""},
		{"node3", filepath.Join(pm.debrosDir, "node3/ipfs/repo"), 4503, 4103, 7503, ""},
	}
	for _, ipfsNode := range ipfsNodes {
		if err := pm.waitIPFSReady(ctx, ipfsNode); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: IPFS %s did not become ready: %v\n", ipfsNode.name, err)
		}
	}

	// Read cluster secret to ensure all nodes use the same PSK
	secretPath := filepath.Join(pm.debrosDir, "cluster-secret")
	clusterSecret, err := os.ReadFile(secretPath)
	if err != nil {
		return fmt.Errorf("failed to read cluster secret: %w", err)
	}
	clusterSecretHex := strings.TrimSpace(string(clusterSecret))

	// Phase 1: Initialize and start bootstrap IPFS Cluster, then read its identity
	bootstrapMultiaddr := ""
	{
		node := nodes[0] // bootstrap

		// Always clean stale cluster state to ensure fresh initialization with correct secret
		if err := pm.cleanClusterState(node.clusterPath); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to clean cluster state for %s: %v\n", node.name, err)
		}

		os.MkdirAll(node.clusterPath, 0755)
		fmt.Fprintf(pm.logWriter, "  Initializing IPFS Cluster (%s)...\n", node.name)
		cmd := exec.CommandContext(ctx, "ipfs-cluster-service", "init", "--force")
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("IPFS_CLUSTER_PATH=%s", node.clusterPath),
			fmt.Sprintf("CLUSTER_SECRET=%s", clusterSecretHex),
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: ipfs-cluster-service init failed: %v (output: %s)\n", err, string(output))
		}

		// Ensure correct ports in service.json BEFORE starting daemon
		// This is critical: it sets the cluster listen port to clusterPort, not the default
		if err := pm.ensureIPFSClusterPorts(node.clusterPath, node.restAPIPort, node.clusterPort); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to update IPFS Cluster config for %s: %v\n", node.name, err)
		}

		// Verify the config was written correctly (debug: read it back)
		serviceJSONPath := filepath.Join(node.clusterPath, "service.json")
		if data, err := os.ReadFile(serviceJSONPath); err == nil {
			var verifyConfig map[string]interface{}
			if err := json.Unmarshal(data, &verifyConfig); err == nil {
				if cluster, ok := verifyConfig["cluster"].(map[string]interface{}); ok {
					if listenAddrs, ok := cluster["listen_multiaddress"].([]interface{}); ok {
						fmt.Fprintf(pm.logWriter, "    Config verified: %s cluster listening on %v\n", node.name, listenAddrs)
					}
				}
			}
		}

		// Start bootstrap cluster service
		pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("ipfs-cluster-%s.pid", node.name))
		logPath := filepath.Join(pm.debrosDir, "logs", fmt.Sprintf("ipfs-cluster-%s.log", node.name))

		cmd = exec.CommandContext(ctx, "ipfs-cluster-service", "daemon")
		cmd.Env = append(os.Environ(), fmt.Sprintf("IPFS_CLUSTER_PATH=%s", node.clusterPath))
		logFile, _ := os.Create(logPath)
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		if err := cmd.Start(); err != nil {
			fmt.Fprintf(pm.logWriter, "  ⚠️  Failed to start ipfs-cluster-%s: %v\n", node.name, err)
			return err
		}

		os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
		fmt.Fprintf(pm.logWriter, "✓ IPFS Cluster (%s) started (PID: %d, API: %d)\n", node.name, cmd.Process.Pid, node.restAPIPort)

		// Wait for bootstrap to be ready and read its identity
		if err := pm.waitClusterReady(ctx, node.name, node.restAPIPort); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: IPFS Cluster %s did not become ready: %v\n", node.name, err)
		}

		// Add a brief delay to allow identity.json to be written
		time.Sleep(500 * time.Millisecond)

		// Read bootstrap peer ID for follower nodes to join
		peerID, err := pm.waitForClusterPeerID(ctx, filepath.Join(node.clusterPath, "identity.json"))
		if err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to read bootstrap peer ID: %v\n", err)
		} else {
			bootstrapMultiaddr = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", node.clusterPort, peerID)
			fmt.Fprintf(pm.logWriter, "    Bootstrap multiaddress: %s\n", bootstrapMultiaddr)
		}
	}

	// Phase 2: Initialize and start follower IPFS Cluster nodes with bootstrap flag
	for i := 1; i < len(nodes); i++ {
		node := nodes[i]

		// Always clean stale cluster state to ensure fresh initialization with correct secret
		if err := pm.cleanClusterState(node.clusterPath); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to clean cluster state for %s: %v\n", node.name, err)
		}

		os.MkdirAll(node.clusterPath, 0755)
		fmt.Fprintf(pm.logWriter, "  Initializing IPFS Cluster (%s)...\n", node.name)
		cmd := exec.CommandContext(ctx, "ipfs-cluster-service", "init", "--force")
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("IPFS_CLUSTER_PATH=%s", node.clusterPath),
			fmt.Sprintf("CLUSTER_SECRET=%s", clusterSecretHex),
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: ipfs-cluster-service init failed for %s: %v (output: %s)\n", node.name, err, string(output))
		}

		// Ensure correct ports in service.json BEFORE starting daemon
		if err := pm.ensureIPFSClusterPorts(node.clusterPath, node.restAPIPort, node.clusterPort); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to update IPFS Cluster config for %s: %v\n", node.name, err)
		}

		// Verify the config was written correctly (debug: read it back)
		serviceJSONPath := filepath.Join(node.clusterPath, "service.json")
		if data, err := os.ReadFile(serviceJSONPath); err == nil {
			var verifyConfig map[string]interface{}
			if err := json.Unmarshal(data, &verifyConfig); err == nil {
				if cluster, ok := verifyConfig["cluster"].(map[string]interface{}); ok {
					if listenAddrs, ok := cluster["listen_multiaddress"].([]interface{}); ok {
						fmt.Fprintf(pm.logWriter, "    Config verified: %s cluster listening on %v\n", node.name, listenAddrs)
					}
				}
			}
		}

		// Start follower cluster service with bootstrap flag
		pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("ipfs-cluster-%s.pid", node.name))
		logPath := filepath.Join(pm.debrosDir, "logs", fmt.Sprintf("ipfs-cluster-%s.log", node.name))

		args := []string{"daemon"}
		if bootstrapMultiaddr != "" {
			args = append(args, "--bootstrap", bootstrapMultiaddr)
		}

		cmd = exec.CommandContext(ctx, "ipfs-cluster-service", args...)
		cmd.Env = append(os.Environ(), fmt.Sprintf("IPFS_CLUSTER_PATH=%s", node.clusterPath))
		logFile, _ := os.Create(logPath)
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		if err := cmd.Start(); err != nil {
			fmt.Fprintf(pm.logWriter, "  ⚠️  Failed to start ipfs-cluster-%s: %v\n", node.name, err)
			continue
		}

		os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
		fmt.Fprintf(pm.logWriter, "✓ IPFS Cluster (%s) started (PID: %d, API: %d)\n", node.name, cmd.Process.Pid, node.restAPIPort)

		// Wait for follower node to connect to the bootstrap peer
		if err := pm.waitClusterReady(ctx, node.name, node.restAPIPort); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: IPFS Cluster %s did not become ready: %v\n", node.name, err)
		}
	}

	// Phase 3: Wait for all cluster peers to discover each other
	fmt.Fprintf(pm.logWriter, "  Waiting for IPFS Cluster peers to form...\n")
	if err := pm.waitClusterFormed(ctx, nodes[0].restAPIPort); err != nil {
		fmt.Fprintf(pm.logWriter, "    Warning: IPFS Cluster did not form fully: %v\n", err)
	}

	time.Sleep(1 * time.Second)
	return nil
}

// waitForClusterPeerID polls the identity.json file until it appears and extracts the peer ID
func (pm *ProcessManager) waitForClusterPeerID(ctx context.Context, identityPath string) (string, error) {
	maxRetries := 30
	retryInterval := 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		data, err := os.ReadFile(identityPath)
		if err == nil {
			var identity map[string]interface{}
			if err := json.Unmarshal(data, &identity); err == nil {
				if id, ok := identity["id"].(string); ok {
					return id, nil
				}
			}
		}

		select {
		case <-time.After(retryInterval):
			continue
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	return "", fmt.Errorf("could not read cluster peer ID after %d seconds", (maxRetries * int(retryInterval.Milliseconds()) / 1000))
}

// waitClusterReady polls the cluster REST API until it's ready
func (pm *ProcessManager) waitClusterReady(ctx context.Context, name string, restAPIPort int) error {
	maxRetries := 30
	retryInterval := 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		httpURL := fmt.Sprintf("http://127.0.0.1:%d/peers", restAPIPort)
		resp, err := http.Get(httpURL)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}

		select {
		case <-time.After(retryInterval):
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("IPFS Cluster %s did not become ready after %d seconds", name, (maxRetries * int(retryInterval.Seconds())))
}

// waitClusterFormed waits for all cluster peers to be visible from the bootstrap node
func (pm *ProcessManager) waitClusterFormed(ctx context.Context, bootstrapRestAPIPort int) error {
	maxRetries := 30
	retryInterval := 1 * time.Second
	requiredPeers := 3 // bootstrap, node2, node3

	for attempt := 0; attempt < maxRetries; attempt++ {
		httpURL := fmt.Sprintf("http://127.0.0.1:%d/peers", bootstrapRestAPIPort)
		resp, err := http.Get(httpURL)
		if err == nil && resp.StatusCode == 200 {
			var peers []interface{}
			if err := json.NewDecoder(resp.Body).Decode(&peers); err == nil {
				resp.Body.Close()
				if len(peers) >= requiredPeers {
					return nil // All peers have formed
				}
			} else {
				resp.Body.Close()
			}
		}
		if resp != nil {
			resp.Body.Close()
		}

		select {
		case <-time.After(retryInterval):
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("IPFS Cluster did not form fully after %d seconds", (maxRetries * int(retryInterval.Seconds())))
}

// cleanClusterState removes stale cluster state files to ensure fresh initialization
// This prevents PSK (private network key) mismatches when cluster secret changes
func (pm *ProcessManager) cleanClusterState(clusterPath string) error {
	// Remove pebble datastore (contains persisted PSK state)
	pebblePath := filepath.Join(clusterPath, "pebble")
	if err := os.RemoveAll(pebblePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove pebble directory: %w", err)
	}

	// Remove peerstore (contains peer addresses and metadata)
	peerstorePath := filepath.Join(clusterPath, "peerstore")
	if err := os.Remove(peerstorePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove peerstore: %w", err)
	}

	// Remove service.json (will be regenerated with correct ports and secret)
	serviceJSONPath := filepath.Join(clusterPath, "service.json")
	if err := os.Remove(serviceJSONPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove service.json: %w", err)
	}

	// Remove cluster.lock if it exists (from previous run)
	lockPath := filepath.Join(clusterPath, "cluster.lock")
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove cluster.lock: %w", err)
	}

	// Note: We keep identity.json as it's tied to the node's peer ID
	// The secret will be updated via CLUSTER_SECRET env var during init

	return nil
}

// ensureIPFSClusterPorts updates service.json with correct per-node ports and IPFS connector settings
func (pm *ProcessManager) ensureIPFSClusterPorts(clusterPath string, restAPIPort int, clusterPort int) error {
	serviceJSONPath := filepath.Join(clusterPath, "service.json")

	// Read existing config
	data, err := os.ReadFile(serviceJSONPath)
	if err != nil {
		return fmt.Errorf("failed to read service.json: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to unmarshal service.json: %w", err)
	}

	// Calculate unique ports for this node based on restAPIPort offset
	// bootstrap=9094 -> proxy=9095, pinsvc=9097, cluster=9096
	// node2=9104 -> proxy=9105, pinsvc=9107, cluster=9106
	// node3=9114 -> proxy=9115, pinsvc=9117, cluster=9116
	portOffset := restAPIPort - 9094
	proxyPort := 9095 + portOffset
	pinsvcPort := 9097 + portOffset

	// Infer IPFS port from REST API port
	// 9094 -> 4501 (bootstrap), 9104 -> 4502 (node2), 9114 -> 4503 (node3)
	ipfsPort := 4501 + (portOffset / 10)

	// Update API settings
	if api, ok := config["api"].(map[string]interface{}); ok {
		// Update REST API listen address
		if restapi, ok := api["restapi"].(map[string]interface{}); ok {
			restapi["http_listen_multiaddress"] = fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", restAPIPort)
		}

		// Update IPFS Proxy settings
		if proxy, ok := api["ipfsproxy"].(map[string]interface{}); ok {
			proxy["listen_multiaddress"] = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", proxyPort)
			proxy["node_multiaddress"] = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort)
		}

		// Update Pinning Service API port
		if pinsvc, ok := api["pinsvcapi"].(map[string]interface{}); ok {
			pinsvc["http_listen_multiaddress"] = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", pinsvcPort)
		}
	}

	// Update cluster listen multiaddress to match the correct port
	// Replace all old listen addresses with new ones for the correct port
	if cluster, ok := config["cluster"].(map[string]interface{}); ok {
		listenAddrs := []string{
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", clusterPort),
			fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", clusterPort),
		}
		cluster["listen_multiaddress"] = listenAddrs
	}

	// Update IPFS connector settings to point to correct IPFS API port
	if connector, ok := config["ipfs_connector"].(map[string]interface{}); ok {
		if ipfshttp, ok := connector["ipfshttp"].(map[string]interface{}); ok {
			ipfshttp["node_multiaddress"] = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort)
		}
	}

	// Write updated config
	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal updated config: %w", err)
	}

	if err := os.WriteFile(serviceJSONPath, updatedData, 0644); err != nil {
		return fmt.Errorf("failed to write service.json: %w", err)
	}

	return nil
}

func (pm *ProcessManager) startRQLite(ctx context.Context) error {
	nodes := []struct {
		name     string
		dataDir  string
		httpPort int
		raftPort int
		joinAddr string
	}{
		{"bootstrap", filepath.Join(pm.debrosDir, "bootstrap/rqlite"), 5001, 7001, ""},
		{"node2", filepath.Join(pm.debrosDir, "node2/rqlite"), 5002, 7002, "localhost:7001"},
		{"node3", filepath.Join(pm.debrosDir, "node3/rqlite"), 5003, 7003, "localhost:7001"},
	}

	for _, node := range nodes {
		os.MkdirAll(node.dataDir, 0755)

		pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("rqlite-%s.pid", node.name))
		logPath := filepath.Join(pm.debrosDir, "logs", fmt.Sprintf("rqlite-%s.log", node.name))

		var args []string
		args = append(args, fmt.Sprintf("-http-addr=0.0.0.0:%d", node.httpPort))
		args = append(args, fmt.Sprintf("-http-adv-addr=localhost:%d", node.httpPort))
		args = append(args, fmt.Sprintf("-raft-addr=0.0.0.0:%d", node.raftPort))
		args = append(args, fmt.Sprintf("-raft-adv-addr=localhost:%d", node.raftPort))
		if node.joinAddr != "" {
			args = append(args, "-join", node.joinAddr, "-join-attempts", "30", "-join-interval", "10s")
		}
		args = append(args, node.dataDir)
		cmd := exec.CommandContext(ctx, "rqlited", args...)

		logFile, _ := os.Create(logPath)
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start rqlite-%s: %w", node.name, err)
		}

		os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
		pm.processes[fmt.Sprintf("rqlite-%s", node.name)] = &ManagedProcess{
			Name:      fmt.Sprintf("rqlite-%s", node.name),
			PID:       cmd.Process.Pid,
			StartTime: time.Now(),
			LogPath:   logPath,
		}

		fmt.Fprintf(pm.logWriter, "✓ RQLite (%s) started (PID: %d, HTTP: %d, Raft: %d)\n", node.name, cmd.Process.Pid, node.httpPort, node.raftPort)
	}

	time.Sleep(2 * time.Second)
	return nil
}

func (pm *ProcessManager) startOlric(ctx context.Context) error {
	pidPath := filepath.Join(pm.pidsDir, "olric.pid")
	logPath := filepath.Join(pm.debrosDir, "logs", "olric.log")
	configPath := filepath.Join(pm.debrosDir, "olric-config.yaml")

	cmd := exec.CommandContext(ctx, "olric-server")
	cmd.Env = append(os.Environ(), fmt.Sprintf("OLRIC_SERVER_CONFIG=%s", configPath))
	logFile, _ := os.Create(logPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start olric: %w", err)
	}

	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
	fmt.Fprintf(pm.logWriter, "✓ Olric started (PID: %d)\n", cmd.Process.Pid)

	time.Sleep(1 * time.Second)
	return nil
}

func (pm *ProcessManager) startAnon(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		return nil // Skip on non-macOS for now
	}

	pidPath := filepath.Join(pm.pidsDir, "anon.pid")
	logPath := filepath.Join(pm.debrosDir, "logs", "anon.log")

	cmd := exec.CommandContext(ctx, "npx", "anyone-client")
	logFile, _ := os.Create(logPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(pm.logWriter, "  ⚠️  Failed to start Anon: %v\n", err)
		return nil
	}

	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
	fmt.Fprintf(pm.logWriter, "✓ Anon proxy started (PID: %d, SOCKS: 9050)\n", cmd.Process.Pid)

	return nil
}

func (pm *ProcessManager) startBootstrapNode(ctx context.Context) error {
	return pm.startNode("bootstrap", "bootstrap.yaml", filepath.Join(pm.debrosDir, "logs", "bootstrap.log"))
}

func (pm *ProcessManager) startNode2(ctx context.Context) error {
	return pm.startNode("node2", "node2.yaml", filepath.Join(pm.debrosDir, "logs", "node2.log"))
}

func (pm *ProcessManager) startNode3(ctx context.Context) error {
	return pm.startNode("node3", "node3.yaml", filepath.Join(pm.debrosDir, "logs", "node3.log"))
}

func (pm *ProcessManager) startNode(name, configFile, logPath string) error {
	pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("%s.pid", name))
	cmd := exec.Command("./bin/node", "--config", configFile)
	logFile, _ := os.Create(logPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", name, err)
	}

	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
	fmt.Fprintf(pm.logWriter, "✓ %s started (PID: %d)\n", strings.Title(name), cmd.Process.Pid)

	time.Sleep(1 * time.Second)
	return nil
}

func (pm *ProcessManager) startGateway(ctx context.Context) error {
	pidPath := filepath.Join(pm.pidsDir, "gateway.pid")
	logPath := filepath.Join(pm.debrosDir, "logs", "gateway.log")

	cmd := exec.Command("./bin/gateway", "--config", "gateway.yaml")
	logFile, _ := os.Create(logPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start gateway: %w", err)
	}

	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
	fmt.Fprintf(pm.logWriter, "✓ Gateway started (PID: %d, listen: 6001)\n", cmd.Process.Pid)

	return nil
}

// stopProcess terminates a managed process
func (pm *ProcessManager) stopProcess(name string) error {
	pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("%s.pid", name))
	pidBytes, err := os.ReadFile(pidPath)
	if err != nil {
		return nil // Process not running or PID not found
	}

	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(pidPath)
		return nil
	}

	proc.Signal(os.Interrupt)
	os.Remove(pidPath)

	fmt.Fprintf(pm.logWriter, "✓ %s stopped\n", name)
	return nil
}

// checkProcessRunning checks if a process with given PID is running
func checkProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Send signal 0 to check if process exists (doesn't actually send signal)
	err = proc.Signal(os.Signal(nil))
	return err == nil
}
