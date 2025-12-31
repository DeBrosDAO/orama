package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
	"github.com/DeBrosOfficial/network/pkg/environments/production"
)

func handleProdInstall(args []string) {
	// Parse arguments using flag.FlagSet
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	vpsIP := fs.String("vps-ip", "", "Public IP of this VPS (required)")
	domain := fs.String("domain", "", "Domain name for HTTPS (optional, e.g. gateway.example.com)")
	branch := fs.String("branch", "main", "Git branch to use (main or nightly)")
	noPull := fs.Bool("no-pull", false, "Skip git clone/pull, use existing repository in /home/debros/src")
	force := fs.Bool("force", false, "Force reconfiguration even if already installed")
	dryRun := fs.Bool("dry-run", false, "Show what would be done without making changes")
	skipResourceChecks := fs.Bool("skip-checks", false, "Skip minimum resource checks (RAM/CPU)")

	// Cluster join flags
	joinAddress := fs.String("join", "", "Join an existing cluster (e.g. 1.2.3.4:7001)")
	clusterSecret := fs.String("cluster-secret", "", "Cluster secret for IPFS Cluster (required if joining)")
	swarmKey := fs.String("swarm-key", "", "IPFS Swarm key (required if joining)")
	peersStr := fs.String("peers", "", "Comma-separated list of bootstrap peer multiaddrs")

	// IPFS/Cluster specific info for Peering configuration
	ipfsPeerID := fs.String("ipfs-peer", "", "Peer ID of existing IPFS node to peer with")
	ipfsAddrs := fs.String("ipfs-addrs", "", "Comma-separated multiaddrs of existing IPFS node")
	ipfsClusterPeerID := fs.String("ipfs-cluster-peer", "", "Peer ID of existing IPFS Cluster node")
	ipfsClusterAddrs := fs.String("ipfs-cluster-addrs", "", "Comma-separated multiaddrs of existing IPFS Cluster node")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "❌ Failed to parse flags: %v\n", err)
		os.Exit(1)
	}

	// Validate required flags
	if *vpsIP == "" && !*dryRun {
		fmt.Fprintf(os.Stderr, "❌ Error: --vps-ip is required for installation\n")
		fmt.Fprintf(os.Stderr, "   Example: dbn prod install --vps-ip 1.2.3.4\n")
		os.Exit(1)
	}

	if os.Geteuid() != 0 && !*dryRun {
		fmt.Fprintf(os.Stderr, "❌ Production installation must be run as root (use sudo)\n")
		os.Exit(1)
	}

	oramaHome := "/home/debros"
	oramaDir := oramaHome + "/.orama"
	fmt.Printf("🚀 Starting production installation...\n\n")

	isFirstNode := *joinAddress == ""
	peers, err := utils.NormalizePeers(*peersStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Invalid peers: %v\n", err)
		os.Exit(1)
	}

	// If cluster secret was provided, save it to secrets directory before setup
	if *clusterSecret != "" {
		secretsDir := filepath.Join(oramaDir, "secrets")
		if err := os.MkdirAll(secretsDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to create secrets directory: %v\n", err)
			os.Exit(1)
		}
		secretPath := filepath.Join(secretsDir, "cluster-secret")
		if err := os.WriteFile(secretPath, []byte(*clusterSecret), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to save cluster secret: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  ✓ Cluster secret saved\n")
	}

	// If swarm key was provided, save it to secrets directory in full format
	if *swarmKey != "" {
		secretsDir := filepath.Join(oramaDir, "secrets")
		if err := os.MkdirAll(secretsDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to create secrets directory: %v\n", err)
			os.Exit(1)
		}
		// Convert 64-hex key to full swarm.key format
		swarmKeyContent := fmt.Sprintf("/key/swarm/psk/1.0.0/\n/base16/\n%s\n", strings.ToUpper(*swarmKey))
		swarmKeyPath := filepath.Join(secretsDir, "swarm.key")
		if err := os.WriteFile(swarmKeyPath, []byte(swarmKeyContent), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to save swarm key: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  ✓ Swarm key saved\n")
	}

	// Store IPFS peer info for peering
	var ipfsPeerInfo *utils.IPFSPeerInfo
	if *ipfsPeerID != "" {
		var addrs []string
		if *ipfsAddrs != "" {
			addrs = strings.Split(*ipfsAddrs, ",")
		}
		ipfsPeerInfo = &utils.IPFSPeerInfo{
			PeerID: *ipfsPeerID,
			Addrs:  addrs,
		}
	}

	// Store IPFS Cluster peer info for cluster peer discovery
	var ipfsClusterPeerInfo *utils.IPFSClusterPeerInfo
	if *ipfsClusterPeerID != "" {
		var addrs []string
		if *ipfsClusterAddrs != "" {
			addrs = strings.Split(*ipfsClusterAddrs, ",")
		}
		ipfsClusterPeerInfo = &utils.IPFSClusterPeerInfo{
			PeerID: *ipfsClusterPeerID,
			Addrs:  addrs,
		}
	}

	setup := production.NewProductionSetup(oramaHome, os.Stdout, *force, *branch, *noPull, *skipResourceChecks)

	// Inform user if skipping git pull
	if *noPull {
		fmt.Printf("  ⚠️  --no-pull flag enabled: Skipping git clone/pull\n")
		fmt.Printf("     Using existing repository at /home/debros/src\n")
	}

	// Check port availability before proceeding
	if err := utils.EnsurePortsAvailable("install", utils.DefaultPorts()); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Validate DNS if domain is provided
	if *domain != "" {
		fmt.Printf("\n🌐 Pre-flight DNS validation...\n")
		utils.ValidateDNSRecord(*domain, *vpsIP)
	}

	// Dry-run mode: show what would be done and exit
	if *dryRun {
		utils.ShowDryRunSummary(*vpsIP, *domain, *branch, peers, *joinAddress, isFirstNode, oramaDir)
		return
	}

	// Save branch preference for future upgrades
	if err := production.SaveBranchPreference(oramaDir, *branch); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: Failed to save branch preference: %v\n", err)
	}

	// Phase 1: Check prerequisites
	fmt.Printf("\n📋 Phase 1: Checking prerequisites...\n")
	if err := setup.Phase1CheckPrerequisites(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Prerequisites check failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 2: Provision environment
	fmt.Printf("\n🛠️  Phase 2: Provisioning environment...\n")
	if err := setup.Phase2ProvisionEnvironment(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Environment provisioning failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 2b: Install binaries
	fmt.Printf("\nPhase 2b: Installing binaries...\n")
	if err := setup.Phase2bInstallBinaries(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Binary installation failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 3: Generate secrets FIRST (before service initialization)
	// This ensures cluster secret and swarm key exist before repos are seeded
	fmt.Printf("\n🔐 Phase 3: Generating secrets...\n")
	if err := setup.Phase3GenerateSecrets(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Secret generation failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 4: Generate configs (BEFORE service initialization)
	// This ensures node.yaml exists before services try to access it
	fmt.Printf("\n⚙️  Phase 4: Generating configurations...\n")
	enableHTTPS := *domain != ""
	if err := setup.Phase4GenerateConfigs(peers, *vpsIP, enableHTTPS, *domain, *joinAddress); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Configuration generation failed: %v\n", err)
		os.Exit(1)
	}

	// Validate generated configuration
	fmt.Printf("  Validating generated configuration...\n")
	if err := utils.ValidateGeneratedConfig(oramaDir); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Configuration validation failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ Configuration validated\n")

	// Phase 2c: Initialize services (after config is in place)
	fmt.Printf("\nPhase 2c: Initializing services...\n")
	var prodIPFSPeer *production.IPFSPeerInfo
	if ipfsPeerInfo != nil {
		prodIPFSPeer = &production.IPFSPeerInfo{
			PeerID: ipfsPeerInfo.PeerID,
			Addrs:  ipfsPeerInfo.Addrs,
		}
	}
	var prodIPFSClusterPeer *production.IPFSClusterPeerInfo
	if ipfsClusterPeerInfo != nil {
		prodIPFSClusterPeer = &production.IPFSClusterPeerInfo{
			PeerID: ipfsClusterPeerInfo.PeerID,
			Addrs:  ipfsClusterPeerInfo.Addrs,
		}
	}
	if err := setup.Phase2cInitializeServices(peers, *vpsIP, prodIPFSPeer, prodIPFSClusterPeer); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service initialization failed: %v\n", err)
		os.Exit(1)
	}

	// Phase 5: Create systemd services
	fmt.Printf("\n🔧 Phase 5: Creating systemd services...\n")
	if err := setup.Phase5CreateSystemdServices(enableHTTPS); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Service creation failed: %v\n", err)
		os.Exit(1)
	}

	// Log completion with actual peer ID
	setup.LogSetupComplete(setup.NodePeerID)
	fmt.Printf("✅ Production installation complete!\n\n")

	// For first node, print important secrets and identifiers
	if isFirstNode {
		fmt.Printf("📋 Save these for joining future nodes:\n\n")

		// Print cluster secret
		clusterSecretPath := filepath.Join(oramaDir, "secrets", "cluster-secret")
		if clusterSecretData, err := os.ReadFile(clusterSecretPath); err == nil {
			fmt.Printf("  Cluster Secret (--cluster-secret):\n")
			fmt.Printf("    %s\n\n", string(clusterSecretData))
		}

		// Print swarm key
		swarmKeyPath := filepath.Join(oramaDir, "secrets", "swarm.key")
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
		fmt.Printf("    %s\n\n", setup.NodePeerID)
	}
}
