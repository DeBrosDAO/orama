package upgrade

import (
	"fmt"
	"net"
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

	setup := production.NewProductionSetup(oramaHome, os.Stdout, flags.Force, branch, flags.NoPull, false)
	setup.SetNameserver(isNameserver)

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
	enableHTTPS, _ := o.extractGatewayConfig()
	if err := o.setup.Phase5CreateSystemdServices(enableHTTPS); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Service update warning: %v\n", err)
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

func (o *Orchestrator) stopServices() error {
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
		"debros-anyone-client.service", // Independent
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

func (o *Orchestrator) extractGatewayConfig() (enableHTTPS bool, domain string) {
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
	return enableHTTPS, domain
}

func (o *Orchestrator) regenerateConfigs() error {
	peers := o.extractPeers()
	vpsIP, joinAddress := o.extractNetworkConfig()
	enableHTTPS, domain := o.extractGatewayConfig()

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
	if joinAddress != "" {
		fmt.Printf("    - Join address: %s\n", joinAddress)
	}

	// Phase 4: Generate configs
	if err := o.setup.Phase4GenerateConfigs(peers, vpsIP, enableHTTPS, domain, joinAddress); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Config generation warning: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Existing configs preserved\n")
	}

	return nil
}

func (o *Orchestrator) restartServices() error {
	fmt.Printf("   Restarting services...\n")
	// Reload systemd daemon
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "   ⚠️  Warning: Failed to reload systemd daemon: %v\n", err)
	}

	// Restart services to apply changes - use getProductionServices to only restart existing services
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
	} else {
		for _, svc := range services {
			if err := exec.Command("systemctl", "restart", svc).Run(); err != nil {
				fmt.Printf("   ⚠️  Failed to restart %s: %v\n", svc, err)
			} else {
				fmt.Printf("   ✓ Restarted %s\n", svc)
			}
		}
		fmt.Printf("   ✓ All services restarted\n")
	}

	return nil
}
