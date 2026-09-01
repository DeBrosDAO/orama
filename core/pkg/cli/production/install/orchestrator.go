package install

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
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
	oramaHome := production.OramaBase
	oramaDir := production.OramaDir

	// Normalize peers
	peers, err := utils.NormalizePeers(flags.PeersStr)
	if err != nil {
		return nil, fmt.Errorf("invalid peers: %w", err)
	}

	setup := production.NewProductionSetup(oramaHome, os.Stdout, flags.Force, flags.SkipChecks)
	setup.SetNameserver(flags.Nameserver)

	// Configure Anyone mode
	setup.SetAnyoneClient(true)

	// Set operator metadata (from orama node setup)
	setup.SSHUser = flags.SSHUser
	setup.Environment = flags.Environment
	setup.OperatorWallet = flags.OperatorWallet

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

	// Validate DNS if domain is provided
	o.validator.ValidateDNS()

	// Dry-run mode: show what would be done and exit
	if o.flags.DryRun {
		utils.ShowDryRunSummary(o.flags.VpsIP, o.flags.Domain, "main", o.peers, o.flags.JoinAddress, o.validator.IsFirstNode(), o.oramaDir)
		return nil
	}

	// Save secrets before installation (only for genesis; join flow gets secrets from response)
	if !o.isJoiningNode() {
		if err := o.validator.SaveSecrets(); err != nil {
			return err
		}
	}

	// Save preferences for future upgrades. Anyone is always client-only.
	prefs := &production.NodePreferences{
		Branch:       "main",
		Nameserver:   o.flags.Nameserver,
		AnyoneClient: true,
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

	// Install namespace systemd template units
	fmt.Printf("\n🔧 Phase 5b: Installing namespace systemd templates...\n")
	if err := o.installNamespaceTemplates(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Template installation warning: %v\n", err)
	}

	// Phase 7: Seed DNS records (with retry — migrations may still be running)
	if o.flags.Nameserver && o.flags.BaseDomain != "" {
		fmt.Printf("\n🌐 Phase 7: Seeding DNS records...\n")
		var seedErr error
		for attempt := 1; attempt <= 6; attempt++ {
			waitSec := 5 * attempt
			fmt.Printf("  Waiting for RQLite + migrations (%ds, attempt %d/6)...\n", waitSec, attempt)
			time.Sleep(time.Duration(waitSec) * time.Second)
			seedErr = o.setup.SeedDNSRecords(o.flags.BaseDomain, o.flags.VpsIP, o.peers)
			if seedErr == nil {
				fmt.Printf("  ✓ DNS records seeded\n")
				break
			}
			fmt.Fprintf(os.Stderr, "  ⚠️  Attempt %d failed: %v\n", attempt, seedErr)
		}
		if seedErr != nil {
			fmt.Fprintf(os.Stderr, "  ⚠️  Warning: DNS seeding failed after all attempts.\n")
			fmt.Fprintf(os.Stderr, "     Records will self-heal via node heartbeat once running.\n")
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
	if err := o.verifyWGTunnel(joinResp.WGPeers, o.flags.JoinAddress); err != nil {
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

	// Auto-generate domain for non-nameserver joining nodes
	if o.flags.Domain == "" && !o.flags.Nameserver && joinResp.BaseDomain != "" {
		o.flags.Domain = generateNodeDomain(joinResp.BaseDomain)
		fmt.Printf("\n🌐 Auto-generated domain: %s\n", o.flags.Domain)
	}

	// Step 7: Generate configs using WG IP as advertise address
	// All inter-node communication uses WireGuard IPs, not public IPs
	fmt.Printf("\n⚙️  Generating configurations...\n")
	enableHTTPS := false
	rqliteJoin := joinResp.RQLiteJoinAddress
	if err := o.setup.Phase4GenerateConfigs(joinResp.BootstrapPeers, joinResp.WGIP, enableHTTPS, o.flags.Domain, joinResp.BaseDomain, rqliteJoin, joinResp.OlricPeers); err != nil {
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

	// Install namespace systemd template units
	fmt.Printf("\n🔧 Installing namespace systemd templates...\n")
	if err := o.installNamespaceTemplates(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Template installation warning: %v\n", err)
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

	tlsConfig := &tls.Config{}
	if o.flags.CAFingerprint != "" {
		// TOFU: verify the server's TLS cert fingerprint matches the one from the invite
		expectedFP, err := hex.DecodeString(o.flags.CAFingerprint)
		if err != nil {
			return nil, fmt.Errorf("invalid --ca-fingerprint: must be hex-encoded SHA-256: %w", err)
		}
		tlsConfig.InsecureSkipVerify = true
		tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("server presented no TLS certificates")
			}
			hash := sha256.Sum256(rawCerts[0])
			if !bytes.Equal(hash[:], expectedFP) {
				return fmt.Errorf("TLS certificate fingerprint mismatch: expected %s, got %x (possible MITM attack)",
					o.flags.CAFingerprint, hash[:])
			}
			return nil
		}
	} else {
		// No fingerprint provided — fall back to insecure for backward compatibility
		tlsConfig.InsecureSkipVerify = true
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
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

	// Write API key HMAC secret
	if resp.APIKeyHMACSecret != "" {
		if err := os.WriteFile(filepath.Join(secretsDir, "api-key-hmac-secret"), []byte(resp.APIKeyHMACSecret), 0600); err != nil {
			return fmt.Errorf("failed to write api-key-hmac-secret: %w", err)
		}
	}

	// Write RQLite password and generate auth JSON file
	if resp.RQLitePassword != "" {
		if err := os.WriteFile(filepath.Join(secretsDir, "rqlite-password"), []byte(resp.RQLitePassword), 0600); err != nil {
			return fmt.Errorf("failed to write rqlite-password: %w", err)
		}
		// Also generate the auth JSON file that rqlited uses with -auth flag
		authJSON := fmt.Sprintf(`[{"username": "orama", "password": "%s", "perms": ["all"]}]`, resp.RQLitePassword)
		if err := os.WriteFile(filepath.Join(secretsDir, "rqlite-auth.json"), []byte(authJSON), 0600); err != nil {
			return fmt.Errorf("failed to write rqlite-auth.json: %w", err)
		}
	}

	// Write Olric encryption key
	if resp.OlricEncryptionKey != "" {
		if err := os.WriteFile(filepath.Join(secretsDir, "olric-encryption-key"), []byte(resp.OlricEncryptionKey), 0600); err != nil {
			return fmt.Errorf("failed to write olric-encryption-key: %w", err)
		}
	}

	// Write serverless secrets encryption key (bugboard #837) — identical on
	// every node so namespace function secrets decrypt cluster-wide.
	if resp.SecretsEncryptionKey != "" {
		if err := os.WriteFile(filepath.Join(secretsDir, "secrets-encryption-key"), []byte(resp.SecretsEncryptionKey), 0600); err != nil {
			return fmt.Errorf("failed to write secrets-encryption-key: %w", err)
		}
	}

	// Write TURN shared secret (feat-124 #913) — identical on every node so
	// WebRTC TURN credentials validate cluster-wide and survive config regen.
	if resp.TURNSecret != "" {
		if err := os.WriteFile(filepath.Join(secretsDir, "turn-secret"), []byte(resp.TURNSecret), 0600); err != nil {
			return fmt.Errorf("failed to write turn-secret: %w", err)
		}
	}

	// Write IPFS Cluster trusted peer IDs
	if len(resp.IPFSClusterPeerIDs) > 0 {
		content := strings.Join(resp.IPFSClusterPeerIDs, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(secretsDir, "ipfs-cluster-trusted-peers"), []byte(content), 0600); err != nil {
			return fmt.Errorf("failed to write ipfs-cluster-trusted-peers: %w", err)
		}
	}

	return nil
}

// verifyWGTunnel pings a WG peer to verify the tunnel is working.
// It targets the node that handled the join request (joinAddress), since that
// node is the only one guaranteed to have the new peer's key immediately.
// Other peers learn the key via the WireGuard sync loop (up to 60s delay),
// so pinging them would race against replication.
func (o *Orchestrator) verifyWGTunnel(peers []joinhandlers.WGPeerInfo, joinAddress string) error {
	if len(peers) == 0 {
		return fmt.Errorf("no WG peers to verify")
	}

	// Find the join node's WG IP by matching its public IP against peer endpoints.
	targetIP := ""
	joinHost := extractHost(joinAddress)
	for _, p := range peers {
		endpointHost := extractHost(p.Endpoint)
		if endpointHost == joinHost {
			targetIP = strings.TrimSuffix(p.AllowedIP, "/32")
			break
		}
	}

	// Fallback to first peer if the join node wasn't found in the peer list.
	if targetIP == "" {
		targetIP = strings.TrimSuffix(peers[0].AllowedIP, "/32")
	}

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

// extractHost returns the host part from a URL or host:port string.
func extractHost(addr string) string {
	// Strip scheme (http://, https://)
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	// Strip port
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		addr = addr[:idx]
	}
	// Strip trailing path
	if idx := strings.Index(addr, "/"); idx != -1 {
		addr = addr[:idx]
	}
	return addr
}

func (o *Orchestrator) printFirstNodeSecrets() {
	fmt.Printf("📋 To add more nodes to this cluster:\n\n")
	fmt.Printf("  1. Generate an invite token:\n")
	fmt.Printf("     orama invite\n\n")
	fmt.Printf("  2. Run the printed command on the new VPS.\n\n")
	fmt.Printf("  Node Peer ID: %s\n\n", o.setup.NodePeerID)
}

// promptForBaseDomain interactively prompts the user to select a network environment
// Returns the selected base domain for deployment routing
func promptForBaseDomain() string {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n🌐 Network Environment Selection")
	fmt.Println("=================================")
	fmt.Println("Select the network environment for this node:")
	fmt.Println()
	fmt.Println("  1. orama-devnet.network   (Development - for testing)")
	fmt.Println("  2. orama-testnet.network  (Testnet - pre-production)")
	fmt.Println("  3. orama-mainnet.network  (Mainnet - production)")
	fmt.Println("  4. Custom domain...")
	fmt.Println()
	fmt.Print("Select option [1-4] (default: 1): ")

	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	switch choice {
	case "", "1":
		fmt.Println("✓ Selected: orama-devnet.network")
		return "orama-devnet.network"
	case "2":
		fmt.Println("✓ Selected: orama-testnet.network")
		return "orama-testnet.network"
	case "3":
		fmt.Println("✓ Selected: orama-mainnet.network")
		return "orama-mainnet.network"
	case "4":
		fmt.Print("Enter custom base domain (e.g., example.com): ")
		customDomain, _ := reader.ReadString('\n')
		customDomain = strings.TrimSpace(customDomain)
		if customDomain == "" {
			fmt.Println("⚠️  No domain entered, using orama-devnet.network")
			return "orama-devnet.network"
		}
		// Remove any protocol prefix if user included it
		customDomain = strings.TrimPrefix(customDomain, "https://")
		customDomain = strings.TrimPrefix(customDomain, "http://")
		customDomain = strings.TrimSuffix(customDomain, "/")
		fmt.Printf("✓ Selected: %s\n", customDomain)
		return customDomain
	default:
		fmt.Println("⚠️  Invalid option, using orama-devnet.network")
		return "orama-devnet.network"
	}
}

// installNamespaceTemplates installs systemd template unit files for namespace services
func (o *Orchestrator) installNamespaceTemplates() error {
	// Check pre-built archive path first, fall back to source path
	sourceDir := production.OramaSystemdDir
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		sourceDir = filepath.Join(o.oramaHome, "src", "systemd")
	}
	systemdDir := "/etc/systemd/system"

	templates := []string{
		"orama-namespace-rqlite@.service",
		"orama-namespace-olric@.service",
		"orama-namespace-gateway@.service",
		"orama-namespace-sfu@.service",
		"orama-namespace-turn@.service",
		"orama-namespace-pubsub@.service",
	}

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

// generateNodeDomain creates a random subdomain like "node-a3f8k2.example.com"
func generateNodeDomain(baseDomain string) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based
		return fmt.Sprintf("node-%06x.%s", time.Now().UnixNano()%0xffffff, baseDomain)
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return fmt.Sprintf("node-%s.%s", string(b), baseDomain)
}
