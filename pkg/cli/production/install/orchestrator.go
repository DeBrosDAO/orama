package install

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
	"github.com/DeBrosOfficial/network/pkg/environments/production"
	joinhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/join"
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

	// Prompt for base domain if not provided via flag
	if flags.BaseDomain == "" {
		flags.BaseDomain = promptForBaseDomain()
	}

	// Normalize peers
	peers, err := utils.NormalizePeers(flags.PeersStr)
	if err != nil {
		return nil, fmt.Errorf("invalid peers: %w", err)
	}

	setup := production.NewProductionSetup(oramaHome, os.Stdout, flags.Force, flags.Branch, flags.NoPull, flags.SkipChecks)
	setup.SetNameserver(flags.Nameserver)

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
		var relayInfo *utils.AnyoneRelayDryRunInfo
		if o.flags.AnyoneRelay {
			relayInfo = &utils.AnyoneRelayDryRunInfo{
				Enabled:  true,
				Exit:     o.flags.AnyoneExit,
				Nickname: o.flags.AnyoneNickname,
				Contact:  o.flags.AnyoneContact,
				Wallet:   o.flags.AnyoneWallet,
				ORPort:   o.flags.AnyoneORPort,
			}
		}
		utils.ShowDryRunSummaryWithRelay(o.flags.VpsIP, o.flags.Domain, o.flags.Branch, o.peers, o.flags.JoinAddress, o.validator.IsFirstNode(), o.oramaDir, relayInfo)
		return nil
	}

	// Save secrets before installation (only for genesis; join flow gets secrets from response)
	if !o.isJoiningNode() {
		if err := o.validator.SaveSecrets(); err != nil {
			return err
		}
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

	// Branch: genesis node vs joining node
	if o.isJoiningNode() {
		return o.executeJoinFlow()
	}
	return o.executeGenesisFlow()
}

// isJoiningNode returns true if --join and --token are both set
func (o *Orchestrator) isJoiningNode() bool {
	return o.flags.JoinAddress != "" && o.flags.Token != ""
}

// executeGenesisFlow runs the install for the first node in a new cluster
func (o *Orchestrator) executeGenesisFlow() error {
	// Phase 3: Generate secrets locally
	fmt.Printf("\n🔐 Phase 3: Generating secrets...\n")
	if err := o.setup.Phase3GenerateSecrets(); err != nil {
		return fmt.Errorf("secret generation failed: %w", err)
	}

	// Phase 6a: WireGuard — self-assign 10.0.0.1
	fmt.Printf("\n🔒 Phase 6a: Setting up WireGuard mesh VPN...\n")
	if _, _, err := o.setup.Phase6SetupWireGuard(true); err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠️  Warning: WireGuard setup failed: %v\n", err)
	} else {
		fmt.Printf("  ✓ WireGuard configured (10.0.0.1)\n")
	}

	// Phase 6b: UFW firewall
	fmt.Printf("\n🛡️  Phase 6b: Setting up UFW firewall...\n")
	if err := o.setup.Phase6bSetupFirewall(o.flags.SkipFirewall); err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠️  Warning: Firewall setup failed: %v\n", err)
	}

	// Phase 4: Generate configs using WG IP (10.0.0.1) as advertise address
	// All inter-node communication uses WireGuard IPs, not public IPs
	fmt.Printf("\n⚙️  Phase 4: Generating configurations...\n")
	enableHTTPS := false
	genesisWGIP := "10.0.0.1"
	if err := o.setup.Phase4GenerateConfigs(o.peers, genesisWGIP, enableHTTPS, o.flags.Domain, o.flags.BaseDomain, ""); err != nil {
		return fmt.Errorf("configuration generation failed: %w", err)
	}

	if err := o.validator.ValidateGeneratedConfig(); err != nil {
		return err
	}

	// Phase 2c: Initialize services (use WG IP for IPFS Cluster peer discovery)
	fmt.Printf("\nPhase 2c: Initializing services...\n")
	if err := o.setup.Phase2cInitializeServices(o.peers, genesisWGIP, nil, nil); err != nil {
		return fmt.Errorf("service initialization failed: %w", err)
	}

	// Phase 5: Create systemd services
	fmt.Printf("\n🔧 Phase 5: Creating systemd services...\n")
	if err := o.setup.Phase5CreateSystemdServices(enableHTTPS); err != nil {
		return fmt.Errorf("service creation failed: %w", err)
	}

	// Phase 7: Seed DNS records
	if o.flags.Nameserver && o.flags.BaseDomain != "" {
		fmt.Printf("\n🌐 Phase 7: Seeding DNS records...\n")
		fmt.Printf("  Waiting for RQLite to start (10s)...\n")
		time.Sleep(10 * time.Second)
		if err := o.setup.SeedDNSRecords(o.flags.BaseDomain, o.flags.VpsIP, o.peers); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠️  Warning: Failed to seed DNS records: %v\n", err)
		} else {
			fmt.Printf("  ✓ DNS records seeded\n")
		}
	}

	o.setup.LogSetupComplete(o.setup.NodePeerID)
	fmt.Printf("✅ Production installation complete!\n\n")
	o.printFirstNodeSecrets()
	return nil
}

// executeJoinFlow runs the install for a node joining an existing cluster via invite token
func (o *Orchestrator) executeJoinFlow() error {
	// Step 1: Generate WG keypair
	fmt.Printf("\n🔑 Generating WireGuard keypair...\n")
	privKey, pubKey, err := production.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("failed to generate WG keypair: %w", err)
	}
	fmt.Printf("  ✓ WireGuard keypair generated\n")

	// Step 2: Call join endpoint on existing node
	fmt.Printf("\n🤝 Requesting cluster join from %s...\n", o.flags.JoinAddress)
	joinResp, err := o.callJoinEndpoint(pubKey)
	if err != nil {
		return fmt.Errorf("join request failed: %w", err)
	}
	fmt.Printf("  ✓ Join approved — assigned WG IP: %s\n", joinResp.WGIP)
	fmt.Printf("  ✓ Received %d WG peers\n", len(joinResp.WGPeers))

	// Step 3: Configure WireGuard with assigned IP and peers
	fmt.Printf("\n🔒 Configuring WireGuard tunnel...\n")
	var wgPeers []production.WireGuardPeer
	for _, p := range joinResp.WGPeers {
		wgPeers = append(wgPeers, production.WireGuardPeer{
			PublicKey: p.PublicKey,
			Endpoint:  p.Endpoint,
			AllowedIP: p.AllowedIP,
		})
	}
	// Install WG package first
	wp := production.NewWireGuardProvisioner(production.WireGuardConfig{})
	if err := wp.Install(); err != nil {
		return fmt.Errorf("failed to install wireguard: %w", err)
	}
	if err := o.setup.EnableWireGuardWithPeers(privKey, joinResp.WGIP, wgPeers); err != nil {
		return fmt.Errorf("failed to enable WireGuard: %w", err)
	}

	// Step 4: Verify WG tunnel
	fmt.Printf("\n🔍 Verifying WireGuard tunnel...\n")
	if err := o.verifyWGTunnel(joinResp.WGPeers); err != nil {
		return fmt.Errorf("WireGuard tunnel verification failed: %w", err)
	}
	fmt.Printf("  ✓ WireGuard tunnel established\n")

	// Step 5: UFW firewall
	fmt.Printf("\n🛡️  Setting up UFW firewall...\n")
	if err := o.setup.Phase6bSetupFirewall(o.flags.SkipFirewall); err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠️  Warning: Firewall setup failed: %v\n", err)
	}

	// Step 6: Save secrets from join response
	fmt.Printf("\n🔐 Saving cluster secrets...\n")
	if err := o.saveSecretsFromJoinResponse(joinResp); err != nil {
		return fmt.Errorf("failed to save secrets: %w", err)
	}
	fmt.Printf("  ✓ Secrets saved\n")

	// Step 7: Generate configs using WG IP as advertise address
	// All inter-node communication uses WireGuard IPs, not public IPs
	fmt.Printf("\n⚙️  Generating configurations...\n")
	enableHTTPS := false
	rqliteJoin := joinResp.RQLiteJoinAddress
	if err := o.setup.Phase4GenerateConfigs(joinResp.BootstrapPeers, joinResp.WGIP, enableHTTPS, o.flags.Domain, joinResp.BaseDomain, rqliteJoin); err != nil {
		return fmt.Errorf("configuration generation failed: %w", err)
	}

	if err := o.validator.ValidateGeneratedConfig(); err != nil {
		return err
	}

	// Step 8: Initialize services with IPFS peer info from join response
	fmt.Printf("\nInitializing services...\n")
	var ipfsPeerInfo *production.IPFSPeerInfo
	if joinResp.IPFSPeer.ID != "" {
		ipfsPeerInfo = &production.IPFSPeerInfo{
			PeerID: joinResp.IPFSPeer.ID,
			Addrs:  joinResp.IPFSPeer.Addrs,
		}
	}
	var ipfsClusterPeerInfo *production.IPFSClusterPeerInfo
	if joinResp.IPFSClusterPeer.ID != "" {
		ipfsClusterPeerInfo = &production.IPFSClusterPeerInfo{
			PeerID: joinResp.IPFSClusterPeer.ID,
			Addrs:  joinResp.IPFSClusterPeer.Addrs,
		}
	}

	if err := o.setup.Phase2cInitializeServices(joinResp.BootstrapPeers, joinResp.WGIP, ipfsPeerInfo, ipfsClusterPeerInfo); err != nil {
		return fmt.Errorf("service initialization failed: %w", err)
	}

	// Step 9: Create systemd services
	fmt.Printf("\n🔧 Creating systemd services...\n")
	if err := o.setup.Phase5CreateSystemdServices(enableHTTPS); err != nil {
		return fmt.Errorf("service creation failed: %w", err)
	}

	o.setup.LogSetupComplete(o.setup.NodePeerID)
	fmt.Printf("✅ Production installation complete! Joined cluster via %s\n\n", o.flags.JoinAddress)
	return nil
}

// callJoinEndpoint sends the join request to the existing node's HTTPS endpoint
func (o *Orchestrator) callJoinEndpoint(wgPubKey string) (*joinhandlers.JoinResponse, error) {
	reqBody := joinhandlers.JoinRequest{
		Token:       o.flags.Token,
		WGPublicKey: wgPubKey,
		PublicIP:    o.flags.VpsIP,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := strings.TrimRight(o.flags.JoinAddress, "/") + "/v1/internal/join"
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // Self-signed certs during initial setup
			},
		},
	}

	resp, err := client.Post(url, "application/json", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("failed to contact %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("join rejected (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var joinResp joinhandlers.JoinResponse
	if err := json.Unmarshal(respBody, &joinResp); err != nil {
		return nil, fmt.Errorf("failed to parse join response: %w", err)
	}

	return &joinResp, nil
}

// saveSecretsFromJoinResponse writes cluster secrets received from the join endpoint to disk
func (o *Orchestrator) saveSecretsFromJoinResponse(resp *joinhandlers.JoinResponse) error {
	secretsDir := filepath.Join(o.oramaDir, "secrets")
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		return fmt.Errorf("failed to create secrets dir: %w", err)
	}

	// Write cluster secret
	if resp.ClusterSecret != "" {
		if err := os.WriteFile(filepath.Join(secretsDir, "cluster-secret"), []byte(resp.ClusterSecret), 0600); err != nil {
			return fmt.Errorf("failed to write cluster-secret: %w", err)
		}
	}

	// Write swarm key
	if resp.SwarmKey != "" {
		if err := os.WriteFile(filepath.Join(secretsDir, "swarm.key"), []byte(resp.SwarmKey), 0600); err != nil {
			return fmt.Errorf("failed to write swarm.key: %w", err)
		}
	}

	return nil
}

// verifyWGTunnel pings a WG peer to verify the tunnel is working
func (o *Orchestrator) verifyWGTunnel(peers []joinhandlers.WGPeerInfo) error {
	if len(peers) == 0 {
		return fmt.Errorf("no WG peers to verify")
	}

	// Extract the IP from the first peer's AllowedIP (e.g. "10.0.0.1/32" -> "10.0.0.1")
	targetIP := strings.TrimSuffix(peers[0].AllowedIP, "/32")

	// Retry ping for up to 30 seconds
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cmd := exec.Command("ping", "-c", "1", "-W", "2", targetIP)
		if err := cmd.Run(); err == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("could not reach %s via WireGuard after 30s", targetIP)
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

// promptForBaseDomain interactively prompts the user to select a network environment
// Returns the selected base domain for deployment routing
func promptForBaseDomain() string {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n🌐 Network Environment Selection")
	fmt.Println("=================================")
	fmt.Println("Select the network environment for this node:")
	fmt.Println()
	fmt.Println("  1. devnet-orama.network   (Development - for testing)")
	fmt.Println("  2. testnet-orama.network  (Testnet - pre-production)")
	fmt.Println("  3. mainnet-orama.network  (Mainnet - production)")
	fmt.Println("  4. Custom domain...")
	fmt.Println()
	fmt.Print("Select option [1-4] (default: 1): ")

	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	switch choice {
	case "", "1":
		fmt.Println("✓ Selected: devnet-orama.network")
		return "devnet-orama.network"
	case "2":
		fmt.Println("✓ Selected: testnet-orama.network")
		return "testnet-orama.network"
	case "3":
		fmt.Println("✓ Selected: mainnet-orama.network")
		return "mainnet-orama.network"
	case "4":
		fmt.Print("Enter custom base domain (e.g., example.com): ")
		customDomain, _ := reader.ReadString('\n')
		customDomain = strings.TrimSpace(customDomain)
		if customDomain == "" {
			fmt.Println("⚠️  No domain entered, using devnet-orama.network")
			return "devnet-orama.network"
		}
		// Remove any protocol prefix if user included it
		customDomain = strings.TrimPrefix(customDomain, "https://")
		customDomain = strings.TrimPrefix(customDomain, "http://")
		customDomain = strings.TrimSuffix(customDomain, "/")
		fmt.Printf("✓ Selected: %s\n", customDomain)
		return customDomain
	default:
		fmt.Println("⚠️  Invalid option, using devnet-orama.network")
		return "devnet-orama.network"
	}
}
