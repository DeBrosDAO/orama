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
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
	"github.com/DeBrosOfficial/network/pkg/environments/production"
	"github.com/DeBrosOfficial/network/pkg/systemd"
)

// newOramaBinaryPath is the on-disk path Phase 2b installs the new
// orama binary to. Re-exec target for bugboard #15 chicken-and-egg fix.
const newOramaBinaryPath = "/opt/orama/bin/orama"

// Orchestrator manages the upgrade process
type Orchestrator struct {
	oramaHome string
	oramaDir  string
	setup     *production.ProductionSetup
	flags     *Flags
}

// NewOrchestrator creates a new upgrade orchestrator
func NewOrchestrator(flags *Flags) *Orchestrator {
	oramaHome := production.OramaBase
	oramaDir := production.OramaDir

	// Load existing preferences
	prefs := production.LoadPreferences(oramaDir)

	// Use saved nameserver preference if not explicitly specified
	isNameserver := prefs.Nameserver
	if flags.Nameserver != nil {
		isNameserver = *flags.Nameserver
	}

	setup := production.NewProductionSetup(oramaHome, os.Stdout, flags.Force, flags.SkipChecks)
	setup.SetNameserver(isNameserver)

	// Anyone is client-only. Always enable the SOCKS client so a bare
	// `orama node upgrade --restart` does not disable /v1/proxy/anon.
	setup.SetAnyoneClient(true)

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
	if o.flags.ReexecedAfterBinarySwap {
		fmt.Printf("  (Resumed under newly-installed binary — bug #15 chicken-and-egg fix.)\n")
		fmt.Printf("  Skipping Phase 1/2/2b (already done by previous process); Phase 3+ runs here.\n")
	} else {
		fmt.Printf("  This will preserve existing configurations and data\n")
		fmt.Printf("  Configurations will be updated to latest format\n\n")
	}

	// Phases 1, 2, 2b are skipped on the re-execed run — already
	// performed by the prior (old-binary) process. Phase 3 (secrets)
	// onward runs here, deliberately under the new binary so Phase 4
	// (config regen, the actual point of the re-exec) uses current code.
	if !o.flags.ReexecedAfterBinarySwap {
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
	}

	// Bugboard #15 fix — chicken-and-egg.
	//
	// Up to here we are still running the OLD orama binary's compiled
	// code. The next phases (3 secrets, 4 configs, 5 systemd) include
	// Phase4GenerateConfigs which is COMPILED into this process. If we
	// keep running, those phases use OLD logic and any config-shape
	// changes shipped in this release only take effect on the NEXT
	// upgrade.
	//
	// Re-exec the just-installed binary with the same args + a hidden
	// marker so it skips the pre-binary phases (already done above) and
	// runs Phase 3+ with its OWN up-to-date code. syscall.Exec replaces
	// this process — control never returns past it on success.
	if !o.flags.ReexecedAfterBinarySwap {
		if err := o.reexecAfterBinarySwap(); err != nil {
			// Soft-fail: log and continue with old-binary phases as a
			// fallback. Operator gets a clear warning that the chicken-
			// and-egg fix didn't apply for this run.
			fmt.Fprintf(os.Stderr, "⚠️  Could not re-exec post-binary-swap (%v); "+
				"continuing with current binary — config changes from this release "+
				"may only take effect on the NEXT upgrade. See bugboard #15.\n", err)
		}
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

	// Install namespace systemd template units
	fmt.Printf("\n🔧 Phase 5b: Installing namespace systemd templates...\n")
	if err := o.installNamespaceTemplates(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Template installation warning: %v\n", err)
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
	fmt.Printf("   sudo systemctl restart orama-*\n")
	fmt.Printf("\n")

	return nil
}

func (o *Orchestrator) handleBranchPreferences() error {
	// Load current preferences
	prefs := production.LoadPreferences(o.oramaDir)
	prefsChanged := false

	// If nameserver was explicitly provided, update it
	if o.flags.Nameserver != nil {
		prefs.Nameserver = *o.flags.Nameserver
		prefsChanged = true
	}
	if o.setup.IsNameserver() {
		fmt.Printf("  Nameserver mode: enabled (CoreDNS + Caddy)\n")
	}

	if !prefs.AnyoneClient {
		prefs.AnyoneClient = true
		prefsChanged = true
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
	Nodes      []ClusterNode `json:"nodes"`
	CapturedAt time.Time     `json:"captured_at"`
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
	raftDir := filepath.Join(production.OramaData, "rqlite", "raft")
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

	// First, stop all namespace services (orama-namespace-*@*.service)
	fmt.Printf("  Stopping namespace services...\n")
	if err := o.stopAllNamespaceServices(serviceController); err != nil {
		fmt.Printf("  ⚠️  Warning: Failed to stop namespace services: %v\n", err)
	}

	// Stop services in reverse dependency order
	services := []string{
		"caddy.service",               // Depends on node
		"coredns.service",             // Depends on node
		"orama-gateway.service",       // Legacy
		"orama-node.service",          // Depends on cluster, olric
		"orama-ipfs-cluster.service",  // Depends on IPFS
		"orama-ipfs.service",          // Base IPFS
		"orama-olric.service",         // Independent
		"orama-vault.service",         // Vault guardian
		"orama-anyone-client.service", // Client mode
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

// stopAllNamespaceServices stops all running namespace services
func (o *Orchestrator) stopAllNamespaceServices(serviceController *production.SystemdController) error {
	// Find all running namespace services using systemctl list-units
	cmd := exec.Command("systemctl", "list-units", "--type=service", "--state=running", "--no-pager", "--no-legend", "orama-namespace-*@*.service")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list namespace services: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	stoppedCount := 0
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			serviceName := fields[0]
			if strings.HasPrefix(serviceName, "orama-namespace-") {
				if err := serviceController.StopService(serviceName); err != nil {
					fmt.Printf("    ⚠️  Warning: Failed to stop %s: %v\n", serviceName, err)
				} else {
					stoppedCount++
				}
			}
		}
	}

	if stoppedCount > 0 {
		fmt.Printf("  ✓ Stopped %d namespace service(s)\n", stoppedCount)
	}

	return nil
}

// installNamespaceTemplates installs systemd template unit files for namespace services
func (o *Orchestrator) installNamespaceTemplates() error {
	// Check pre-built archive path first, fall back to source path
	sourceDir := production.OramaSystemdDir
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		sourceDir = filepath.Join(o.oramaHome, "src", "systemd")
	}
	systemdDir := "/etc/systemd/system"

	templates := systemd.UnitFilesToInstall()

	installedCount := 0
	for _, template := range templates {
		sourcePath := filepath.Join(sourceDir, template)
		destPath := filepath.Join(systemdDir, template)

		// Read template file
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			fmt.Printf("  ⚠️  Warning: Failed to read %s: %v\n", template, err)
			continue
		}

		// Write to systemd directory
		if err := os.WriteFile(destPath, data, 0644); err != nil {
			fmt.Printf("  ⚠️  Warning: Failed to install %s: %v\n", template, err)
			continue
		}

		installedCount++
		fmt.Printf("  ✓ Installed %s\n", template)
	}

	if installedCount > 0 {
		// Reload systemd daemon to pick up new templates
		if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
			return fmt.Errorf("failed to reload systemd daemon: %w", err)
		}
		fmt.Printf("  ✓ Systemd daemon reloaded (%d templates installed)\n", installedCount)
	}

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

// reexecAfterBinarySwap replaces this process with the newly-installed
// orama binary at /opt/orama/bin/orama, preserving all original CLI args
// and appending --reexeced-after-binary-swap so the new process knows
// to skip the pre-binary phases. Bugboard #15 chicken-and-egg fix.
//
// Returns nil only when syscall.Exec is about to take effect; on success
// the function never actually returns (the process image is replaced).
// On any failure before the exec syscall, returns the wrapping error so
// the caller can fall back to running the rest of the upgrade with the
// old binary (with a warning).
func (o *Orchestrator) reexecAfterBinarySwap() error {
	if _, err := os.Stat(newOramaBinaryPath); err != nil {
		return fmt.Errorf("new binary not found at %s: %w", newOramaBinaryPath, err)
	}
	// Defensive: don't re-exec ourselves into a loop if the install
	// somehow placed our currently-running binary at that path. Compare
	// inode-stable identity via os.Stat.
	if cur, err := os.Executable(); err == nil {
		curInfo, e1 := os.Stat(cur)
		newInfo, e2 := os.Stat(newOramaBinaryPath)
		if e1 == nil && e2 == nil && os.SameFile(curInfo, newInfo) {
			// Already running the new binary (e.g. someone manually pre-
			// installed it). No re-exec needed.
			fmt.Printf("  (current binary already matches installed binary; skipping re-exec)\n")
			return nil
		}
	}

	args := append([]string{newOramaBinaryPath}, os.Args[1:]...)
	args = append(args, "--reexeced-after-binary-swap")
	fmt.Printf("\n🔁 Re-executing with newly-installed binary to run remaining phases with current code (#15 fix)...\n")
	// syscall.Exec replaces this process image; argv[0] is the binary
	// path, env inherited as-is. On success we never return.
	if err := syscall.Exec(newOramaBinaryPath, args, os.Environ()); err != nil {
		return fmt.Errorf("syscall.Exec %s: %w", newOramaBinaryPath, err)
	}
	return nil
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

	// Unmask and re-enable all services BEFORE restarting them.
	// "orama node stop" masks services (symlinks unit to /dev/null) to prevent
	// Restart=always from reviving them. We must unmask first, then re-enable,
	// so that all services (including namespace services) can actually start.
	for _, svc := range services {
		if masked, err := utils.IsServiceMasked(svc); err == nil && masked {
			if err := exec.Command("systemctl", "unmask", svc).Run(); err != nil {
				fmt.Printf("   ⚠️  Warning: Failed to unmask %s: %v\n", svc, err)
			}
		}
		if err := exec.Command("systemctl", "enable", svc).Run(); err != nil {
			fmt.Printf("   ⚠️  Warning: Failed to re-enable %s: %v\n", svc, err)
		}
	}

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
		"orama-node",         // Start node first - contains RQLite cluster
		"orama-olric",        // Distributed cache
		"orama-ipfs",         // IPFS daemon
		"orama-ipfs-cluster", // IPFS cluster
		"orama-vault",        // Vault guardian
		"orama-gateway",      // Gateway (legacy)
		"coredns",            // DNS server
		"caddy",              // Reverse proxy
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
				if svc == "orama-node" {
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

	// Restart remaining services (namespace + any others) in dependency order.
	// Namespace services are restarted: rqlite → olric (+ wait) → gateway.
	// Without ordering, the gateway starts before Olric is accepting connections,
	// the Olric client initialization fails, and the cache stays permanently unavailable.
	var remaining []string
	for _, svc := range services {
		isPriority := false
		for _, priority := range priorityOrder {
			if svc == priority {
				isPriority = true
				break
			}
		}
		if !isPriority {
			remaining = append(remaining, svc)
		}
	}
	utils.StartServicesOrdered(remaining, "restart")

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
