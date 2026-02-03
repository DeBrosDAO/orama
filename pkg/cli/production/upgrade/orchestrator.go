package upgrade

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
	"github.com/DeBrosOfficial/network/pkg/environments/production"
)

// Orchestrator manages the upgrade process
type Orchestrator struct {
	oramaHome string
	oramaDir  string
	setup     *production.ProductionSetup
	flags     *Flags
}

// NewOrchestrator creates a new upgrade orchestrator
func NewOrchestrator(flags *Flags) *Orchestrator {
	oramaHome := "/home/debros"
	oramaDir := oramaHome + "/.orama"

	// Load existing preferences
	prefs := production.LoadPreferences(oramaDir)

	// Use saved branch if not specified
	branch := flags.Branch
	if branch == "" {
		branch = prefs.Branch
	}

	// Use saved nameserver preference if not explicitly specified
	isNameserver := prefs.Nameserver
	if flags.Nameserver != nil {
		isNameserver = *flags.Nameserver
	}

	setup := production.NewProductionSetup(oramaHome, os.Stdout, flags.Force, branch, flags.NoPull, false, flags.PreBuilt)
	setup.SetNameserver(isNameserver)

	// Configure Anyone relay if enabled
	if flags.AnyoneRelay {
		setup.SetAnyoneRelayConfig(&production.AnyoneRelayConfig{
			Enabled:  true,
			Exit:     flags.AnyoneExit,
			Migrate:  flags.AnyoneMigrate,
			Nickname: flags.AnyoneNickname,
			Contact:  flags.AnyoneContact,
			Wallet:   flags.AnyoneWallet,
			ORPort:   flags.AnyoneORPort,
			MyFamily: flags.AnyoneFamily,
		})
	}

	return &Orchestrator{
		oramaHome: oramaHome,
		oramaDir:  oramaDir,
		setup:     setup,
		flags:     flags,
	}
}

// Execute runs the upgrade process
func (o *Orchestrator) Execute() error {
	fmt.Printf("🔄 Upgrading production installation...\n")
	fmt.Printf("  This will preserve existing configurations and data\n")
	fmt.Printf("  Configurations will be updated to latest format\n\n")

	// Log if --no-pull is enabled
	if o.flags.NoPull {
		fmt.Printf("  ⚠️  --no-pull flag enabled: Skipping git clone/pull\n")
		fmt.Printf("     Using existing repository at %s/src\n", o.oramaHome)
	}

	// Log if --pre-built is enabled
	if o.flags.PreBuilt {
		fmt.Printf("  ⚠️  --pre-built flag enabled: Skipping all Go compilation\n")
		fmt.Printf("     Using pre-built binaries from %s/bin and /usr/local/bin\n", o.oramaHome)
	}

	// Handle branch preferences
	if err := o.handleBranchPreferences(); err != nil {
		return err
	}

	// Phase 1: Check prerequisites
	fmt.Printf("\n📋 Phase 1: Checking prerequisites...\n")
	if err := o.setup.Phase1CheckPrerequisites(); err != nil {
		return fmt.Errorf("prerequisites check failed: %w", err)
	}

	// Phase 2: Provision environment
	fmt.Printf("\n🛠️  Phase 2: Provisioning environment...\n")
	if err := o.setup.Phase2ProvisionEnvironment(); err != nil {
		return fmt.Errorf("environment provisioning failed: %w", err)
	}

	// Stop services before upgrading binaries
	if o.setup.IsUpdate() {
		if err := o.stopServices(); err != nil {
			return err
		}
	}

	// Check port availability after stopping services
	if err := utils.EnsurePortsAvailable("prod upgrade", utils.DefaultPorts()); err != nil {
		return err
	}

	// Phase 2b: Install/update binaries
	fmt.Printf("\nPhase 2b: Installing/updating binaries...\n")
	if err := o.setup.Phase2bInstallBinaries(); err != nil {
		return fmt.Errorf("binary installation failed: %w", err)
	}

	// Detect existing installation
	if o.setup.IsUpdate() {
		fmt.Printf("  Detected existing installation\n")
	} else {
		fmt.Printf("  ⚠️  No existing installation detected, treating as fresh install\n")
		fmt.Printf("  Use 'orama install' for fresh installation\n")
	}

	// Phase 3: Ensure secrets exist
	fmt.Printf("\n🔐 Phase 3: Ensuring secrets...\n")
	if err := o.setup.Phase3GenerateSecrets(); err != nil {
		return fmt.Errorf("secret generation failed: %w", err)
	}

	// Phase 4: Regenerate configs
	if err := o.regenerateConfigs(); err != nil {
		return err
	}

	// Phase 2c: Ensure services are properly initialized
	fmt.Printf("\nPhase 2c: Ensuring services are properly initialized...\n")
	peers := o.extractPeers()
	vpsIP, _ := o.extractNetworkConfig()
	if err := o.setup.Phase2cInitializeServices(peers, vpsIP, nil, nil); err != nil {
		return fmt.Errorf("service initialization failed: %w", err)
	}

	// Phase 5: Update systemd services
	fmt.Printf("\n🔧 Phase 5: Updating systemd services...\n")
	enableHTTPS, _, _ := o.extractGatewayConfig()
	if err := o.setup.Phase5CreateSystemdServices(enableHTTPS); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Service update warning: %v\n", err)
	}

	// Re-apply UFW firewall rules (idempotent)
	fmt.Printf("\n🛡️  Re-applying firewall rules...\n")
	if err := o.setup.Phase6bSetupFirewall(false); err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠️  Warning: Firewall re-apply failed: %v\n", err)
	}

	fmt.Printf("\n✅ Upgrade complete!\n")

	// Restart services if requested
	if o.flags.RestartServices {
		return o.restartServices()
	}

	fmt.Printf("   To apply changes, restart services:\n")
	fmt.Printf("   sudo systemctl daemon-reload\n")
	fmt.Printf("   sudo systemctl restart debros-*\n")
	fmt.Printf("\n")

	return nil
}

func (o *Orchestrator) handleBranchPreferences() error {
	// Load current preferences
	prefs := production.LoadPreferences(o.oramaDir)
	prefsChanged := false

	// If branch was explicitly provided, update it
	if o.flags.Branch != "" {
		prefs.Branch = o.flags.Branch
		prefsChanged = true
		fmt.Printf("  Using branch: %s (saved for future upgrades)\n", o.flags.Branch)
	} else {
		fmt.Printf("  Using branch: %s (from saved preference)\n", prefs.Branch)
	}

	// If nameserver was explicitly provided, update it
	if o.flags.Nameserver != nil {
		prefs.Nameserver = *o.flags.Nameserver
		prefsChanged = true
	}
	if o.setup.IsNameserver() {
		fmt.Printf("  Nameserver mode: enabled (CoreDNS + Caddy)\n")
	}

	// Save preferences if anything changed
	if prefsChanged {
		if err := production.SavePreferences(o.oramaDir, prefs); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Warning: Failed to save preferences: %v\n", err)
		}
	}
	return nil
}

// ClusterState represents the saved state of the RQLite cluster before shutdown
type ClusterState struct {
	Nodes     []ClusterNode `json:"nodes"`
	CapturedAt time.Time    `json:"captured_at"`
}

// ClusterNode represents a node in the cluster
type ClusterNode struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	Voter     bool   `json:"voter"`
	Reachable bool   `json:"reachable"`
}

// captureClusterState saves the current RQLite cluster state before stopping services
// This allows nodes to recover cluster membership faster after restart
func (o *Orchestrator) captureClusterState() error {
	fmt.Printf("\n📸 Capturing cluster state before shutdown...\n")

	// Query RQLite /nodes endpoint to get current cluster membership
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:5001/nodes?timeout=3s")
	if err != nil {
		fmt.Printf("  ⚠️  Could not query cluster state: %v\n", err)
		return nil // Non-fatal - continue with upgrade
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("  ⚠️  RQLite returned status %d\n", resp.StatusCode)
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("  ⚠️  Could not read cluster state: %v\n", err)
		return nil
	}

	// Parse the nodes response
	var nodes map[string]struct {
		Addr      string `json:"addr"`
		Voter     bool   `json:"voter"`
		Reachable bool   `json:"reachable"`
	}
	if err := json.Unmarshal(body, &nodes); err != nil {
		fmt.Printf("  ⚠️  Could not parse cluster state: %v\n", err)
		return nil
	}

	// Build cluster state
	state := ClusterState{
		Nodes:      make([]ClusterNode, 0, len(nodes)),
		CapturedAt: time.Now(),
	}

	for id, node := range nodes {
		state.Nodes = append(state.Nodes, ClusterNode{
			ID:        id,
			Address:   node.Addr,
			Voter:     node.Voter,
			Reachable: node.Reachable,
		})
		fmt.Printf("  Found node: %s (voter=%v, reachable=%v)\n", id, node.Voter, node.Reachable)
	}

	// Save to file
	stateFile := filepath.Join(o.oramaDir, "cluster-state.json")
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		fmt.Printf("  ⚠️  Could not marshal cluster state: %v\n", err)
		return nil
	}

	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		fmt.Printf("  ⚠️  Could not save cluster state: %v\n", err)
		return nil
	}

	fmt.Printf("  ✓ Cluster state saved (%d nodes) to %s\n", len(state.Nodes), stateFile)

	// Also write peers.json directly for RQLite recovery
	if err := o.writePeersJSONFromState(state); err != nil {
		fmt.Printf("  ⚠️  Could not write peers.json: %v\n", err)
	} else {
		fmt.Printf("  ✓ peers.json written for cluster recovery\n")
	}

	return nil
}

// writePeersJSONFromState writes RQLite's peers.json file from captured cluster state
func (o *Orchestrator) writePeersJSONFromState(state ClusterState) error {
	// Build peers.json format
	peers := make([]map[string]interface{}, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		peers = append(peers, map[string]interface{}{
			"id":        node.ID,
			"address":   node.ID, // RQLite uses raft address as both id and address
			"non_voter": !node.Voter,
		})
	}

	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return err
	}

	// Write to RQLite's raft directory
	raftDir := filepath.Join(o.oramaHome, ".orama", "data", "rqlite", "raft")
	if err := os.MkdirAll(raftDir, 0755); err != nil {
		return err
	}

	peersFile := filepath.Join(raftDir, "peers.json")
	return os.WriteFile(peersFile, data, 0644)
}

func (o *Orchestrator) stopServices() error {
	// Capture cluster state BEFORE stopping services
	_ = o.captureClusterState()

	fmt.Printf("\n⏹️  Stopping all services before upgrade...\n")
	serviceController := production.NewSystemdController()
	// Stop services in reverse dependency order
	services := []string{
		"caddy.service",              // Depends on node
		"coredns.service",            // Depends on node
		"debros-gateway.service",     // Legacy
		"debros-node.service",        // Depends on cluster, olric
		"debros-ipfs-cluster.service", // Depends on IPFS
		"debros-ipfs.service",        // Base IPFS
		"debros-olric.service",       // Independent
		"debros-anyone-client.service", // Client mode
		"debros-anyone-relay.service",  // Relay mode
	}
	for _, svc := range services {
		unitPath := filepath.Join("/etc/systemd/system", svc)
		if _, err := os.Stat(unitPath); err == nil {
			if err := serviceController.StopService(svc); err != nil {
				fmt.Printf("  ⚠️  Warning: Failed to stop %s: %v\n", svc, err)
			} else {
				fmt.Printf("  ✓ Stopped %s\n", svc)
			}
		}
	}
	// Give services time to shut down gracefully
	time.Sleep(3 * time.Second)
	return nil
}

func (o *Orchestrator) extractPeers() []string {
	nodeConfigPath := filepath.Join(o.oramaDir, "configs", "node.yaml")
	var peers []string
	if data, err := os.ReadFile(nodeConfigPath); err == nil {
		configStr := string(data)
		inPeersList := false
		for _, line := range strings.Split(configStr, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "bootstrap_peers:") || strings.HasPrefix(trimmed, "peers:") {
				inPeersList = true
				continue
			}
			if inPeersList {
				if strings.HasPrefix(trimmed, "-") {
					// Extract multiaddr after the dash
					parts := strings.SplitN(trimmed, "-", 2)
					if len(parts) > 1 {
						peer := strings.TrimSpace(parts[1])
						peer = strings.Trim(peer, "\"'")
						if peer != "" && strings.HasPrefix(peer, "/") {
							peers = append(peers, peer)
						}
					}
				} else if trimmed == "" || !strings.HasPrefix(trimmed, "-") {
					// End of peers list
					break
				}
			}
		}
	}
	return peers
}

func (o *Orchestrator) extractNetworkConfig() (vpsIP, joinAddress string) {
	nodeConfigPath := filepath.Join(o.oramaDir, "configs", "node.yaml")
	if data, err := os.ReadFile(nodeConfigPath); err == nil {
		configStr := string(data)
		for _, line := range strings.Split(configStr, "\n") {
			trimmed := strings.TrimSpace(line)
			// Try to extract VPS IP from http_adv_address or raft_adv_address
			if vpsIP == "" && (strings.HasPrefix(trimmed, "http_adv_address:") || strings.HasPrefix(trimmed, "raft_adv_address:")) {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) > 1 {
					addr := strings.TrimSpace(parts[1])
					addr = strings.Trim(addr, "\"'")
					if addr != "" && addr != "null" && addr != "localhost:5001" && addr != "localhost:7001" {
						// Extract IP from address (format: "IP:PORT" or "[IPv6]:PORT")
						if host, _, err := net.SplitHostPort(addr); err == nil && host != "" && host != "localhost" {
							vpsIP = host
						}
					}
				}
			}
			// Extract join address
			if strings.HasPrefix(trimmed, "rqlite_join_address:") {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) > 1 {
					joinAddress = strings.TrimSpace(parts[1])
					joinAddress = strings.Trim(joinAddress, "\"'")
					if joinAddress == "null" || joinAddress == "" {
						joinAddress = ""
					}
				}
			}
		}
	}
	return vpsIP, joinAddress
}

func (o *Orchestrator) extractGatewayConfig() (enableHTTPS bool, domain string, baseDomain string) {
	gatewayConfigPath := filepath.Join(o.oramaDir, "configs", "gateway.yaml")
	if data, err := os.ReadFile(gatewayConfigPath); err == nil {
		configStr := string(data)
		if strings.Contains(configStr, "domain:") {
			for _, line := range strings.Split(configStr, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "domain:") {
					parts := strings.SplitN(trimmed, ":", 2)
					if len(parts) > 1 {
						domain = strings.TrimSpace(parts[1])
						if domain != "" && domain != "\"\"" && domain != "''" && domain != "null" {
							domain = strings.Trim(domain, "\"'")
							enableHTTPS = true
						} else {
							domain = ""
						}
					}
					break
				}
			}
		}
	}

	// Also check node.yaml for domain and base_domain
	nodeConfigPath := filepath.Join(o.oramaDir, "configs", "node.yaml")
	if data, err := os.ReadFile(nodeConfigPath); err == nil {
		configStr := string(data)
		for _, line := range strings.Split(configStr, "\n") {
			trimmed := strings.TrimSpace(line)
			// Extract domain from node.yaml (under node: section) if not already found
			if domain == "" && strings.HasPrefix(trimmed, "domain:") && !strings.HasPrefix(trimmed, "domain_") {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) > 1 {
					d := strings.TrimSpace(parts[1])
					d = strings.Trim(d, "\"'")
					if d != "" && d != "null" {
						domain = d
						enableHTTPS = true
					}
				}
			}
			if strings.HasPrefix(trimmed, "base_domain:") {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) > 1 {
					baseDomain = strings.TrimSpace(parts[1])
					baseDomain = strings.Trim(baseDomain, "\"'")
					if baseDomain == "null" || baseDomain == "" {
						baseDomain = ""
					}
				}
			}
		}
	}

	return enableHTTPS, domain, baseDomain
}

func (o *Orchestrator) regenerateConfigs() error {
	peers := o.extractPeers()
	vpsIP, joinAddress := o.extractNetworkConfig()
	enableHTTPS, domain, baseDomain := o.extractGatewayConfig()

	fmt.Printf("  Preserving existing configuration:\n")
	if len(peers) > 0 {
		fmt.Printf("    - Peers: %d peer(s) preserved\n", len(peers))
	}
	if vpsIP != "" {
		fmt.Printf("    - VPS IP: %s\n", vpsIP)
	}
	if domain != "" {
		fmt.Printf("    - Domain: %s\n", domain)
	}
	if baseDomain != "" {
		fmt.Printf("    - Base domain: %s\n", baseDomain)
	}
	if joinAddress != "" {
		fmt.Printf("    - Join address: %s\n", joinAddress)
	}

	// Phase 4: Generate configs
	if err := o.setup.Phase4GenerateConfigs(peers, vpsIP, enableHTTPS, domain, baseDomain, joinAddress); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Config generation warning: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Existing configs preserved\n")
	}

	return nil
}

func (o *Orchestrator) restartServices() error {
	fmt.Printf("\n🔄 Restarting services with rolling restart...\n")

	// Reload systemd daemon
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "   ⚠️  Warning: Failed to reload systemd daemon: %v\n", err)
	}

	// Get services to restart
	services := utils.GetProductionServices()

	// If this is a nameserver, also restart CoreDNS and Caddy
	if o.setup.IsNameserver() {
		nameserverServices := []string{"coredns", "caddy"}
		for _, svc := range nameserverServices {
			unitPath := filepath.Join("/etc/systemd/system", svc+".service")
			if _, err := os.Stat(unitPath); err == nil {
				services = append(services, svc)
			}
		}
	}

	if len(services) == 0 {
		fmt.Printf("   ⚠️  No services found to restart\n")
		return nil
	}

	// Define the order for rolling restart - node service first (contains RQLite)
	// This ensures the cluster can reform before other services start
	priorityOrder := []string{
		"debros-node",         // Start node first - contains RQLite cluster
		"debros-olric",        // Distributed cache
		"debros-ipfs",         // IPFS daemon
		"debros-ipfs-cluster", // IPFS cluster
		"debros-gateway",      // Gateway (legacy)
		"coredns",             // DNS server
		"caddy",               // Reverse proxy
	}

	// Restart services in priority order with health checks
	for _, priority := range priorityOrder {
		for _, svc := range services {
			if svc == priority {
				fmt.Printf("   Starting %s...\n", svc)
				if err := exec.Command("systemctl", "restart", svc).Run(); err != nil {
					fmt.Printf("   ⚠️  Failed to restart %s: %v\n", svc, err)
					continue
				}
				fmt.Printf("   ✓ Started %s\n", svc)

				// For the node service, wait for RQLite cluster health
				if svc == "debros-node" {
					fmt.Printf("   Waiting for RQLite cluster to become healthy...\n")
					if err := o.waitForClusterHealth(2 * time.Minute); err != nil {
						fmt.Printf("   ⚠️  Cluster health check warning: %v\n", err)
						fmt.Printf("   Continuing with restart (cluster may recover)...\n")
					} else {
						fmt.Printf("   ✓ RQLite cluster is healthy\n")
					}
				}
				break
			}
		}
	}

	// Start any remaining services not in priority list
	for _, svc := range services {
		found := false
		for _, priority := range priorityOrder {
			if svc == priority {
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("   Starting %s...\n", svc)
			if err := exec.Command("systemctl", "restart", svc).Run(); err != nil {
				fmt.Printf("   ⚠️  Failed to restart %s: %v\n", svc, err)
			} else {
				fmt.Printf("   ✓ Started %s\n", svc)
			}
		}
	}

	fmt.Printf("   ✓ All services restarted\n")

	// Seed DNS records after services are running (RQLite must be up)
	if o.setup.IsNameserver() {
		fmt.Printf("   Seeding DNS records...\n")

		_, _, baseDomain := o.extractGatewayConfig()
		peers := o.extractPeers()
		vpsIP, _ := o.extractNetworkConfig()

		if err := o.setup.SeedDNSRecords(baseDomain, vpsIP, peers); err != nil {
			fmt.Fprintf(os.Stderr, "   ⚠️  Warning: Failed to seed DNS records: %v\n", err)
		} else {
			fmt.Printf("   ✓ DNS records seeded\n")
		}
	}

	return nil
}

// waitForClusterHealth waits for the RQLite cluster to become healthy
func (o *Orchestrator) waitForClusterHealth(timeout time.Duration) error {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// Query RQLite status
		resp, err := client.Get("http://localhost:5001/status")
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		// Parse status response
		var status struct {
			Store struct {
				Raft struct {
					State    string `json:"state"`
					NumPeers int    `json:"num_peers"`
				} `json:"raft"`
			} `json:"store"`
		}

		if err := json.Unmarshal(body, &status); err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		raftState := status.Store.Raft.State
		numPeers := status.Store.Raft.NumPeers

		// Cluster is healthy if we're a Leader or Follower (not Candidate)
		if raftState == "Leader" || raftState == "Follower" {
			fmt.Printf("   RQLite state: %s (peers: %d)\n", raftState, numPeers)
			return nil
		}

		fmt.Printf("   RQLite state: %s (waiting for Leader/Follower)...\n", raftState)
		time.Sleep(3 * time.Second)
	}

	return fmt.Errorf("timeout waiting for cluster to become healthy")
}
