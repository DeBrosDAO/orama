package production

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProductionSetup orchestrates the entire production deployment
type ProductionSetup struct {
	osInfo            *OSInfo
	arch              string
	debrosHome        string
	debrosDir         string
	logWriter         io.Writer
	forceReconfigure  bool
	skipOptionalDeps  bool
	privChecker       *PrivilegeChecker
	osDetector        *OSDetector
	archDetector      *ArchitectureDetector
	resourceChecker   *ResourceChecker
	fsProvisioner     *FilesystemProvisioner
	userProvisioner   *UserProvisioner
	stateDetector     *StateDetector
	configGenerator   *ConfigGenerator
	secretGenerator   *SecretGenerator
	serviceGenerator  *SystemdServiceGenerator
	serviceController *SystemdController
	binaryInstaller   *BinaryInstaller
	branch            string
	NodePeerID        string // Captured during Phase3 for later display
}

// ReadBranchPreference reads the stored branch preference from disk
func ReadBranchPreference(debrosDir string) string {
	branchFile := filepath.Join(debrosDir, ".branch")
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
func SaveBranchPreference(debrosDir, branch string) error {
	branchFile := filepath.Join(debrosDir, ".branch")
	if err := os.MkdirAll(debrosDir, 0755); err != nil {
		return fmt.Errorf("failed to create debros directory: %w", err)
	}
	if err := os.WriteFile(branchFile, []byte(branch), 0644); err != nil {
		return fmt.Errorf("failed to save branch preference: %w", err)
	}
	exec.Command("chown", "debros:debros", branchFile).Run()
	return nil
}

// NewProductionSetup creates a new production setup orchestrator
func NewProductionSetup(debrosHome string, logWriter io.Writer, forceReconfigure bool, branch string) *ProductionSetup {
	debrosDir := debrosHome + "/.debros"
	arch, _ := (&ArchitectureDetector{}).Detect()

	// If branch is empty, try to read from stored preference, otherwise default to main
	if branch == "" {
		branch = ReadBranchPreference(debrosDir)
	}

	return &ProductionSetup{
		debrosHome:        debrosHome,
		debrosDir:         debrosDir,
		logWriter:         logWriter,
		forceReconfigure:  forceReconfigure,
		arch:              arch,
		branch:            branch,
		privChecker:       &PrivilegeChecker{},
		osDetector:        &OSDetector{},
		archDetector:      &ArchitectureDetector{},
		resourceChecker:   NewResourceChecker(),
		fsProvisioner:     NewFilesystemProvisioner(debrosHome),
		userProvisioner:   NewUserProvisioner("debros", debrosHome, "/bin/bash"),
		stateDetector:     NewStateDetector(debrosDir),
		configGenerator:   NewConfigGenerator(debrosDir),
		secretGenerator:   NewSecretGenerator(debrosDir),
		serviceGenerator:  NewSystemdServiceGenerator(debrosHome, debrosDir),
		serviceController: NewSystemdController(),
		binaryInstaller:   NewBinaryInstaller(arch, logWriter),
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
	if err := ps.resourceChecker.CheckDiskSpace(ps.debrosHome); err != nil {
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

	// Create directory structure (base directories only - node-specific dirs created in Phase2c)
	if err := ps.fsProvisioner.EnsureDirectoryStructure(""); err != nil {
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

	// Install DeBros binaries
	if err := ps.binaryInstaller.InstallDeBrosBinaries(ps.branch, ps.debrosHome); err != nil {
		return fmt.Errorf("failed to install DeBros binaries: %w", err)
	}

	ps.logf("  ✓ All binaries installed")
	return nil
}

// Phase2cInitializeServices initializes service repositories and configurations
func (ps *ProductionSetup) Phase2cInitializeServices(nodeType string) error {
	ps.logf("Phase 2c: Initializing services...")

	// Ensure node-specific directories exist
	if err := ps.fsProvisioner.EnsureDirectoryStructure(nodeType); err != nil {
		return fmt.Errorf("failed to create node-specific directories: %w", err)
	}

	// Build paths with nodeType awareness to match systemd unit definitions
	dataDir := filepath.Join(ps.debrosDir, "data", nodeType)

	// Initialize IPFS repo with correct path structure
	// Use port 4501 for API (to avoid conflict with RQLite on 5001), 8080 for gateway (standard), 4001 for swarm
	ipfsRepoPath := filepath.Join(dataDir, "ipfs", "repo")
	if err := ps.binaryInstaller.InitializeIPFSRepo(nodeType, ipfsRepoPath, filepath.Join(ps.debrosDir, "secrets", "swarm.key"), 4501, 8080, 4001); err != nil {
		return fmt.Errorf("failed to initialize IPFS repo: %w", err)
	}

	// Initialize IPFS Cluster config (runs ipfs-cluster-service init)
	clusterPath := filepath.Join(dataDir, "ipfs-cluster")
	clusterSecret, err := ps.secretGenerator.EnsureClusterSecret()
	if err != nil {
		return fmt.Errorf("failed to get cluster secret: %w", err)
	}
	if err := ps.binaryInstaller.InitializeIPFSClusterConfig(nodeType, clusterPath, clusterSecret, 4501); err != nil {
		return fmt.Errorf("failed to initialize IPFS Cluster: %w", err)
	}

	// Initialize RQLite data directory
	rqliteDataDir := filepath.Join(dataDir, "rqlite")
	if err := ps.binaryInstaller.InitializeRQLiteDataDir(nodeType, rqliteDataDir); err != nil {
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
func (ps *ProductionSetup) Phase3GenerateSecrets(isBootstrap bool) error {
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

	// Node identity
	nodeType := "node"
	if isBootstrap {
		nodeType = "bootstrap"
	}

	peerID, err := ps.secretGenerator.EnsureNodeIdentity(nodeType)
	if err != nil {
		return fmt.Errorf("failed to ensure node identity: %w", err)
	}
	peerIDStr := peerID.String()
	ps.NodePeerID = peerIDStr // Capture for later display
	ps.logf("  ✓ Node identity ensured (Peer ID: %s)", peerIDStr)

	return nil
}

// Phase4GenerateConfigs generates node, gateway, and service configs
func (ps *ProductionSetup) Phase4GenerateConfigs(isBootstrap bool, bootstrapPeers []string, vpsIP string, enableHTTPS bool, domain string, bootstrapJoin string) error {
	if ps.IsUpdate() {
		ps.logf("Phase 4: Updating configurations...")
		ps.logf("  (Existing configs will be updated to latest format)")
	} else {
		ps.logf("Phase 4: Generating configurations...")
	}

	// Node config
	nodeConfig, err := ps.configGenerator.GenerateNodeConfig(isBootstrap, bootstrapPeers, vpsIP, bootstrapJoin)
	if err != nil {
		return fmt.Errorf("failed to generate node config: %w", err)
	}

	var configFile string
	if isBootstrap {
		configFile = "bootstrap.yaml"
	} else {
		configFile = "node.yaml"
	}

	if err := ps.secretGenerator.SaveConfig(configFile, nodeConfig); err != nil {
		return fmt.Errorf("failed to save node config: %w", err)
	}
	ps.logf("  ✓ Node config generated: %s", configFile)

	// Gateway config
	olricServers := []string{"127.0.0.1:3320"}
	gatewayConfig, err := ps.configGenerator.GenerateGatewayConfig(bootstrapPeers, enableHTTPS, domain, olricServers)
	if err != nil {
		return fmt.Errorf("failed to generate gateway config: %w", err)
	}

	if err := ps.secretGenerator.SaveConfig("gateway.yaml", gatewayConfig); err != nil {
		return fmt.Errorf("failed to save gateway config: %w", err)
	}
	ps.logf("  ✓ Gateway config generated")

	// Olric config
	olricConfig, err := ps.configGenerator.GenerateOlricConfig("localhost", 3320, 3322)
	if err != nil {
		return fmt.Errorf("failed to generate olric config: %w", err)
	}

	// Create olric config directory
	olricConfigDir := ps.debrosDir + "/configs/olric"
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
func (ps *ProductionSetup) Phase5CreateSystemdServices(nodeType string, vpsIP string) error {
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
	rqliteBinary, err := ps.binaryInstaller.ResolveBinaryPath("rqlited", "/usr/local/bin/rqlited", "/usr/bin/rqlited")
	if err != nil {
		return fmt.Errorf("rqlited binary not available: %w", err)
	}
	olricBinary, err := ps.binaryInstaller.ResolveBinaryPath("olric-server", "/usr/local/bin/olric-server", "/usr/bin/olric-server")
	if err != nil {
		return fmt.Errorf("olric-server binary not available: %w", err)
	}

	// IPFS service
	ipfsUnit := ps.serviceGenerator.GenerateIPFSService(nodeType, ipfsBinary)
	unitName := fmt.Sprintf("debros-ipfs-%s.service", nodeType)
	if err := ps.serviceController.WriteServiceUnit(unitName, ipfsUnit); err != nil {
		return fmt.Errorf("failed to write IPFS service: %w", err)
	}
	ps.logf("  ✓ IPFS service created: %s", unitName)

	// IPFS Cluster service
	clusterUnit := ps.serviceGenerator.GenerateIPFSClusterService(nodeType, clusterBinary)
	clusterUnitName := fmt.Sprintf("debros-ipfs-cluster-%s.service", nodeType)
	if err := ps.serviceController.WriteServiceUnit(clusterUnitName, clusterUnit); err != nil {
		return fmt.Errorf("failed to write IPFS Cluster service: %w", err)
	}
	ps.logf("  ✓ IPFS Cluster service created: %s", clusterUnitName)

	// RQLite service with join address for non-bootstrap nodes
	rqliteJoinAddr := ""
	if nodeType != "bootstrap" && vpsIP != "" {
		rqliteJoinAddr = vpsIP + ":7001"
	}

	// Log the advertise configuration for verification
	advertiseIP := vpsIP
	if advertiseIP == "" {
		advertiseIP = "127.0.0.1"
	}
	ps.logf("  RQLite will advertise: %s (advertise IP: %s)", rqliteJoinAddr, advertiseIP)

	rqliteUnit := ps.serviceGenerator.GenerateRQLiteService(nodeType, rqliteBinary, 5001, 7001, rqliteJoinAddr, advertiseIP)
	rqliteUnitName := fmt.Sprintf("debros-rqlite-%s.service", nodeType)
	if err := ps.serviceController.WriteServiceUnit(rqliteUnitName, rqliteUnit); err != nil {
		return fmt.Errorf("failed to write RQLite service: %w", err)
	}
	ps.logf("  ✓ RQLite service created: %s", rqliteUnitName)

	// Olric service
	olricUnit := ps.serviceGenerator.GenerateOlricService(olricBinary)
	if err := ps.serviceController.WriteServiceUnit("debros-olric.service", olricUnit); err != nil {
		return fmt.Errorf("failed to write Olric service: %w", err)
	}
	ps.logf("  ✓ Olric service created")

	// Node service
	nodeUnit := ps.serviceGenerator.GenerateNodeService(nodeType)
	nodeUnitName := fmt.Sprintf("debros-node-%s.service", nodeType)
	if err := ps.serviceController.WriteServiceUnit(nodeUnitName, nodeUnit); err != nil {
		return fmt.Errorf("failed to write Node service: %w", err)
	}
	ps.logf("  ✓ Node service created: %s", nodeUnitName)

	// Gateway service (optional, only on specific nodes)
	gatewayUnit := ps.serviceGenerator.GenerateGatewayService(nodeType)
	if err := ps.serviceController.WriteServiceUnit("debros-gateway.service", gatewayUnit); err != nil {
		return fmt.Errorf("failed to write Gateway service: %w", err)
	}
	ps.logf("  ✓ Gateway service created")

	// Reload systemd daemon
	if err := ps.serviceController.DaemonReload(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}
	ps.logf("  ✓ Systemd daemon reloaded")

	// Enable services
	services := []string{unitName, clusterUnitName, rqliteUnitName, "debros-olric.service", nodeUnitName, "debros-gateway.service"}
	for _, svc := range services {
		if err := ps.serviceController.EnableService(svc); err != nil {
			ps.logf("  ⚠️  Failed to enable %s: %v", svc, err)
		} else {
			ps.logf("  ✓ Service enabled: %s", svc)
		}
	}

	// Start services in dependency order
	ps.logf("  Starting services...")

	// Start infrastructure first (IPFS, RQLite, Olric)
	infraServices := []string{unitName, rqliteUnitName, "debros-olric.service"}
	for _, svc := range infraServices {
		if err := ps.serviceController.StartService(svc); err != nil {
			ps.logf("  ⚠️  Failed to start %s: %v", svc, err)
		} else {
			ps.logf("    - %s started", svc)
		}
	}

	// Wait a moment for infrastructure to stabilize
	exec.Command("sleep", "2").Run()

	// Start IPFS Cluster
	if err := ps.serviceController.StartService(clusterUnitName); err != nil {
		ps.logf("  ⚠️  Failed to start %s: %v", clusterUnitName, err)
	} else {
		ps.logf("    - %s started", clusterUnitName)
	}

	// Start application services
	appServices := []string{nodeUnitName, "debros-gateway.service"}
	for _, svc := range appServices {
		if err := ps.serviceController.StartService(svc); err != nil {
			ps.logf("  ⚠️  Failed to start %s: %v", svc, err)
		} else {
			ps.logf("    - %s started", svc)
		}
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
	ps.logf("  systemctl status debros-ipfs-bootstrap")
	ps.logf("  journalctl -u debros-node-bootstrap -f")
	ps.logf("  tail -f %s/logs/node-bootstrap.log", ps.debrosDir)
	ps.logf("\nLog Files:")
	ps.logf("  %s/logs/ipfs-bootstrap.log", ps.debrosDir)
	ps.logf("  %s/logs/ipfs-cluster-bootstrap.log", ps.debrosDir)
	ps.logf("  %s/logs/rqlite-bootstrap.log", ps.debrosDir)
	ps.logf("  %s/logs/olric.log", ps.debrosDir)
	ps.logf("  %s/logs/node-bootstrap.log", ps.debrosDir)
	ps.logf("  %s/logs/gateway.log", ps.debrosDir)
	ps.logf("\nStart All Services:")
	ps.logf("  systemctl start debros-ipfs-bootstrap debros-ipfs-cluster-bootstrap debros-rqlite-bootstrap debros-olric debros-node-bootstrap debros-gateway")
	ps.logf("\nVerify Installation:")
	ps.logf("  curl http://localhost:6001/health")
	ps.logf("  curl http://localhost:5001/status\n")
}
