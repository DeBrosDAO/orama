package production

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ProductionSetup orchestrates the entire production deployment
type ProductionSetup struct {
	osInfo             *OSInfo
	arch               string
	oramaHome         string
	oramaDir          string
	logWriter          io.Writer
	forceReconfigure   bool
	skipOptionalDeps   bool
	skipResourceChecks bool
	privChecker        *PrivilegeChecker
	osDetector         *OSDetector
	archDetector       *ArchitectureDetector
	resourceChecker    *ResourceChecker
	portChecker        *PortChecker
	fsProvisioner      *FilesystemProvisioner
	userProvisioner    *UserProvisioner
	stateDetector      *StateDetector
	configGenerator    *ConfigGenerator
	secretGenerator    *SecretGenerator
	serviceGenerator   *SystemdServiceGenerator
	serviceController  *SystemdController
	binaryInstaller    *BinaryInstaller
	branch             string
	skipRepoUpdate     bool
	NodePeerID         string // Captured during Phase3 for later display
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
		return fmt.Errorf("failed to create debros directory: %w", err)
	}
	if err := os.WriteFile(branchFile, []byte(branch), 0644); err != nil {
		return fmt.Errorf("failed to save branch preference: %w", err)
	}
	exec.Command("chown", "debros:debros", branchFile).Run()
	return nil
}

// NewProductionSetup creates a new production setup orchestrator
func NewProductionSetup(oramaHome string, logWriter io.Writer, forceReconfigure bool, branch string, skipRepoUpdate bool, skipResourceChecks bool) *ProductionSetup {
	oramaDir := filepath.Join(oramaHome, ".orama")
	arch, _ := (&ArchitectureDetector{}).Detect()

	// If branch is empty, try to read from stored preference, otherwise default to main
	if branch == "" {
		branch = ReadBranchPreference(oramaDir)
	}

	return &ProductionSetup{
		oramaHome:         oramaHome,
		oramaDir:          oramaDir,
		logWriter:          logWriter,
		forceReconfigure:   forceReconfigure,
		arch:               arch,
		branch:             branch,
		skipRepoUpdate:     skipRepoUpdate,
		skipResourceChecks: skipResourceChecks,
		privChecker:        &PrivilegeChecker{},
		osDetector:         &OSDetector{},
		archDetector:       &ArchitectureDetector{},
		resourceChecker:    NewResourceChecker(),
		portChecker:        NewPortChecker(),
		fsProvisioner:      NewFilesystemProvisioner(oramaHome),
		userProvisioner:    NewUserProvisioner("debros", oramaHome, "/bin/bash"),
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

	// Check basic dependencies
	depChecker := NewDependencyChecker(ps.skipOptionalDeps)
	if missing, err := depChecker.CheckAll(); err != nil {
		ps.logf("  ❌ Missing dependencies:")
		for _, dep := range missing {
			ps.logf("     - %s: %s", dep.Name, dep.InstallHint)
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

// Phase2ProvisionEnvironment sets up users and filesystems
func (ps *ProductionSetup) Phase2ProvisionEnvironment() error {
	ps.logf("Phase 2: Provisioning environment...")

	// Create debros user
	if !ps.userProvisioner.UserExists() {
		if err := ps.userProvisioner.CreateUser(); err != nil {
			return fmt.Errorf("failed to create debros user: %w", err)
		}
		ps.logf("  ✓ Created 'debros' user")
	} else {
		ps.logf("  ✓ 'debros' user already exists")
	}

	// Set up sudoers access if invoked via sudo
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser != "" {
		if err := ps.userProvisioner.SetupSudoersAccess(sudoUser); err != nil {
			ps.logf("  ⚠️  Failed to setup sudoers: %v", err)
		} else {
			ps.logf("  ✓ Sudoers access configured")
		}
	}

	// Create directory structure (unified structure)
	if err := ps.fsProvisioner.EnsureDirectoryStructure(); err != nil {
		return fmt.Errorf("failed to create directory structure: %w", err)
	}
	ps.logf("  ✓ Directory structure created")

	// Fix ownership
	if err := ps.fsProvisioner.FixOwnership(); err != nil {
		return fmt.Errorf("failed to fix ownership: %w", err)
	}
	ps.logf("  ✓ Ownership fixed")

	return nil
}

// Phase2bInstallBinaries installs external binaries and DeBros components
func (ps *ProductionSetup) Phase2bInstallBinaries() error {
	ps.logf("Phase 2b: Installing binaries...")

	// Install system dependencies
	if err := ps.binaryInstaller.InstallSystemDependencies(); err != nil {
		ps.logf("  ⚠️  System dependencies warning: %v", err)
	}

	// Install Go if not present
	if err := ps.binaryInstaller.InstallGo(); err != nil {
		return fmt.Errorf("failed to install Go: %w", err)
	}

	// Install binaries
	if err := ps.binaryInstaller.InstallRQLite(); err != nil {
		ps.logf("  ⚠️  RQLite install warning: %v", err)
	}

	if err := ps.binaryInstaller.InstallIPFS(); err != nil {
		ps.logf("  ⚠️  IPFS install warning: %v", err)
	}

	if err := ps.binaryInstaller.InstallIPFSCluster(); err != nil {
		ps.logf("  ⚠️  IPFS Cluster install warning: %v", err)
	}

	if err := ps.binaryInstaller.InstallOlric(); err != nil {
		ps.logf("  ⚠️  Olric install warning: %v", err)
	}

	// Install anyone-client for SOCKS5 proxy
	if err := ps.binaryInstaller.InstallAnyoneClient(); err != nil {
		ps.logf("  ⚠️  anyone-client install warning: %v", err)
	}

	// Install DeBros binaries
	if err := ps.binaryInstaller.InstallDeBrosBinaries(ps.branch, ps.oramaHome, ps.skipRepoUpdate); err != nil {
		return fmt.Errorf("failed to install DeBros binaries: %w", err)
	}

	ps.logf("  ✓ All binaries installed")
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
	if err := ps.binaryInstaller.InitializeIPFSRepo(ipfsRepoPath, filepath.Join(ps.oramaDir, "secrets", "swarm.key"), 4501, 8080, 4101, ipfsPeer); err != nil {
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
		// Format: /ip4/<ip>/tcp/9098/p2p/<cluster-peer-id>
		peerIP := inferPeerIP(peerAddresses, vpsIP)
		if peerIP != "" {
			// Construct the bootstrap multiaddress for IPFS Cluster
			// Note: IPFS Cluster listens on port 9098 for cluster communication
			clusterBootstrapAddr := fmt.Sprintf("/ip4/%s/tcp/9098/p2p/%s", peerIP, ipfsClusterPeer.PeerID)
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

	// Initialize RQLite data directory
	rqliteDataDir := filepath.Join(dataDir, "rqlite")
	if err := ps.binaryInstaller.InitializeRQLiteDataDir(rqliteDataDir); err != nil {
		ps.logf("  ⚠️  RQLite initialization warning: %v", err)
	}

	// Ensure all directories and files created during service initialization have correct ownership
	// This is critical because directories/files created as root need to be owned by debros user
	if err := ps.fsProvisioner.FixOwnership(); err != nil {
		return fmt.Errorf("failed to fix ownership after service initialization: %w", err)
	}

	ps.logf("  ✓ Services initialized")
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
func (ps *ProductionSetup) Phase4GenerateConfigs(peerAddresses []string, vpsIP string, enableHTTPS bool, domain string, joinAddress string) error {
	if ps.IsUpdate() {
		ps.logf("Phase 4: Updating configurations...")
		ps.logf("  (Existing configs will be updated to latest format)")
	} else {
		ps.logf("Phase 4: Generating configurations...")
	}

	// Node config (unified architecture)
	nodeConfig, err := ps.configGenerator.GenerateNodeConfig(peerAddresses, vpsIP, joinAddress, domain, enableHTTPS)
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
	// - Memberlist binds to 0.0.0.0 for cluster communication across nodes
	// - Environment "lan" for production multi-node clustering
	olricConfig, err := ps.configGenerator.GenerateOlricConfig(
		"127.0.0.1", // HTTP API on localhost
		3320,
		"0.0.0.0", // Memberlist on all interfaces for clustering
		3322,
		"lan", // Production environment
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
	exec.Command("chown", "debros:debros", olricConfigPath).Run()
	ps.logf("  ✓ Olric config generated")

	return nil
}

// Phase5CreateSystemdServices creates and enables systemd units
// enableHTTPS determines the RQLite Raft port (7002 when SNI is enabled, 7001 otherwise)
func (ps *ProductionSetup) Phase5CreateSystemdServices(enableHTTPS bool) error {
	ps.logf("Phase 5: Creating systemd services...")

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
	if err := ps.serviceController.WriteServiceUnit("debros-ipfs.service", ipfsUnit); err != nil {
		return fmt.Errorf("failed to write IPFS service: %w", err)
	}
	ps.logf("  ✓ IPFS service created: debros-ipfs.service")

	// IPFS Cluster service
	clusterUnit := ps.serviceGenerator.GenerateIPFSClusterService(clusterBinary)
	if err := ps.serviceController.WriteServiceUnit("debros-ipfs-cluster.service", clusterUnit); err != nil {
		return fmt.Errorf("failed to write IPFS Cluster service: %w", err)
	}
	ps.logf("  ✓ IPFS Cluster service created: debros-ipfs-cluster.service")

	// RQLite is managed internally by each node - no separate systemd service needed

	// Olric service
	olricUnit := ps.serviceGenerator.GenerateOlricService(olricBinary)
	if err := ps.serviceController.WriteServiceUnit("debros-olric.service", olricUnit); err != nil {
		return fmt.Errorf("failed to write Olric service: %w", err)
	}
	ps.logf("  ✓ Olric service created")

	// Node service (unified - includes embedded gateway)
	nodeUnit := ps.serviceGenerator.GenerateNodeService()
	if err := ps.serviceController.WriteServiceUnit("debros-node.service", nodeUnit); err != nil {
		return fmt.Errorf("failed to write Node service: %w", err)
	}
	ps.logf("  ✓ Node service created: debros-node.service (with embedded gateway)")

	// Anyone Client service (SOCKS5 proxy)
	anyoneUnit := ps.serviceGenerator.GenerateAnyoneClientService()
	if err := ps.serviceController.WriteServiceUnit("debros-anyone-client.service", anyoneUnit); err != nil {
		return fmt.Errorf("failed to write Anyone Client service: %w", err)
	}
	ps.logf("  ✓ Anyone Client service created")

	// Reload systemd daemon
	if err := ps.serviceController.DaemonReload(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}
	ps.logf("  ✓ Systemd daemon reloaded")

	// Enable services (unified names - no bootstrap/node distinction)
	// Note: debros-gateway.service is no longer needed - each node has an embedded gateway
	// Note: debros-rqlite.service is NOT created - RQLite is managed by each node internally
	services := []string{"debros-ipfs.service", "debros-ipfs-cluster.service", "debros-olric.service", "debros-node.service", "debros-anyone-client.service"}
	for _, svc := range services {
		if err := ps.serviceController.EnableService(svc); err != nil {
			ps.logf("  ⚠️  Failed to enable %s: %v", svc, err)
		} else {
			ps.logf("  ✓ Service enabled: %s", svc)
		}
	}

	// Start services in dependency order
	ps.logf("  Starting services...")

	// Start infrastructure first (IPFS, Olric, Anyone Client) - RQLite is managed internally by each node
	infraServices := []string{"debros-ipfs.service", "debros-olric.service"}
	
	// Check if port 9050 is already in use (e.g., another anyone-client or similar service)
	if ps.portChecker.IsPortInUse(9050) {
		ps.logf("  ℹ️  Port 9050 is already in use (anyone-client or similar service running)")
		ps.logf("  ℹ️  Skipping debros-anyone-client startup - using existing service")
	} else {
		infraServices = append(infraServices, "debros-anyone-client.service")
	}
	
	for _, svc := range infraServices {
		if err := ps.serviceController.StartService(svc); err != nil {
			ps.logf("  ⚠️  Failed to start %s: %v", svc, err)
		} else {
			ps.logf("    - %s started", svc)
		}
	}

	// Wait a moment for infrastructure to stabilize
	time.Sleep(2 * time.Second)

	// Start IPFS Cluster
	if err := ps.serviceController.StartService("debros-ipfs-cluster.service"); err != nil {
		ps.logf("  ⚠️  Failed to start debros-ipfs-cluster.service: %v", err)
	} else {
		ps.logf("    - debros-ipfs-cluster.service started")
	}

	// Start node service (gateway is embedded in node, no separate service needed)
	if err := ps.serviceController.StartService("debros-node.service"); err != nil {
		ps.logf("  ⚠️  Failed to start debros-node.service: %v", err)
	} else {
		ps.logf("    - debros-node.service started (with embedded gateway)")
	}

	ps.logf("  ✓ All services started")
	return nil
}

// LogSetupComplete logs completion information
func (ps *ProductionSetup) LogSetupComplete(peerID string) {
	ps.logf("\n" + strings.Repeat("=", 70))
	ps.logf("Setup Complete!")
	ps.logf(strings.Repeat("=", 70))
	ps.logf("\nNode Peer ID: %s", peerID)
	ps.logf("\nService Management:")
	ps.logf("  systemctl status debros-ipfs")
	ps.logf("  journalctl -u debros-node -f")
	ps.logf("  tail -f %s/logs/node.log", ps.oramaDir)
	ps.logf("\nLog Files:")
	ps.logf("  %s/logs/ipfs.log", ps.oramaDir)
	ps.logf("  %s/logs/ipfs-cluster.log", ps.oramaDir)
	ps.logf("  %s/logs/olric.log", ps.oramaDir)
	ps.logf("  %s/logs/node.log", ps.oramaDir)
	ps.logf("  %s/logs/gateway.log", ps.oramaDir)
	ps.logf("  %s/logs/anyone-client.log", ps.oramaDir)
	ps.logf("\nStart All Services:")
	ps.logf("  systemctl start debros-ipfs debros-ipfs-cluster debros-olric debros-anyone-client debros-node")
	ps.logf("\nVerify Installation:")
	ps.logf("  curl http://localhost:6001/health")
	ps.logf("  curl http://localhost:5001/status")
	ps.logf("  # Anyone Client SOCKS5 proxy on localhost:9050\n")
}
