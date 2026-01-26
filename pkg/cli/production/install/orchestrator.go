package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
	"github.com/DeBrosOfficial/network/pkg/environments/production"
)

// Orchestrator manages the install process
type Orchestrator struct {
	oramaHome string
	oramaDir  string
	setup     *production.ProductionSetup
	flags     *Flags
	validator *Validator
	peers     []string
}

// NewOrchestrator creates a new install orchestrator
func NewOrchestrator(flags *Flags) (*Orchestrator, error) {
	oramaHome := "/home/debros"
	oramaDir := oramaHome + "/.orama"

	// Normalize peers
	peers, err := utils.NormalizePeers(flags.PeersStr)
	if err != nil {
		return nil, fmt.Errorf("invalid peers: %w", err)
	}

	setup := production.NewProductionSetup(oramaHome, os.Stdout, flags.Force, flags.Branch, flags.NoPull, flags.SkipChecks)
	setup.SetNameserver(flags.Nameserver)
	validator := NewValidator(flags, oramaDir)

	return &Orchestrator{
		oramaHome: oramaHome,
		oramaDir:  oramaDir,
		setup:     setup,
		flags:     flags,
		validator: validator,
		peers:     peers,
	}, nil
}

// Execute runs the installation process
func (o *Orchestrator) Execute() error {
	fmt.Printf("🚀 Starting production installation...\n\n")

	// Inform user if skipping git pull
	if o.flags.NoPull {
		fmt.Printf("  ⚠️  --no-pull flag enabled: Skipping git clone/pull\n")
		fmt.Printf("     Using existing repository at /home/debros/src\n")
	}

	// Validate DNS if domain is provided
	o.validator.ValidateDNS()

	// Dry-run mode: show what would be done and exit
	if o.flags.DryRun {
		utils.ShowDryRunSummary(o.flags.VpsIP, o.flags.Domain, o.flags.Branch, o.peers, o.flags.JoinAddress, o.validator.IsFirstNode(), o.oramaDir)
		return nil
	}

	// Save secrets before installation
	if err := o.validator.SaveSecrets(); err != nil {
		return err
	}

	// Save preferences for future upgrades (branch + nameserver)
	prefs := &production.NodePreferences{
		Branch:     o.flags.Branch,
		Nameserver: o.flags.Nameserver,
	}
	if err := production.SavePreferences(o.oramaDir, prefs); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: Failed to save preferences: %v\n", err)
	}
	if o.flags.Nameserver {
		fmt.Printf("  ℹ️  This node will be a nameserver (CoreDNS + Caddy)\n")
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

	// Phase 2b: Install binaries
	fmt.Printf("\nPhase 2b: Installing binaries...\n")
	if err := o.setup.Phase2bInstallBinaries(); err != nil {
		return fmt.Errorf("binary installation failed: %w", err)
	}

	// Phase 3: Generate secrets FIRST (before service initialization)
	fmt.Printf("\n🔐 Phase 3: Generating secrets...\n")
	if err := o.setup.Phase3GenerateSecrets(); err != nil {
		return fmt.Errorf("secret generation failed: %w", err)
	}

	// Phase 4: Generate configs (BEFORE service initialization)
	fmt.Printf("\n⚙️  Phase 4: Generating configurations...\n")
	// Internal gateway always runs HTTP on port 6001
	// When using Caddy (nameserver mode), Caddy handles external HTTPS and proxies to internal gateway
	// When not using Caddy, the gateway runs HTTP-only (use a reverse proxy for HTTPS)
	enableHTTPS := false
	if err := o.setup.Phase4GenerateConfigs(o.peers, o.flags.VpsIP, enableHTTPS, o.flags.Domain, o.flags.BaseDomain, o.flags.JoinAddress); err != nil {
		return fmt.Errorf("configuration generation failed: %w", err)
	}

	// Validate generated configuration
	if err := o.validator.ValidateGeneratedConfig(); err != nil {
		return err
	}

	// Phase 2c: Initialize services (after config is in place)
	fmt.Printf("\nPhase 2c: Initializing services...\n")
	ipfsPeerInfo := o.buildIPFSPeerInfo()
	ipfsClusterPeerInfo := o.buildIPFSClusterPeerInfo()

	if err := o.setup.Phase2cInitializeServices(o.peers, o.flags.VpsIP, ipfsPeerInfo, ipfsClusterPeerInfo); err != nil {
		return fmt.Errorf("service initialization failed: %w", err)
	}

	// Phase 5: Create systemd services
	fmt.Printf("\n🔧 Phase 5: Creating systemd services...\n")
	if err := o.setup.Phase5CreateSystemdServices(enableHTTPS); err != nil {
		return fmt.Errorf("service creation failed: %w", err)
	}

	// Log completion with actual peer ID
	o.setup.LogSetupComplete(o.setup.NodePeerID)
	fmt.Printf("✅ Production installation complete!\n\n")

	// For first node, print important secrets and identifiers
	if o.validator.IsFirstNode() {
		o.printFirstNodeSecrets()
	}

	return nil
}

func (o *Orchestrator) buildIPFSPeerInfo() *production.IPFSPeerInfo {
	if o.flags.IPFSPeerID != "" {
		var addrs []string
		if o.flags.IPFSAddrs != "" {
			addrs = strings.Split(o.flags.IPFSAddrs, ",")
		}
		return &production.IPFSPeerInfo{
			PeerID: o.flags.IPFSPeerID,
			Addrs:  addrs,
		}
	}
	return nil
}

func (o *Orchestrator) buildIPFSClusterPeerInfo() *production.IPFSClusterPeerInfo {
	if o.flags.IPFSClusterPeerID != "" {
		var addrs []string
		if o.flags.IPFSClusterAddrs != "" {
			addrs = strings.Split(o.flags.IPFSClusterAddrs, ",")
		}
		return &production.IPFSClusterPeerInfo{
			PeerID: o.flags.IPFSClusterPeerID,
			Addrs:  addrs,
		}
	}
	return nil
}

func (o *Orchestrator) printFirstNodeSecrets() {
	fmt.Printf("📋 Save these for joining future nodes:\n\n")

	// Print cluster secret
	clusterSecretPath := filepath.Join(o.oramaDir, "secrets", "cluster-secret")
	if clusterSecretData, err := os.ReadFile(clusterSecretPath); err == nil {
		fmt.Printf("  Cluster Secret (--cluster-secret):\n")
		fmt.Printf("    %s\n\n", string(clusterSecretData))
	}

	// Print swarm key
	swarmKeyPath := filepath.Join(o.oramaDir, "secrets", "swarm.key")
	if swarmKeyData, err := os.ReadFile(swarmKeyPath); err == nil {
		swarmKeyContent := strings.TrimSpace(string(swarmKeyData))
		lines := strings.Split(swarmKeyContent, "\n")
		if len(lines) >= 3 {
			// Extract just the hex part (last line)
			fmt.Printf("  IPFS Swarm Key (--swarm-key, last line only):\n")
			fmt.Printf("    %s\n\n", lines[len(lines)-1])
		}
	}

	// Print peer ID
	fmt.Printf("  Node Peer ID:\n")
	fmt.Printf("    %s\n\n", o.setup.NodePeerID)
}
