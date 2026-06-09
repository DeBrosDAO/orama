package production

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/environments/production/installers"
)

// AnyoneRelayConfig holds configuration for Anyone relay mode
type AnyoneRelayConfig struct {
	Enabled       bool   // Whether to run as relay operator
	Exit          bool   // Whether to run as exit relay
	Migrate       bool   // Whether to migrate existing installation
	Nickname      string // Relay nickname (1-19 alphanumeric)
	Contact       string // Contact info (email or @telegram)
	Wallet        string // Ethereum wallet for rewards
	ORPort        int    // ORPort for relay (default 9001)
	MyFamily      string // Comma-separated fingerprints of other relays (for multi-relay operators)
	BandwidthPct  int    // Percentage of VPS bandwidth to allocate to relay (0 = unlimited)
	AccountingMax int    // Monthly data cap in GB (0 = unlimited)
}

// ProductionSetup orchestrates the entire production deployment
type ProductionSetup struct {
	osInfo             *OSInfo
	arch               string
	oramaHome          string
	oramaDir           string
	logWriter          io.Writer
	forceReconfigure   bool
	skipOptionalDeps   bool
	skipResourceChecks bool
	isNameserver       bool               // Whether this node is a nameserver (runs CoreDNS + Caddy)
	isAnyoneClient     bool               // Whether this node runs Anyone as client-only (SOCKS5 proxy)
	anyoneRelayConfig  *AnyoneRelayConfig // Configuration for Anyone relay mode
	privChecker        *PrivilegeChecker
	osDetector         *OSDetector
	archDetector       *ArchitectureDetector
	resourceChecker    *ResourceChecker
	portChecker        *PortChecker
	fsProvisioner      *FilesystemProvisioner
	stateDetector      *StateDetector
	configGenerator    *ConfigGenerator
	secretGenerator    *SecretGenerator
	serviceGenerator   *SystemdServiceGenerator
	serviceController  *SystemdController
	binaryInstaller    *BinaryInstaller
	NodePeerID         string // Captured during Phase3 for later display

	// Operator metadata (from --ssh-user, --environment, --operator-wallet flags)
	SSHUser        string
	Environment    string
	OperatorWallet string
}

// ReadBranchPreference reads the stored branch preference from disk
func ReadBranchPreference(oramaDir string) string {
	branchFile := filepath.Join(oramaDir, ".branch")
	data, err := os.ReadFile(branchFile)
	if err != nil {
		return "main" // Default to main if file doesn't exist
	}
	branch := strings.TrimSpace(string(data))
	if branch == "" {
		return "main"
	}
	return branch
}

// SaveBranchPreference saves the branch preference to disk
func SaveBranchPreference(oramaDir, branch string) error {
	branchFile := filepath.Join(oramaDir, ".branch")
	if err := os.MkdirAll(oramaDir, 0755); err != nil {
		return fmt.Errorf("failed to create orama directory: %w", err)
	}
	if err := os.WriteFile(branchFile, []byte(branch), 0644); err != nil {
		return fmt.Errorf("failed to save branch preference: %w", err)
	}
	return nil
}

// NewProductionSetup creates a new production setup orchestrator
func NewProductionSetup(oramaHome string, logWriter io.Writer, forceReconfigure bool, skipResourceChecks bool) *ProductionSetup {
	oramaDir := filepath.Join(oramaHome, ".orama")
	arch, _ := (&ArchitectureDetector{}).Detect()

	return &ProductionSetup{
		oramaHome:          oramaHome,
		oramaDir:           oramaDir,
		logWriter:          logWriter,
		forceReconfigure:   forceReconfigure,
		arch:               arch,
		skipResourceChecks: skipResourceChecks,
		privChecker:        &PrivilegeChecker{},
		osDetector:         &OSDetector{},
		archDetector:       &ArchitectureDetector{},
		resourceChecker:    NewResourceChecker(),
		portChecker:        NewPortChecker(),
		fsProvisioner:      NewFilesystemProvisioner(oramaHome),
		stateDetector:      NewStateDetector(oramaDir),
		configGenerator:    NewConfigGenerator(oramaDir),
		secretGenerator:    NewSecretGenerator(oramaDir),
		serviceGenerator:   NewSystemdServiceGenerator(oramaHome, oramaDir),
		serviceController:  NewSystemdController(),
		binaryInstaller:    NewBinaryInstaller(arch, logWriter),
	}
}

// logf writes a formatted message to the log writer
func (ps *ProductionSetup) logf(format string, args ...interface{}) {
	if ps.logWriter != nil {
		fmt.Fprintf(ps.logWriter, format+"\n", args...)
	}
}

// IsUpdate detects if this is an update to an existing installation
func (ps *ProductionSetup) IsUpdate() bool {
	return ps.stateDetector.IsConfigured() || ps.stateDetector.HasIPFSData()
}

// SetNameserver sets whether this node is a nameserver (runs CoreDNS + Caddy)
func (ps *ProductionSetup) SetNameserver(isNameserver bool) {
	ps.isNameserver = isNameserver
}

// IsNameserver returns whether this node is configured as a nameserver
func (ps *ProductionSetup) IsNameserver() bool {
	return ps.isNameserver
}

// SetAnyoneRelayConfig sets the Anyone relay configuration
func (ps *ProductionSetup) SetAnyoneRelayConfig(config *AnyoneRelayConfig) {
	ps.anyoneRelayConfig = config
}

// IsAnyoneRelay returns whether this node is configured as an Anyone relay operator
func (ps *ProductionSetup) IsAnyoneRelay() bool {
	return ps.anyoneRelayConfig != nil && ps.anyoneRelayConfig.Enabled
}

// SetAnyoneClient sets whether this node runs Anyone as client-only
func (ps *ProductionSetup) SetAnyoneClient(enabled bool) {
	ps.isAnyoneClient = enabled
}

// IsAnyoneClient returns whether this node runs Anyone as client-only
func (ps *ProductionSetup) IsAnyoneClient() bool {
	return ps.isAnyoneClient
}

// disableConflictingAnyoneService stops, disables, and removes a conflicting
// Anyone service file. A node must run either relay or client, never both.
// This is best-effort: errors are logged but do not abort the operation.
func (ps *ProductionSetup) disableConflictingAnyoneService(serviceName string) {
	unitPath := filepath.Join("/etc/systemd/system", serviceName)
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		return // Nothing to clean up
	}

	ps.logf("  Removing conflicting Anyone service: %s", serviceName)

	if err := ps.serviceController.StopService(serviceName); err != nil {
		ps.logf("  ⚠️  Warning: failed to stop %s: %v", serviceName, err)
	}
	if err := ps.serviceController.DisableService(serviceName); err != nil {
		ps.logf("  ⚠️  Warning: failed to disable %s: %v", serviceName, err)
	}
	if err := ps.serviceController.RemoveServiceUnit(serviceName); err != nil {
		ps.logf("  ⚠️  Warning: failed to remove %s: %v", serviceName, err)
	}
}

// Phase1CheckPrerequisites performs initial environment validation
func (ps *ProductionSetup) Phase1CheckPrerequisites() error {
	ps.logf("Phase 1: Checking prerequisites...")

	// Check root
	if err := ps.privChecker.CheckRoot(); err != nil {
		return fmt.Errorf("privilege check failed: %w", err)
	}
	ps.logf("  ✓ Running as root")

	// Check Linux OS
	if err := ps.privChecker.CheckLinuxOS(); err != nil {
		return fmt.Errorf("OS check failed: %w", err)
	}
	ps.logf("  ✓ Running on Linux")

	// Detect OS
	osInfo, err := ps.osDetector.Detect()
	if err != nil {
		return fmt.Errorf("failed to detect OS: %w", err)
	}
	ps.osInfo = osInfo
	ps.logf("  ✓ Detected OS: %s", osInfo.Name)

	// Check if supported
	if !ps.osDetector.IsSupportedOS(osInfo) {
		ps.logf("  ⚠️  OS %s is not officially supported (Ubuntu 22/24/25, Debian 12)", osInfo.Name)
		ps.logf("     Proceeding anyway, but issues may occur")
	}

	// Detect architecture
	arch, err := ps.archDetector.Detect()
	if err != nil {
		return fmt.Errorf("failed to detect architecture: %w", err)
	}
	ps.arch = arch
	ps.logf("  ✓ Detected architecture: %s", arch)

	// Check basic dependencies (auto-installs missing ones)
	depChecker := NewDependencyChecker(ps.skipOptionalDeps)
	if missing, err := depChecker.CheckAll(); err != nil {
		ps.logf("  ❌ Failed to install dependencies:")
		for _, dep := range missing {
			ps.logf("     - %s", dep.Name)
		}
		return err
	}
	ps.logf("  ✓ Basic dependencies available")

	// Check system resources
	if ps.skipResourceChecks {
		ps.logf("  ⚠️  Skipping system resource checks (disk, RAM, CPU) due to --ignore-resource-checks flag")
	} else {
		if err := ps.resourceChecker.CheckDiskSpace(ps.oramaHome); err != nil {
			ps.logf("  ❌ %v", err)
			return err
		}
		ps.logf("  ✓ Sufficient disk space available")

		if err := ps.resourceChecker.CheckRAM(); err != nil {
			ps.logf("  ❌ %v", err)
			return err
		}
		ps.logf("  ✓ Sufficient RAM available")

		if err := ps.resourceChecker.CheckCPU(); err != nil {
			ps.logf("  ❌ %v", err)
			return err
		}
		ps.logf("  ✓ Sufficient CPU cores available")
	}

	return nil
}

// Phase2ProvisionEnvironment sets up filesystems
func (ps *ProductionSetup) Phase2ProvisionEnvironment() error {
	ps.logf("Phase 2: Provisioning environment...")

	// Create directory structure (unified structure)
	if err := ps.fsProvisioner.EnsureDirectoryStructure(); err != nil {
		return fmt.Errorf("failed to create directory structure: %w", err)
	}
	ps.logf("  ✓ Directory structure created")

	// Create dedicated orama user for running services (non-root)
	if err := ps.fsProvisioner.EnsureOramaUser(); err != nil {
		ps.logf("  ⚠️  Could not create orama user: %v (services will run as root)", err)
	} else {
		ps.logf("  ✓ orama user ensured")
	}

	return nil
}

// Phase2bInstallBinaries installs external binaries and Orama components.
// Auto-detects pre-built mode if /opt/orama/manifest.json exists.
func (ps *ProductionSetup) Phase2bInstallBinaries() error {
	ps.logf("Phase 2b: Installing binaries...")

	// Auto-detect pre-built binary archive
	if HasPreBuiltArchive() {
		manifest, err := LoadPreBuiltManifest()
		if err != nil {
			ps.logf("  ⚠️  Pre-built manifest found but unreadable: %v", err)
			ps.logf("  Falling back to source mode...")
			if err := ps.installFromSource(); err != nil {
				return err
			}
		} else {
			if err := ps.installFromPreBuilt(manifest); err != nil {
				return err
			}
		}
	} else {
		// Source mode: compile everything on the VPS (original behavior)
		if err := ps.installFromSource(); err != nil {
			return err
		}
	}

	// Anyone relay/client configuration runs after BOTH paths.
	// Pre-built mode installs the anon binary via .deb/apt;
	// source mode installs it via the relay installer's Install().
	// Configuration (anonrc, bandwidth, migration) is always needed.
	if err := ps.configureAnyone(); err != nil {
		ps.logf("  ⚠️  Anyone configuration warning: %v", err)
	}

	ps.logf("  ✓ All binaries installed")
	return nil
}

// installFromSource installs binaries by compiling from source on the VPS.
// This is the original Phase2bInstallBinaries logic, preserved as fallback.
func (ps *ProductionSetup) installFromSource() error {
	// Install system dependencies (always needed for runtime libs)
	if err := ps.binaryInstaller.InstallSystemDependencies(); err != nil {
		ps.logf("  ⚠️  System dependencies warning: %v", err)
	}

	// Install Go toolchain (downloads from go.dev if needed)
	if err := ps.binaryInstaller.InstallGo(); err != nil {
		return fmt.Errorf("failed to install Go: %w", err)
	}

	if err := ps.binaryInstaller.InstallOlric(); err != nil {
		ps.logf("  ⚠️  Olric install warning: %v", err)
	}

	// Install Orama binaries (source must be at /opt/orama/src via SCP)
	if err := ps.binaryInstaller.InstallDeBrosBinaries(ps.oramaHome); err != nil {
		return fmt.Errorf("failed to install Orama binaries: %w", err)
	}

	// Install CoreDNS only for nameserver nodes
	if ps.isNameserver {
		if err := ps.binaryInstaller.InstallCoreDNS(); err != nil {
			ps.logf("  ⚠️  CoreDNS install warning: %v", err)
		}
	}

	// Install Caddy on ALL nodes (any node may host namespaces and need TLS)
	if err := ps.binaryInstaller.InstallCaddy(); err != nil {
		ps.logf("  ⚠️  Caddy install warning: %v", err)
	}

	// Install ntfy on every node (feature #72). ntfy listens on
	// 127.0.0.1:NtfyListenPort and is only reachable via the local
	// Caddy reverse-proxy block, so it's safe to run cluster-wide:
	// nodes that don't host a public push.* DNS entry simply have
	// an idle ntfy with no inbound traffic. Uniform install means no
	// per-node toggling and no surprises when DNS topology changes.
	if err := ps.binaryInstaller.InstallNtfy(); err != nil {
		ps.logf("  ⚠️  ntfy install warning: %v", err)
	}

	// These are pre-built binary downloads (not Go compilation), always run them
	if err := ps.binaryInstaller.InstallRQLite(); err != nil {
		ps.logf("  ⚠️  RQLite install warning: %v", err)
	}

	if err := ps.binaryInstaller.InstallIPFS(); err != nil {
		ps.logf("  ⚠️  IPFS install warning: %v", err)
	}

	if err := ps.binaryInstaller.InstallIPFSCluster(); err != nil {
		ps.logf("  ⚠️  IPFS Cluster install warning: %v", err)
	}

	return nil
}

// configureAnyone handles Anyone relay/client installation and configuration.
// This runs after both pre-built and source mode binary installation.
func (ps *ProductionSetup) configureAnyone() error {
	if ps.IsAnyoneRelay() {
		ps.logf("  Installing Anyone relay (operator mode)...")
		relayConfig := installers.AnyoneRelayConfig{
			Nickname:      ps.anyoneRelayConfig.Nickname,
			Contact:       ps.anyoneRelayConfig.Contact,
			Wallet:        ps.anyoneRelayConfig.Wallet,
			ORPort:        ps.anyoneRelayConfig.ORPort,
			ExitRelay:     ps.anyoneRelayConfig.Exit,
			Migrate:       ps.anyoneRelayConfig.Migrate,
			MyFamily:      ps.anyoneRelayConfig.MyFamily,
			AccountingMax: ps.anyoneRelayConfig.AccountingMax,
		}

		// Run bandwidth test and calculate limits if percentage is set
		if ps.anyoneRelayConfig.BandwidthPct > 0 {
			measuredKBs, err := installers.MeasureBandwidth(ps.logWriter)
			if err != nil {
				ps.logf("  ⚠️  Bandwidth test failed, relay will run without bandwidth limits: %v", err)
			} else if measuredKBs > 0 {
				rate, burst := installers.CalculateBandwidthLimits(measuredKBs, ps.anyoneRelayConfig.BandwidthPct)
				relayConfig.BandwidthRate = rate
				relayConfig.BandwidthBurst = burst
				rateMbps := float64(rate) * 8 / 1024
				ps.logf("  ✓ Relay bandwidth limited to %d%% of measured speed (%d KBytes/s = %.1f Mbps)",
					ps.anyoneRelayConfig.BandwidthPct, rate, rateMbps)
			}
		}

		relayInstaller := installers.NewAnyoneRelayInstaller(ps.arch, ps.logWriter, relayConfig)

		// Check for existing installation if migration is requested
		if relayConfig.Migrate {
			existing, err := installers.DetectExistingAnyoneInstallation()
			if err != nil {
				ps.logf("  ⚠️  Failed to detect existing installation: %v", err)
			} else if existing != nil {
				backupDir := filepath.Join(ps.oramaDir, "backups")
				if err := relayInstaller.MigrateExistingInstallation(existing, backupDir); err != nil {
					ps.logf("  ⚠️  Migration warning: %v", err)
				}
			}
		}

		// Install the relay (apt-based, not Go — idempotent if already installed via .deb)
		if err := relayInstaller.Install(); err != nil {
			ps.logf("  ⚠️  Anyone relay install warning: %v", err)
		}

		// Configure the relay
		if err := relayInstaller.Configure(); err != nil {
			ps.logf("  ⚠️  Anyone relay config warning: %v", err)
		}
	} else if ps.IsAnyoneClient() {
		ps.logf("  Installing Anyone client-only mode (SOCKS5 proxy)...")
		clientInstaller := installers.NewAnyoneRelayInstaller(ps.arch, ps.logWriter, installers.AnyoneRelayConfig{})

		// Install the anon binary (same apt package as relay — idempotent)
		if err := clientInstaller.Install(); err != nil {
			ps.logf("  ⚠️  Anyone client install warning: %v", err)
		}

		// Configure as client-only (SocksPort 9050, no ORPort)
		if err := clientInstaller.ConfigureClient(); err != nil {
			ps.logf("  ⚠️  Anyone client config warning: %v", err)
		}
	}

	return nil
}

// Phase2cInitializeServices initializes service repositories and configurations
// ipfsPeer can be nil for the first node, or contain peer info for joining nodes
// ipfsClusterPeer can be nil for the first node, or contain IPFS Cluster peer info for joining nodes
func (ps *ProductionSetup) Phase2cInitializeServices(peerAddresses []string, vpsIP string, ipfsPeer *IPFSPeerInfo, ipfsClusterPeer *IPFSClusterPeerInfo) error {
	ps.logf("Phase 2c: Initializing services...")

	// Ensure directories exist (unified structure)
	if err := ps.fsProvisioner.EnsureDirectoryStructure(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Build paths - unified data directory (all nodes equal)
	dataDir := filepath.Join(ps.oramaDir, "data")

	// Initialize IPFS repo with correct path structure
	// Use port 4501 for API (to avoid conflict with RQLite on 5001), 8080 for gateway (standard), 4101 for swarm (to avoid conflict with LibP2P on 4001)
	ipfsRepoPath := filepath.Join(dataDir, "ipfs", "repo")
	if err := ps.binaryInstaller.InitializeIPFSRepo(ipfsRepoPath, filepath.Join(ps.oramaDir, "secrets", "swarm.key"), 4501, 8080, 4101, vpsIP, ipfsPeer); err != nil {
		return fmt.Errorf("failed to initialize IPFS repo: %w", err)
	}

	// Initialize IPFS Cluster config (runs ipfs-cluster-service init)
	clusterPath := filepath.Join(dataDir, "ipfs-cluster")
	clusterSecret, err := ps.secretGenerator.EnsureClusterSecret()
	if err != nil {
		return fmt.Errorf("failed to get cluster secret: %w", err)
	}

	// Get cluster peer addresses from IPFS Cluster peer info if available
	var clusterPeers []string
	if ipfsClusterPeer != nil && ipfsClusterPeer.PeerID != "" {
		// Construct cluster peer multiaddress using the discovered peer ID
		// Format: /ip4/<ip>/tcp/9100/p2p/<cluster-peer-id>
		peerIP := inferPeerIP(peerAddresses, vpsIP)
		if peerIP != "" {
			// Construct the bootstrap multiaddress for IPFS Cluster
			// Note: IPFS Cluster listens on port 9100 for cluster communication
			clusterBootstrapAddr := fmt.Sprintf("/ip4/%s/tcp/9100/p2p/%s", peerIP, ipfsClusterPeer.PeerID)
			clusterPeers = []string{clusterBootstrapAddr}
			ps.logf("  ℹ️  IPFS Cluster will connect to peer: %s", clusterBootstrapAddr)
		} else if len(ipfsClusterPeer.Addrs) > 0 {
			// Fallback: use the addresses from discovery (if they include peer ID)
			for _, addr := range ipfsClusterPeer.Addrs {
				if strings.Contains(addr, ipfsClusterPeer.PeerID) {
					clusterPeers = append(clusterPeers, addr)
				}
			}
			if len(clusterPeers) > 0 {
				ps.logf("  ℹ️  IPFS Cluster will connect to discovered peers: %v", clusterPeers)
			}
		}
	}

	if err := ps.binaryInstaller.InitializeIPFSClusterConfig(clusterPath, clusterSecret, 4501, clusterPeers); err != nil {
		return fmt.Errorf("failed to initialize IPFS Cluster: %w", err)
	}

	// After init, save own IPFS Cluster peer ID to trusted peers file
	if err := ps.saveOwnClusterPeerID(clusterPath); err != nil {
		ps.logf("  ⚠️  Could not save IPFS Cluster peer ID to trusted peers: %v", err)
	}

	// Initialize RQLite data directory
	rqliteDataDir := filepath.Join(dataDir, "rqlite")
	if err := ps.binaryInstaller.InitializeRQLiteDataDir(rqliteDataDir); err != nil {
		ps.logf("  ⚠️  RQLite initialization warning: %v", err)
	}

	ps.logf("  ✓ Services initialized")
	return nil
}

// saveOwnClusterPeerID reads this node's IPFS Cluster peer ID from identity.json
// and appends it to the trusted-peers file so EnsureConfig() can use it.
func (ps *ProductionSetup) saveOwnClusterPeerID(clusterPath string) error {
	identityPath := filepath.Join(clusterPath, "identity.json")
	data, err := os.ReadFile(identityPath)
	if err != nil {
		return fmt.Errorf("failed to read identity.json: %w", err)
	}

	var identity struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		return fmt.Errorf("failed to parse identity.json: %w", err)
	}
	if identity.ID == "" {
		return fmt.Errorf("peer ID not found in identity.json")
	}

	// Read existing trusted peers
	trustedPeersPath := filepath.Join(ps.oramaDir, "secrets", "ipfs-cluster-trusted-peers")
	var existing []string
	if fileData, err := os.ReadFile(trustedPeersPath); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(fileData)), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				if line == identity.ID {
					return nil // already present
				}
				existing = append(existing, line)
			}
		}
	}

	existing = append(existing, identity.ID)
	content := strings.Join(existing, "\n") + "\n"
	if err := os.WriteFile(trustedPeersPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write trusted peers file: %w", err)
	}

	ps.logf("  ✓ IPFS Cluster peer ID saved to trusted peers: %s", identity.ID)
	return nil
}

// Phase3GenerateSecrets generates shared secrets and keys
func (ps *ProductionSetup) Phase3GenerateSecrets() error {
	ps.logf("Phase 3: Generating secrets...")

	// Cluster secret
	if _, err := ps.secretGenerator.EnsureClusterSecret(); err != nil {
		return fmt.Errorf("failed to ensure cluster secret: %w", err)
	}
	ps.logf("  ✓ Cluster secret ensured")

	// Swarm key
	if _, err := ps.secretGenerator.EnsureSwarmKey(); err != nil {
		return fmt.Errorf("failed to ensure swarm key: %w", err)
	}
	ps.logf("  ✓ IPFS swarm key ensured")

	// RQLite auth credentials
	if _, _, err := ps.secretGenerator.EnsureRQLiteAuth(); err != nil {
		return fmt.Errorf("failed to ensure RQLite auth: %w", err)
	}
	ps.logf("  ✓ RQLite auth credentials ensured")

	// Olric gossip encryption key
	if _, err := ps.secretGenerator.EnsureOlricEncryptionKey(); err != nil {
		return fmt.Errorf("failed to ensure Olric encryption key: %w", err)
	}
	ps.logf("  ✓ Olric encryption key ensured")

	// API key HMAC secret
	if _, err := ps.secretGenerator.EnsureAPIKeyHMACSecret(); err != nil {
		return fmt.Errorf("failed to ensure API key HMAC secret: %w", err)
	}
	ps.logf("  ✓ API key HMAC secret ensured")

	// Serverless function secrets encryption key (bugboard #837)
	if _, err := ps.secretGenerator.EnsureSecretsEncryptionKey(); err != nil {
		return fmt.Errorf("failed to ensure secrets encryption key: %w", err)
	}
	ps.logf("  ✓ Secrets encryption key ensured")

	// Node identity (unified architecture)
	peerID, err := ps.secretGenerator.EnsureNodeIdentity()
	if err != nil {
		return fmt.Errorf("failed to ensure node identity: %w", err)
	}
	peerIDStr := peerID.String()
	ps.NodePeerID = peerIDStr // Capture for later display
	ps.logf("  ✓ Node identity ensured (Peer ID: %s)", peerIDStr)

	return nil
}

// Phase4GenerateConfigs generates node, gateway, and service configs
func (ps *ProductionSetup) Phase4GenerateConfigs(peerAddresses []string, vpsIP string, enableHTTPS bool, domain string, baseDomain string, joinAddress string, olricPeers ...[]string) error {
	if ps.IsUpdate() {
		ps.logf("Phase 4: Updating configurations...")
		ps.logf("  (Existing configs will be updated to latest format)")
	} else {
		ps.logf("Phase 4: Generating configurations...")
	}

	// Propagate operator metadata to config generator
	ps.configGenerator.SSHUser = ps.SSHUser
	ps.configGenerator.Environment = ps.Environment
	ps.configGenerator.OperatorWallet = ps.OperatorWallet

	// Node config (unified architecture)
	nodeConfig, err := ps.configGenerator.GenerateNodeConfig(peerAddresses, vpsIP, joinAddress, domain, baseDomain, enableHTTPS)
	if err != nil {
		return fmt.Errorf("failed to generate node config: %w", err)
	}

	configFile := "node.yaml"
	if err := ps.secretGenerator.SaveConfig(configFile, nodeConfig); err != nil {
		return fmt.Errorf("failed to save node config: %w", err)
	}
	ps.logf("  ✓ Node config generated: %s", configFile)

	// Gateway configuration is now embedded in each node's config
	// No separate gateway.yaml needed - each node runs its own embedded gateway

	// Olric config:
	// - HTTP API binds to localhost for security (accessed via gateway)
	// - Memberlist binds to WG IP for cluster communication across nodes
	// - Advertise WG IP so peers can reach this node
	// - Seed peers from join response for initial cluster formation
	var olricSeedPeers []string
	if len(olricPeers) > 0 {
		olricSeedPeers = olricPeers[0]
	}
	olricConfig, err := ps.configGenerator.GenerateOlricConfig(
		vpsIP, // HTTP API on WG IP (unique per node, avoids memberlist name conflict)
		3320,
		vpsIP, // Memberlist on WG IP for clustering
		3322,
		"lan", // Production environment
		vpsIP, // Advertise WG IP
		olricSeedPeers,
	)
	if err != nil {
		return fmt.Errorf("failed to generate olric config: %w", err)
	}

	// Create olric config directory
	olricConfigDir := ps.oramaDir + "/configs/olric"
	if err := os.MkdirAll(olricConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create olric config directory: %w", err)
	}

	olricConfigPath := olricConfigDir + "/config.yaml"
	if err := os.WriteFile(olricConfigPath, []byte(olricConfig), 0644); err != nil {
		return fmt.Errorf("failed to save olric config: %w", err)
	}
	ps.logf("  ✓ Olric config generated")

	// Vault Guardian config
	vaultConfig := ps.configGenerator.GenerateVaultConfig(vpsIP)
	vaultConfigPath := filepath.Join(ps.oramaDir, "data", "vault", "vault.yaml")
	if err := os.WriteFile(vaultConfigPath, []byte(vaultConfig), 0644); err != nil {
		return fmt.Errorf("failed to save vault config: %w", err)
	}
	ps.logf("  ✓ Vault config generated")

	// Configure CoreDNS (if baseDomain is provided - this is the zone name)
	// CoreDNS uses baseDomain (e.g., "dbrs.space") as the authoritative zone
	dnsZone := baseDomain
	if dnsZone == "" {
		dnsZone = domain // Fall back to node domain if baseDomain not set
	}
	if dnsZone != "" {
		// Get node IPs from peer addresses or use the VPS IP for all
		ns1IP := vpsIP
		ns2IP := vpsIP
		ns3IP := vpsIP
		if len(peerAddresses) >= 1 && peerAddresses[0] != "" {
			ns1IP = peerAddresses[0]
		}
		if len(peerAddresses) >= 2 && peerAddresses[1] != "" {
			ns2IP = peerAddresses[1]
		}
		if len(peerAddresses) >= 3 && peerAddresses[2] != "" {
			ns3IP = peerAddresses[2]
		}

		rqliteDSN := "http://localhost:5001"
		if err := ps.binaryInstaller.ConfigureCoreDNS(dnsZone, rqliteDSN, ns1IP, ns2IP, ns3IP); err != nil {
			ps.logf("  ⚠️  CoreDNS config warning: %v", err)
		} else {
			ps.logf("  ✓ CoreDNS config generated (zone: %s)", dnsZone)
		}

		// Configure Caddy (uses baseDomain for admin email if node domain not set)
		caddyDomain := domain
		if caddyDomain == "" {
			caddyDomain = baseDomain
		}
		email := "admin@" + caddyDomain
		acmeEndpoint := "http://localhost:6001/v1/internal/acme"

		// Self-hosted ntfy (feature #72): always emit the Caddy
		// push.<dnsZone> reverse-proxy block and write
		// /etc/ntfy/server.yml. Must happen BEFORE ConfigureCaddy is
		// called below so the generated Caddyfile picks up the block.
		// ntfy is installed unconditionally on every node (see Phase 2)
		// so the local 127.0.0.1:NtfyListenPort target always exists.
		ntfyHost := "push." + dnsZone
		ps.binaryInstaller.EnableCaddyNtfyProxy(ntfyHost)
		ntfyBaseURL := "https://" + ntfyHost
		if err := ps.binaryInstaller.ConfigureNtfy(ntfyBaseURL); err != nil {
			ps.logf("  ⚠️  ntfy config warning: %v", err)
		} else {
			ps.logf("  ✓ ntfy config generated (base_url: %s)", ntfyBaseURL)
		}

		if err := ps.binaryInstaller.ConfigureCaddy(caddyDomain, email, acmeEndpoint, baseDomain); err != nil {
			ps.logf("  ⚠️  Caddy config warning: %v", err)
		} else {
			ps.logf("  ✓ Caddy config generated")
		}
	}

	return nil
}

// Phase5CreateSystemdServices creates and enables systemd units
// enableHTTPS determines the RQLite Raft port (7002 when SNI is enabled, 7001 otherwise)
func (ps *ProductionSetup) Phase5CreateSystemdServices(enableHTTPS bool) error {
	ps.logf("Phase 5: Creating systemd services...")

	// Re-chown all orama directories to the orama user.
	// Phases 2b-4 create files as root (IPFS repo, configs, secrets, etc.)
	// that must be readable/writable by the orama service user.
	if err := exec.Command("id", "orama").Run(); err == nil {
		for _, dir := range []string{ps.oramaDir, filepath.Join(ps.oramaHome, "bin")} {
			if _, statErr := os.Stat(dir); statErr == nil {
				if output, chownErr := exec.Command("chown", "-R", "orama:orama", dir).CombinedOutput(); chownErr != nil {
					ps.logf("  ⚠️  Failed to chown %s: %v\n%s", dir, chownErr, string(output))
				}
			}
		}
		ps.logf("  ✓ File ownership updated for orama user")
	}

	// Validate all required binaries are available before creating services
	ipfsBinary, err := ps.binaryInstaller.ResolveBinaryPath("ipfs", "/usr/local/bin/ipfs", "/usr/bin/ipfs")
	if err != nil {
		return fmt.Errorf("ipfs binary not available: %w", err)
	}
	clusterBinary, err := ps.binaryInstaller.ResolveBinaryPath("ipfs-cluster-service", "/usr/local/bin/ipfs-cluster-service", "/usr/bin/ipfs-cluster-service")
	if err != nil {
		return fmt.Errorf("ipfs-cluster-service binary not available: %w", err)
	}
	olricBinary, err := ps.binaryInstaller.ResolveBinaryPath("olric-server", "/usr/local/bin/olric-server", "/usr/bin/olric-server")
	if err != nil {
		return fmt.Errorf("olric-server binary not available: %w", err)
	}

	// IPFS service (unified - no bootstrap/node distinction)
	ipfsUnit := ps.serviceGenerator.GenerateIPFSService(ipfsBinary)
	if err := ps.serviceController.WriteServiceUnit("orama-ipfs.service", ipfsUnit); err != nil {
		return fmt.Errorf("failed to write IPFS service: %w", err)
	}
	ps.logf("  ✓ IPFS service created: orama-ipfs.service")

	// IPFS Cluster service
	clusterUnit := ps.serviceGenerator.GenerateIPFSClusterService(clusterBinary)
	if err := ps.serviceController.WriteServiceUnit("orama-ipfs-cluster.service", clusterUnit); err != nil {
		return fmt.Errorf("failed to write IPFS Cluster service: %w", err)
	}
	ps.logf("  ✓ IPFS Cluster service created: orama-ipfs-cluster.service")

	// RQLite is managed internally by each node - no separate systemd service needed

	// Olric service
	olricUnit := ps.serviceGenerator.GenerateOlricService(olricBinary)
	if err := ps.serviceController.WriteServiceUnit("orama-olric.service", olricUnit); err != nil {
		return fmt.Errorf("failed to write Olric service: %w", err)
	}
	ps.logf("  ✓ Olric service created")

	// Node service (unified - includes embedded gateway)
	nodeUnit := ps.serviceGenerator.GenerateNodeService()
	if err := ps.serviceController.WriteServiceUnit("orama-node.service", nodeUnit); err != nil {
		return fmt.Errorf("failed to write Node service: %w", err)
	}
	ps.logf("  ✓ Node service created: orama-node.service (with embedded gateway)")

	// Vault Guardian service
	vaultUnit := ps.serviceGenerator.GenerateVaultService()
	if err := ps.serviceController.WriteServiceUnit("orama-vault.service", vaultUnit); err != nil {
		return fmt.Errorf("failed to write Vault service: %w", err)
	}
	ps.logf("  ✓ Vault service created: orama-vault.service")

	// Anyone Relay service (only created when --anyone-relay flag is used)
	// A node must run EITHER relay OR client, never both. When writing one
	// mode's service, we remove the other to prevent conflicts (they share
	// the same anon binary and would fight over ports).
	if ps.IsAnyoneRelay() {
		anyoneUnit := ps.serviceGenerator.GenerateAnyoneRelayService()
		if err := ps.serviceController.WriteServiceUnit("orama-anyone-relay.service", anyoneUnit); err != nil {
			return fmt.Errorf("failed to write Anyone Relay service: %w", err)
		}
		ps.logf("  ✓ Anyone Relay service created (operator mode, ORPort: %d)", ps.anyoneRelayConfig.ORPort)
		ps.disableConflictingAnyoneService("orama-anyone-client.service")
	} else if ps.IsAnyoneClient() {
		anyoneUnit := ps.serviceGenerator.GenerateAnyoneClientService()
		if err := ps.serviceController.WriteServiceUnit("orama-anyone-client.service", anyoneUnit); err != nil {
			return fmt.Errorf("failed to write Anyone client service: %w", err)
		}
		ps.logf("  ✓ Anyone client service created (SocksPort 9050)")
		ps.disableConflictingAnyoneService("orama-anyone-relay.service")
	} else {
		// Neither mode configured — clean up both
		ps.disableConflictingAnyoneService("orama-anyone-client.service")
		ps.disableConflictingAnyoneService("orama-anyone-relay.service")
	}

	// CoreDNS service (only for nameserver nodes)
	if ps.isNameserver {
		if _, err := os.Stat("/usr/local/bin/coredns"); err == nil {
			corednsUnit := ps.serviceGenerator.GenerateCoreDNSService()
			if err := ps.serviceController.WriteServiceUnit("coredns.service", corednsUnit); err != nil {
				ps.logf("  ⚠️  Failed to write CoreDNS service: %v", err)
			} else {
				ps.logf("  ✓ CoreDNS service created")
			}
		}
	}

	// Caddy service on ALL nodes (any node may host namespaces and need TLS)
	if _, err := os.Stat("/usr/bin/caddy"); err == nil {
		// Create caddy data directory and ensure orama user can write to it
		exec.Command("mkdir", "-p", "/var/lib/caddy").Run()
		exec.Command("chown", "-R", "orama:orama", "/var/lib/caddy").Run()

		caddyUnit := ps.serviceGenerator.GenerateCaddyService()
		if err := ps.serviceController.WriteServiceUnit("caddy.service", caddyUnit); err != nil {
			ps.logf("  ⚠️  Failed to write Caddy service: %v", err)
		} else {
			ps.logf("  ✓ Caddy service created")
		}
	}

	// Reload systemd daemon
	if err := ps.serviceController.DaemonReload(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}
	ps.logf("  ✓ Systemd daemon reloaded")

	// Enable services (unified names - no bootstrap/node distinction)
	// Note: orama-gateway.service is no longer needed - each node has an embedded gateway
	// Note: orama-rqlite.service is NOT created - RQLite is managed by each node internally
	services := []string{"orama-ipfs.service", "orama-ipfs-cluster.service", "orama-olric.service", "orama-vault.service", "orama-node.service"}

	// Add Anyone service if configured (relay or client)
	if ps.IsAnyoneRelay() {
		services = append(services, "orama-anyone-relay.service")
	} else if ps.IsAnyoneClient() {
		services = append(services, "orama-anyone-client.service")
	}

	// Add CoreDNS only for nameserver nodes
	if ps.isNameserver {
		if _, err := os.Stat("/usr/local/bin/coredns"); err == nil {
			services = append(services, "coredns.service")
		}
	}
	// Add Caddy on ALL nodes (any node may host namespaces and need TLS)
	if _, err := os.Stat("/usr/bin/caddy"); err == nil {
		services = append(services, "caddy.service")
	}
	// Add ntfy on every node (#72). The unit file is written by
	// installers/ntfy.go::writeSystemdUnit during Phase 2.
	if _, err := os.Stat("/usr/local/bin/ntfy"); err == nil {
		services = append(services, "ntfy.service")
	}
	for _, svc := range services {
		if err := ps.serviceController.EnableService(svc); err != nil {
			ps.logf("  ⚠️  Failed to enable %s: %v", svc, err)
		} else {
			ps.logf("  ✓ Service enabled: %s", svc)
		}
	}

	// Restart services in dependency order (restart instead of start ensures
	// services pick up new configs even if already running from a previous install)
	ps.logf("  Starting services...")

	// Start infrastructure first (IPFS, Olric, Vault, Anyone) - RQLite is managed internally by each node
	infraServices := []string{"orama-ipfs.service", "orama-olric.service", "orama-vault.service"}

	// Add Anyone service if configured (relay or client)
	if ps.IsAnyoneRelay() {
		orPort := 9001
		if ps.anyoneRelayConfig != nil && ps.anyoneRelayConfig.ORPort > 0 {
			orPort = ps.anyoneRelayConfig.ORPort
		}
		if ps.portChecker.IsPortInUse(orPort) {
			ps.logf("  ℹ️  ORPort %d is already in use (existing anon relay running)", orPort)
			ps.logf("  ℹ️  Skipping orama-anyone-relay startup - using existing service")
		} else {
			infraServices = append(infraServices, "orama-anyone-relay.service")
		}
	} else if ps.IsAnyoneClient() {
		infraServices = append(infraServices, "orama-anyone-client.service")
	}

	for _, svc := range infraServices {
		if err := ps.serviceController.RestartService(svc); err != nil {
			ps.logf("  ⚠️  Failed to start %s: %v", svc, err)
		} else {
			ps.logf("    - %s started", svc)
		}
	}

	// Wait a moment for infrastructure to stabilize
	time.Sleep(2 * time.Second)

	// Start IPFS Cluster
	if err := ps.serviceController.RestartService("orama-ipfs-cluster.service"); err != nil {
		ps.logf("  ⚠️  Failed to start orama-ipfs-cluster.service: %v", err)
	} else {
		ps.logf("    - orama-ipfs-cluster.service started")
	}

	// Start node service (gateway is embedded in node, no separate service needed)
	if err := ps.serviceController.RestartService("orama-node.service"); err != nil {
		ps.logf("  ⚠️  Failed to start orama-node.service: %v", err)
	} else {
		ps.logf("    - orama-node.service started (with embedded gateway)")
	}

	// Start CoreDNS (nameserver nodes only)
	if ps.isNameserver {
		if _, err := os.Stat("/usr/local/bin/coredns"); err == nil {
			if err := ps.serviceController.RestartService("coredns.service"); err != nil {
				ps.logf("  ⚠️  Failed to start coredns.service: %v", err)
			} else {
				ps.logf("    - coredns.service started")
			}
		}
	}
	// Start Caddy on ALL nodes (any node may host namespaces and need TLS)
	// Caddy depends on orama-node.service (gateway on :6001), so start after node
	if _, err := os.Stat("/usr/bin/caddy"); err == nil {
		if err := ps.serviceController.RestartService("caddy.service"); err != nil {
			ps.logf("  ⚠️  Failed to start caddy.service: %v", err)
		} else {
			ps.logf("    - caddy.service started")
		}
	}

	// Start ntfy on every node (#72). Caddy must already be up (it
	// terminates TLS for push.<dnsZone>), which the order above
	// guarantees.
	if _, err := os.Stat("/usr/local/bin/ntfy"); err == nil {
		if err := ps.serviceController.RestartService("ntfy.service"); err != nil {
			ps.logf("  ⚠️  Failed to start ntfy.service: %v", err)
		} else {
			ps.logf("    - ntfy.service started")
		}
	}

	ps.logf("  ✓ All services started")
	return nil
}

// SeedDNSRecords seeds DNS records into RQLite after services are running
func (ps *ProductionSetup) SeedDNSRecords(baseDomain, vpsIP string, peerAddresses []string) error {
	if !ps.isNameserver {
		return nil // Skip for non-nameserver nodes
	}
	if baseDomain == "" {
		return nil // Skip if no domain configured
	}

	ps.logf("Seeding DNS records...")

	// Get node IPs from peer addresses (multiaddrs) or use the VPS IP for all
	// Peer addresses are multiaddrs like /ip4/1.2.3.4/tcp/4001/p2p/12D3KooW...
	// We need to extract just the IP from them
	ns1IP := vpsIP
	ns2IP := vpsIP
	ns3IP := vpsIP

	// Extract IPs from multiaddrs
	var extractedIPs []string
	for _, peer := range peerAddresses {
		if peer != "" {
			if ip := extractIPFromMultiaddr(peer); ip != "" {
				extractedIPs = append(extractedIPs, ip)
			}
		}
	}

	// Assign extracted IPs to nameservers
	if len(extractedIPs) >= 1 {
		ns1IP = extractedIPs[0]
	}
	if len(extractedIPs) >= 2 {
		ns2IP = extractedIPs[1]
	}
	if len(extractedIPs) >= 3 {
		ns3IP = extractedIPs[2]
	}

	rqliteDSN := "http://localhost:5001"
	if err := ps.binaryInstaller.SeedDNS(baseDomain, rqliteDSN, ns1IP, ns2IP, ns3IP); err != nil {
		return fmt.Errorf("failed to seed DNS records: %w", err)
	}

	return nil
}

// Phase6SetupWireGuard installs WireGuard and generates keys for this node.
// For the first node, it self-assigns 10.0.0.1. For joining nodes, the peer
// exchange happens via HTTPS in the install CLI orchestrator.
func (ps *ProductionSetup) Phase6SetupWireGuard(isFirstNode bool) (privateKey, publicKey string, err error) {
	ps.logf("Phase 6a: Setting up WireGuard...")

	wp := NewWireGuardProvisioner(WireGuardConfig{})

	// Install WireGuard package
	if err := wp.Install(); err != nil {
		return "", "", fmt.Errorf("failed to install wireguard: %w", err)
	}
	ps.logf("  ✓ WireGuard installed")

	// Generate keypair
	privKey, pubKey, err := GenerateKeyPair()
	if err != nil {
		return "", "", fmt.Errorf("failed to generate WG keys: %w", err)
	}
	ps.logf("  ✓ WireGuard keypair generated")

	// Save public key to orama secrets so the gateway (running as orama user)
	// can read it without needing root access to /etc/wireguard/wg0.conf
	pubKeyPath := filepath.Join(ps.oramaDir, "secrets", "wg-public-key")
	if err := os.WriteFile(pubKeyPath, []byte(pubKey), 0600); err != nil {
		return "", "", fmt.Errorf("failed to save WG public key: %w", err)
	}

	if isFirstNode {
		// First node: self-assign 10.0.0.1, no peers yet
		wp.config = WireGuardConfig{
			PrivateKey: privKey,
			PrivateIP:  "10.0.0.1",
			ListenPort: 51820,
		}
		if err := wp.WriteConfig(); err != nil {
			return "", "", fmt.Errorf("failed to write WG config: %w", err)
		}
		if err := wp.Enable(); err != nil {
			return "", "", fmt.Errorf("failed to enable WG: %w", err)
		}
		ps.logf("  ✓ WireGuard enabled (first node: 10.0.0.1)")
	}

	return privKey, pubKey, nil
}

// Phase6bSetupFirewall sets up UFW firewall rules
func (ps *ProductionSetup) Phase6bSetupFirewall(skipFirewall bool) error {
	if skipFirewall {
		ps.logf("Phase 6b: Skipping firewall setup (--skip-firewall)")
		return nil
	}

	ps.logf("Phase 6b: Setting up UFW firewall...")

	anyoneORPort := 0
	if ps.IsAnyoneRelay() && ps.anyoneRelayConfig != nil {
		anyoneORPort = ps.anyoneRelayConfig.ORPort
	}

	fp := NewFirewallProvisioner(FirewallConfig{
		SSHPort:       22,
		IsNameserver:  ps.isNameserver,
		AnyoneORPort:  anyoneORPort,
		WireGuardPort: 51820,
	})

	if err := fp.Setup(); err != nil {
		return fmt.Errorf("firewall setup failed: %w", err)
	}

	ps.logf("  ✓ UFW firewall configured and enabled")
	return nil
}

// EnableWireGuardWithPeers writes WG config with assigned IP and peers, then enables it.
// Called by joining nodes after peer exchange.
func (ps *ProductionSetup) EnableWireGuardWithPeers(privateKey, assignedIP string, peers []WireGuardPeer) error {
	wp := NewWireGuardProvisioner(WireGuardConfig{
		PrivateKey: privateKey,
		PrivateIP:  assignedIP,
		ListenPort: 51820,
		Peers:      peers,
	})

	if err := wp.WriteConfig(); err != nil {
		return fmt.Errorf("failed to write WG config: %w", err)
	}
	if err := wp.Enable(); err != nil {
		return fmt.Errorf("failed to enable WG: %w", err)
	}

	ps.logf("  ✓ WireGuard enabled (IP: %s, peers: %d)", assignedIP, len(peers))
	return nil
}

// LogSetupComplete logs completion information
func (ps *ProductionSetup) LogSetupComplete(peerID string) {
	ps.logf("\n" + strings.Repeat("=", 70))
	ps.logf("Setup Complete!")
	ps.logf(strings.Repeat("=", 70))
	ps.logf("\nNode Peer ID: %s", peerID)
	ps.logf("\nService Management:")
	ps.logf("  systemctl status orama-ipfs")
	ps.logf("  journalctl -u orama-node -f")
	ps.logf("  tail -f %s/logs/node.log", ps.oramaDir)
	ps.logf("\nLog Files:")
	ps.logf("  %s/logs/ipfs.log", ps.oramaDir)
	ps.logf("  %s/logs/ipfs-cluster.log", ps.oramaDir)
	ps.logf("  %s/logs/olric.log", ps.oramaDir)
	ps.logf("  %s/logs/node.log", ps.oramaDir)
	ps.logf("  %s/logs/gateway.log", ps.oramaDir)
	ps.logf("  %s/logs/vault.log", ps.oramaDir)

	// Anyone mode-specific logs and commands
	if ps.IsAnyoneRelay() {
		ps.logf("  /var/log/anon/notices.log (Anyone Relay)")
		ps.logf("\nStart All Services:")
		ps.logf("  systemctl start orama-ipfs orama-ipfs-cluster orama-olric orama-vault orama-anyone-relay orama-node")
		ps.logf("\nAnyone Relay Operator:")
		ps.logf("  ORPort: %d", ps.anyoneRelayConfig.ORPort)
		ps.logf("  Wallet: %s", ps.anyoneRelayConfig.Wallet)
		ps.logf("  Config: /etc/anon/anonrc")
		ps.logf("  Register at: https://dashboard.anyone.io")
		ps.logf("  IMPORTANT: You need 100 $ANYONE tokens in your wallet to receive rewards")
	} else if ps.IsAnyoneClient() {
		ps.logf("\nStart All Services:")
		ps.logf("  systemctl start orama-ipfs orama-ipfs-cluster orama-olric orama-vault orama-anyone-client orama-node")
	} else {
		ps.logf("\nStart All Services:")
		ps.logf("  systemctl start orama-ipfs orama-ipfs-cluster orama-olric orama-vault orama-node")
	}

	ps.logf("\nVerify Installation:")
	ps.logf("  curl http://localhost:6001/health")
	ps.logf("  curl http://localhost:5001/status\n")
}
