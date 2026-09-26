package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/install/installers"
	"github.com/DeBrosOfficial/network/pkg/legacylayout"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/systemd"
)

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
	isNameserver       bool // Whether this node is a nameserver (runs CoreDNS)
	privChecker        *PrivilegeChecker
	osDetector         *OSDetector
	archDetector       *ArchitectureDetector
	resourceChecker    *ResourceChecker
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

// SetPublicIP records this node's public address in node.yaml.
func (ps *ProductionSetup) SetPublicIP(ip string) { ps.configGenerator.SetPublicIP(ip) }

// PublicIP is the address set for this run, else the one node.yaml records,
// else "".
func (ps *ProductionSetup) PublicIP() (string, error) { return ps.configGenerator.PublicIP() }

// SetACMECA sets the ACME directory Caddy issues certificates from.
func (ps *ProductionSetup) SetACMECA(url string) { ps.configGenerator.SetACMECA(url) }

// SetNameserver sets whether this node is a nameserver (runs CoreDNS + Caddy).
func (ps *ProductionSetup) SetNameserver(isNameserver bool) {
	ps.isNameserver = isNameserver
}

// IsNameserver returns whether this node is configured as a nameserver
func (ps *ProductionSetup) IsNameserver() bool {
	return ps.isNameserver
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

	// An unsupported release used to log a warning and carry on, then fail
	// minutes later in Phase 2d when the Tor Project had no apt suite for it.
	if !ps.osDetector.IsSupportedOS(osInfo) {
		return fmt.Errorf("OS %s is not supported (supported: %s); reinstall the VPS with a supported release or upgrade it with do-release-upgrade", osInfo.Name, SupportedReleasesText())
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

	// Create the dedicated orama user the services run as. Fatal: the units
	// say User=orama, so without it nothing starts — the old message that the
	// services would "run as root" instead was never true.
	if err := ps.fsProvisioner.EnsureOramaUser(); err != nil {
		return fmt.Errorf("create the orama user: %w", err)
	}
	ps.logf("  ✓ orama user ensured")

	return nil
}

// Phase2bInstallBinaries installs the binaries of the build archive extracted
// at /opt/orama. There is no other way to install: no archive, or a manifest
// that cannot be read, is a hard failure. (Compiling /opt/orama/src on the
// node was the other way, and it installed whatever that directory held with
// no signature check at all.)
func (ps *ProductionSetup) Phase2bInstallBinaries() error {
	ps.logf("Phase 2b: Installing binaries...")

	if !HasPreBuiltArchive() {
		return fmt.Errorf("no build archive at %s (%s is missing): put one there with `orama node setup` on a "+
			"new machine or `orama push` on an installed node", OramaBase, OramaManifest)
	}
	manifest, err := LoadPreBuiltManifest()
	if err != nil {
		return fmt.Errorf("refusing to install from %s: %w", OramaBase, err)
	}
	if err := ps.installFromPreBuilt(manifest); err != nil {
		return err
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
	root := OramaRoot(ps.oramaDir)

	// Initialize IPFS repo with correct path structure
	// API on constants.IPFSAPIPort, Kubo gateway on IPFSGatewayPort, swarm on
	// IPFSSwarmPort (kept off 4001 so it cannot collide with the node's libp2p host).
	ipfsRepoPath := filepath.Join(dataDir, "ipfs", "repo")
	if err := ps.binaryInstaller.InitializeIPFSRepo(root, ipfsRepoPath, filepath.Join(ps.oramaDir, "secrets", "swarm.key"), constants.IPFSAPIPort, constants.IPFSGatewayPort, constants.IPFSSwarmPort, vpsIP, ipfsPeer); err != nil {
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
		// Construct cluster peer multiaddress using the discovered peer ID:
		// /ip4/<ip>/tcp/<constants.IPFSClusterSwarmPort>/p2p/<cluster-peer-id>
		peerIP := inferPeerIP(peerAddresses, vpsIP)
		if peerIP != "" {
			clusterBootstrapAddr := fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", peerIP, constants.IPFSClusterSwarmPort, ipfsClusterPeer.PeerID)
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

	if err := ps.binaryInstaller.InitializeIPFSClusterConfig(root, clusterPath, clusterSecret, constants.IPFSAPIPort, vpsIP, clusterPeers); err != nil {
		return fmt.Errorf("failed to initialize IPFS Cluster: %w", err)
	}

	// After init, save own IPFS Cluster peer ID to trusted peers file
	if err := ps.saveOwnClusterPeerID(clusterPath); err != nil {
		ps.logf("  ⚠️  Could not save IPFS Cluster peer ID to trusted peers: %v", err)
	}

	// Initialize RQLite data directory
	rqliteDataDir := filepath.Join(dataDir, "rqlite")
	if err := ps.binaryInstaller.InitializeRQLiteDataDir(root, rqliteDataDir); err != nil {
		ps.logf("  ⚠️  RQLite initialization warning: %v", err)
	}

	ps.logf("  ✓ Services initialized")
	return nil
}

// saveOwnClusterPeerID reads this node's IPFS Cluster peer ID from identity.json
// and appends it to the trusted-peers file so EnsureConfig() can use it.
func (ps *ProductionSetup) saveOwnClusterPeerID(clusterPath string) error {
	identityPath := filepath.Join(clusterPath, "identity.json")
	root := OramaRoot(ps.oramaDir)
	data, err := root.ReadFile(identityPath, rootfs.SmallFileLimit)
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
	fileData, err := root.ReadFile(trustedPeersPath, rootfs.SmallFileLimit)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to read trusted peers file: %w", err)
	}
	if err == nil {
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
	if err := root.WriteFile(trustedPeersPath, []byte(content), 0600); err != nil {
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

	// WebRTC TURN shared secret (feat-124 #913). Persisting it here lets the
	// TURN config survive Phase4 config regeneration so namespace gateways are
	// never restarted with an empty turn_secret (the AnChat outage).
	if _, err := ps.secretGenerator.EnsureTURNSecret(); err != nil {
		return fmt.Errorf("failed to ensure TURN secret: %w", err)
	}
	ps.logf("  ✓ TURN secret ensured")

	// Node identity (unified architecture)
	peerID, err := ps.EnsureNodeIdentity()
	if err != nil {
		return err
	}
	ps.logf("  ✓ Node identity ensured (Peer ID: %s)", peerID)

	return nil
}

// EnsureNodeIdentity creates the node's libp2p identity if it does not exist
// and records the peer id on the setup, returning it.
//
// It is exported because the join flow needs the identity BEFORE it asks to
// join: the peer id is what the cluster keys this machine by in every store, so
// a join that cannot name it forces the receiving node to invent a synthetic id
// that matches nothing and has to be backfilled later.
//
// Calling it early is safe and does not change what Phase 3 does: it creates
// its own directory, and reads back the identity it already wrote rather than
// generating a second one.
func (ps *ProductionSetup) EnsureNodeIdentity() (string, error) {
	peerID, err := ps.secretGenerator.EnsureNodeIdentity()
	if err != nil {
		return "", fmt.Errorf("failed to ensure node identity: %w", err)
	}
	ps.NodePeerID = peerID.String()
	return ps.NodePeerID, nil
}

// nodeConfigPath is the node.yaml Phase 4 writes (SecretGenerator.SaveConfig
// puts it under configs/).
func (ps *ProductionSetup) nodeConfigPath() string {
	return filepath.Join(ps.oramaDir, "configs", "node.yaml")
}

// Phase4GenerateConfigs generates node, gateway, and service configs
func (ps *ProductionSetup) Phase4GenerateConfigs(peerAddresses []string, vpsIP string, enableHTTPS bool, domain string, baseDomain string, joinAddress string, olricPeers ...[]string) error {
	if err := requireBaseDomain(baseDomain); err != nil {
		return fmt.Errorf("generate configs: %w", err)
	}
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
	// Index gateway is orama-namespace-gateway@index; no separate host gateway.yaml

	// Olric config:
	// - HTTP API binds to the WireGuard IP (unique per node; not localhost)
	// - Memberlist binds to WG IP for cluster communication across nodes
	// - Advertise WG IP so peers can reach this node
	// - Seed peers from join response for initial cluster formation
	var olricSeedPeers []string
	if len(olricPeers) > 0 {
		olricSeedPeers = olricPeers[0]
	}
	olricConfig, err := ps.configGenerator.GenerateOlricConfig(
		vpsIP, // HTTP API on WG IP (unique per node, avoids memberlist name conflict)
		constants.OlricHTTPPort,
		vpsIP, // Memberlist on WG IP for clustering
		constants.OlricMemberlistPort,
		"lan", // Production environment
		vpsIP, // Advertise WG IP
		olricSeedPeers,
	)
	if err != nil {
		return fmt.Errorf("failed to generate olric config: %w", err)
	}

	// Create olric config directory
	root := OramaRoot(ps.oramaDir)
	olricConfigDir := ps.oramaDir + "/configs/olric"
	if err := root.MkdirAll(olricConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create olric config directory: %w", err)
	}

	olricConfigPath := olricConfigDir + "/config.yaml"
	if err := root.WriteFile(olricConfigPath, []byte(olricConfig), 0644); err != nil {
		return fmt.Errorf("failed to save olric config: %w", err)
	}
	ps.logf("  ✓ Olric config generated")

	// Vault Guardian config
	vaultConfig := ps.configGenerator.GenerateVaultConfig(vpsIP)
	vaultConfigPath := filepath.Join(ps.oramaDir, "data", "vault", "vault.yaml")
	if err := root.WriteFile(vaultConfigPath, []byte(vaultConfig), 0644); err != nil {
		return fmt.Errorf("failed to save vault config: %w", err)
	}
	ps.logf("  ✓ Vault config generated")

	// CoreDNS serves the base domain as its authoritative zone.
	dnsZone := baseDomain

	// CoreDNS reads the index rqlite where the node.yaml just written
	// says it binds, with its credentials.
	rq, err := rqlite.EndpointFromNodeConfig(OramaRoot(ps.oramaDir), ps.nodeConfigPath())
	if err != nil {
		return fmt.Errorf("configure CoreDNS: %w", err)
	}
	// Every writer below is fatal: a nameserver whose Corefile or
	// Caddyfile was not written cannot answer for its zone or serve
	// HTTPS, and the install used to report success anyway.
	if err := ps.binaryInstaller.ConfigureCoreDNS(dnsZone, rq); err != nil {
		return fmt.Errorf("configure CoreDNS: %w", err)
	}
	ps.logf("  ✓ CoreDNS config generated (zone: %s)", dnsZone)

	// Configure Caddy (the node's own domain when it has one, else the base domain)
	caddyDomain := domain
	if caddyDomain == "" {
		caddyDomain = baseDomain
	}
	email := "admin@" + caddyDomain
	acmeEndpoint := fmt.Sprintf("http://localhost:%d/v1/internal/acme", constants.GatewayAPIPort)

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
		return fmt.Errorf("configure ntfy: %w", err)
	}
	ps.logf("  ✓ ntfy config generated (base_url: %s)", ntfyBaseURL)

	// Stealth TURN-over-443 (feat-124): when the node opted in
	// (sni_router.enabled in the node.yaml just written above), Caddy
	// must vacate :443 so the orama-sni-router can own it. Move Caddy's
	// HTTPS listener to :8443 BEFORE ConfigureCaddy renders the Caddyfile.
	// When not opted in, the Caddyfile is byte-identical to before.
	if ps.configGenerator.SNIRouterEnabled() {
		ps.binaryInstaller.EnableCaddySNIRouterMode()
		ps.logf("  ✓ SNI router enabled — Caddy HTTPS will bind :8443")
	}

	acmeCA, err := ps.configGenerator.ACMECA()
	if err != nil {
		return fmt.Errorf("configure Caddy: %w", err)
	}
	// Caddy's DNS-01 calls are signed with a key derived from the cluster
	// secret; the gateway refuses them otherwise.
	clusterSecret, err := ps.secretGenerator.EnsureClusterSecret()
	if err != nil {
		return fmt.Errorf("configure Caddy: %w", err)
	}
	if err := ps.binaryInstaller.ConfigureCaddy(caddyDomain, email, acmeEndpoint, baseDomain, acmeCA, clusterSecret); err != nil {
		return fmt.Errorf("configure Caddy: %w", err)
	}
	ps.logf("  ✓ Caddy config generated")

	// Stealth TURN-over-443 (feat-124): when opted in, write the
	// orama-sni-router config (listen :443, fallback Caddy :8443,
	// turn_discovery scanning this node's namespaces dir for the cluster's
	// base domain). orama-node starts orama-namespace-sni-router@index when
	// sni_router.enabled is set. The router uses the base domain as the zone
	// for stealth/turn.ns-* hostnames.
	if ps.configGenerator.SNIRouterEnabled() {
		if err := ps.binaryInstaller.ConfigureSNIRouter(dnsZone); err != nil {
			return fmt.Errorf("configure the SNI router: %w", err)
		}
		ps.logf("  ✓ SNI router config generated (zone: %s)", dnsZone)
	}

	return nil
}

// Phase5CreateSystemdServices writes orama-node.service, retires the
// pre-namespace host units, and enables and starts orama-node.
//
// orama-node.service is the one host unit install writes. Every daemon it
// supervises runs as an orama-namespace-*@ instance, from the templates
// InstallNamespaceTemplates copies, and the privileged helper's units come
// from EnsurePrivHelper. The per-daemon host units install used to write here
// (orama-ipfs, orama-olric, caddy, ...) were never enabled, and orama-node
// stopped and disabled them on every boot; they are deleted instead.
//
// enableHTTPS selects the SNI-aware RQLite Raft advertise port.
func (ps *ProductionSetup) Phase5CreateSystemdServices(enableHTTPS bool) error {
	ps.logf("Phase 5: Creating systemd services...")

	if err := ps.chownOramaTree(); err != nil {
		return err
	}
	if err := ps.requireServiceBinaries(); err != nil {
		return err
	}
	if err := ensureCaddyDataDir(); err != nil {
		return err
	}

	nodeUnit := ps.serviceGenerator.GenerateNodeService()
	if err := ps.serviceController.WriteServiceUnit(nodeServiceName, nodeUnit); err != nil {
		return fmt.Errorf("failed to write Node service: %w", err)
	}
	ps.logf("  ✓ Node service created: %s (supervisor)", nodeServiceName)

	if err := installers.NewLegacyHostUnitCleaner(ps.logWriter).Remove(); err != nil {
		return fmt.Errorf("retire the pre-namespace host units: %w", err)
	}

	if err := InstallLogrotateConfig(ps.oramaDir); err != nil {
		return err
	}
	ps.logf("  ✓ Log rotation configured (%s)", logrotateConfigPath)

	if err := ps.serviceController.DaemonReload(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}
	ps.logf("  ✓ Systemd daemon reloaded")

	return ps.enableAndStartNode()
}

// nodeServiceName is the supervisor's unit, the one host unit install writes.
const nodeServiceName = "orama-node.service"

// caddyDataDir is Caddy's XDG_DATA_HOME in orama-namespace-caddy@: its ACME
// account and certificates. The template lists it in ReadWritePaths, which
// systemd refuses to start the unit without.
const caddyDataDir = "/var/lib/caddy"

// chownOramaTree hands the orama tree to the orama user. Phases 2b-4 create
// files there as root (the IPFS repo, configs, secrets) that the services,
// which run as orama, must read and write.
func (ps *ProductionSetup) chownOramaTree() error {
	if out, err := exec.Command("chown", "-R", "orama:orama", ps.oramaDir).CombinedOutput(); err != nil {
		return fmt.Errorf("chown %s to the orama user the services run as: %w\n%s", ps.oramaDir, err, string(out))
	}
	if err := lockOramaBinDir(filepath.Join(ps.oramaHome, "bin")); err != nil {
		return fmt.Errorf("lock %s: %w", filepath.Join(ps.oramaHome, "bin"), err)
	}
	ps.logf("  ✓ File ownership updated for orama user; bin/ is root:orama 0750")
	return nil
}

// requireServiceBinaries fails the phase when a daemon the supervisor starts
// is missing. Phase 2b installs them, and a failed install there is otherwise
// found only when orama-node cannot start the @index instance.
func (ps *ProductionSetup) requireServiceBinaries() error {
	for _, bin := range []struct{ name, local, system string }{
		{"ipfs", "/usr/local/bin/ipfs", "/usr/bin/ipfs"},
		{"ipfs-cluster-service", "/usr/local/bin/ipfs-cluster-service", "/usr/bin/ipfs-cluster-service"},
		{"olric-server", "/usr/local/bin/olric-server", "/usr/bin/olric-server"},
	} {
		if _, err := ps.binaryInstaller.ResolveBinaryPath(bin.name, bin.local, bin.system); err != nil {
			return fmt.Errorf("%s binary not available: %w", bin.name, err)
		}
	}
	return nil
}

// ensureCaddyDataDir creates Caddy's data directory for the orama user.
func ensureCaddyDataDir() error {
	if out, err := exec.Command("mkdir", "-p", caddyDataDir).CombinedOutput(); err != nil {
		return fmt.Errorf("create %s for Caddy: %w\n%s", caddyDataDir, err, string(out))
	}
	if out, err := exec.Command("chown", "-R", "orama:orama", caddyDataDir).CombinedOutput(); err != nil {
		return fmt.Errorf("chown %s to the orama user Caddy runs as: %w\n%s", caddyDataDir, err, string(out))
	}
	return nil
}

// enableAndStartNode enables and (re)starts the supervisor.
//
// orama-node starts the orama-namespace-*@index host daemons and, on
// nameservers, orama-namespace-coredns@nameserver.
//
// The WireGuard unit is enabled alongside it, NOT left to the supervisor.
// wg0 previously existed only if Node.Start got as far as
// startIndexWireGuard, so a bad node.yaml, a failed config validation or a
// missing binary left the node with no overlay at all - unreachable on
// 10.0.0.x by every orama CLI path, and diagnosable only over public-IP SSH.
// The overlay must come up at boot on its own; the unit is idempotent
// (`wg show wg0 || wg-quick up wg0`), so the supervisor starting it again is
// a no-op.
func (ps *ProductionSetup) enableAndStartNode() error {
	for _, svc := range []string{nodeServiceName, "orama-namespace-wireguard@index.service"} {
		if err := ps.serviceController.EnableService(svc); err != nil {
			return err
		}
		ps.logf("  ✓ Service enabled: %s", svc)
	}

	// Disabled, never stopped: stopping wg-quick@wg0 runs wg-quick down and
	// drops the mesh. orama-namespace-wireguard@index brings up the same conf.
	if err := ps.serviceController.DisableService(systemd.LeftoverWireGuardUnit); err != nil {
		return err
	}
	ps.logf("  ✓ Leftover disabled: %s (interface left up)", systemd.LeftoverWireGuardUnit)

	ps.logf("  Starting orama-node (supervisor starts @index host stack and @nameserver CoreDNS)...")
	if err := ps.serviceController.RestartService(nodeServiceName); err != nil {
		return err
	}
	ps.logf("  ✓ %s started", nodeServiceName)
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
	if err := OramaRoot(ps.oramaDir).WriteFile(pubKeyPath, []byte(pubKey), 0600); err != nil {
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

	fwCfg := FirewallConfig{
		SSHPort:       22,
		IsNameserver:  ps.isNameserver,
		WireGuardPort: 51820,
	}
	// TURN relay ports (bugboard #846). This node's rule set must include the
	// relay range whenever it hosts a TURN instance, or the relay silently
	// stops forwarding (calls reach ICE "checking" but never connect).
	//
	// This used to be a re-add after `ufw --force reset` closed the ports.
	// Reconcile never resets, so the range is simply part of the desired set
	// and stays open throughout. The relay range is the full default
	// (49152-65535), a superset of every namespace's per-tenant sub-range.
	runsTURN, err := ps.hostRunsTURN()
	if err != nil {
		return fmt.Errorf("decide whether this node relays TURN, which decides whether its relay range stays open: %w", err)
	}
	if runsTURN {
		ps.logf("  TURN instance detected — opening relay ports")
		fwCfg.TURNEnabled = true
		fwCfg.TURNRelayStart = defaultTURNRelayPortStart
		fwCfg.TURNRelayEnd = defaultTURNRelayPortEnd
	}

	fp := NewFirewallProvisioner(fwCfg)

	// Reconcile, not Setup. Setup starts with `ufw --force reset`, and this
	// phase runs on every upgrade — with every service already up. Between the
	// reset and the re-enable the node is firewalled to nothing and then to
	// default-deny with no rules; that window is why the TURN relay range
	// needed a dedicated re-add to survive an upgrade.
	if err := fp.Reconcile(); err != nil {
		return fmt.Errorf("firewall reconcile failed: %w", err)
	}

	ps.logf("  ✓ UFW firewall reconciled")
	return nil
}

// hostRunsTURN reports whether this node relays TURN, so Phase 6b keeps the
// relay range open (bugboard #846).
//
// It reads the state the node is actually in. Phase 6b runs before orama-node
// restarts, and it is orama-node that moves the pre-0.200 layout into the
// current one (pkg/legacylayout), so on the one upgrade that crosses the two
// the node is still on the old layout here. It looks at:
//
//   - the current layout: the shared config data/turn/turn.yaml, which the
//     node writes while it holds a TURN allocation and deletes when it stops
//     relaying. Nothing writes a turn.env into the unit env tree any more: the
//     shared server has no per-namespace unit, and the migration deletes the
//     old env files rather than staging them;
//   - the old layout, on a node not yet migrated: configs/turn.yaml and a
//     per-namespace data/namespaces/<ns>/turn.env.
//
// It detects from files, not systemctl: the upgrade has stopped every TURN
// unit by now, and a stopped template instance can drop out of
// `systemctl list-units`. A false negative closes the relay and breaks every
// call, so a location that cannot be read is an error, never a "no".
func (ps *ProductionSetup) hostRunsTURN() (bool, error) {
	shared := constants.HostTURNConfigPath(ps.oramaDir)
	if _, err := os.Lstat(shared); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect %s: %w", shared, err)
	}
	return legacylayout.HasTURN(ps.oramaDir)
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

// LogSetupComplete logs completion information. It names orama CLI commands
// only: the CLI knows the services' dependency order and quorum, which a raw
// systemctl does not.
func (ps *ProductionSetup) LogSetupComplete(peerID string) {
	ps.logf("\n" + strings.Repeat("=", 70))
	ps.logf("Setup Complete!")
	ps.logf(strings.Repeat("=", 70))
	ps.logf("\nNode Peer ID: %s", peerID)
	ps.logf("\nOn this node:")
	ps.logf("  orama node status        # every service and whether it is running")
	ps.logf("  orama node logs node -f  # the supervisor; also gateway, rqlite, olric, ipfs, caddy, coredns")
	ps.logf("  orama node doctor        # diagnose common issues")
	ps.logf("  orama node report        # full health report as JSON\n")
}
