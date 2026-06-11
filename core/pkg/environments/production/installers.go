package production

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/DeBrosOfficial/network/pkg/environments/production/installers"
)

// BinaryInstaller handles downloading and installing external binaries
// This is a backward-compatible wrapper around the new installers package
type BinaryInstaller struct {
	arch      string
	logWriter io.Writer
	oramaHome string

	// Embedded installers
	rqlite      *installers.RQLiteInstaller
	ipfs        *installers.IPFSInstaller
	ipfsCluster *installers.IPFSClusterInstaller
	olric       *installers.OlricInstaller
	gateway     *installers.GatewayInstaller
	coredns     *installers.CoreDNSInstaller
	caddy       *installers.CaddyInstaller
	ntfy        *installers.NtfyInstaller      // feature #72; installed only when EnableNtfy is set
	sniRouter   *installers.SNIRouterInstaller // feat-124; configured only when sni_router.enabled
}

// NewBinaryInstaller creates a new binary installer
func NewBinaryInstaller(arch string, logWriter io.Writer) *BinaryInstaller {
	oramaHome := OramaBase
	return &BinaryInstaller{
		arch:        arch,
		logWriter:   logWriter,
		oramaHome:   oramaHome,
		rqlite:      installers.NewRQLiteInstaller(arch, logWriter),
		ipfs:        installers.NewIPFSInstaller(arch, logWriter),
		ipfsCluster: installers.NewIPFSClusterInstaller(arch, logWriter),
		olric:       installers.NewOlricInstaller(arch, logWriter),
		gateway:     installers.NewGatewayInstaller(arch, logWriter),
		coredns:     installers.NewCoreDNSInstaller(arch, logWriter, oramaHome),
		caddy:       installers.NewCaddyInstaller(arch, logWriter, oramaHome),
		ntfy:        installers.NewNtfyInstaller(arch, logWriter),
		sniRouter:   installers.NewSNIRouterInstaller(arch, logWriter, OramaDir),
	}
}

// InstallRQLite downloads and installs RQLite
func (bi *BinaryInstaller) InstallRQLite() error {
	return bi.rqlite.Install()
}

// InstallIPFS downloads and installs IPFS (Kubo)
func (bi *BinaryInstaller) InstallIPFS() error {
	return bi.ipfs.Install()
}

// InstallIPFSCluster downloads and installs IPFS Cluster Service
func (bi *BinaryInstaller) InstallIPFSCluster() error {
	return bi.ipfsCluster.Install()
}

// InstallOlric downloads and installs Olric server
func (bi *BinaryInstaller) InstallOlric() error {
	return bi.olric.Install()
}

// InstallGo downloads and installs Go toolchain
func (bi *BinaryInstaller) InstallGo() error {
	return bi.gateway.InstallGo()
}

// ResolveBinaryPath finds the fully-qualified path to a required executable
func (bi *BinaryInstaller) ResolveBinaryPath(binary string, extraPaths ...string) (string, error) {
	return installers.ResolveBinaryPath(binary, extraPaths...)
}

// InstallDeBrosBinaries builds Orama binaries from source
func (bi *BinaryInstaller) InstallDeBrosBinaries(oramaHome string) error {
	return bi.gateway.InstallDeBrosBinaries(oramaHome)
}

// InstallSystemDependencies installs system-level dependencies via apt
func (bi *BinaryInstaller) InstallSystemDependencies() error {
	return bi.gateway.InstallSystemDependencies()
}

// IPFSPeerInfo holds IPFS peer information for configuring Peering.Peers
type IPFSPeerInfo = installers.IPFSPeerInfo

// IPFSClusterPeerInfo contains IPFS Cluster peer information for cluster peer discovery
type IPFSClusterPeerInfo = installers.IPFSClusterPeerInfo

// InitializeIPFSRepo initializes an IPFS repository for a node (unified - no bootstrap/node distinction)
// If ipfsPeer is provided, configures Peering.Peers for peer discovery in private networks
func (bi *BinaryInstaller) InitializeIPFSRepo(ipfsRepoPath string, swarmKeyPath string, apiPort, gatewayPort, swarmPort int, bindIP string, ipfsPeer *IPFSPeerInfo) error {
	return bi.ipfs.InitializeRepo(ipfsRepoPath, swarmKeyPath, apiPort, gatewayPort, swarmPort, bindIP, ipfsPeer)
}

// InitializeIPFSClusterConfig initializes IPFS Cluster configuration (unified - no bootstrap/node distinction)
// This runs `ipfs-cluster-service init` to create the service.json configuration file.
// For existing installations, it ensures the cluster secret is up to date.
// clusterPeers should be in format: ["/ip4/<ip>/tcp/9098/p2p/<cluster-peer-id>"]
func (bi *BinaryInstaller) InitializeIPFSClusterConfig(clusterPath, clusterSecret string, ipfsAPIPort int, clusterPeers []string) error {
	return bi.ipfsCluster.InitializeConfig(clusterPath, clusterSecret, ipfsAPIPort, clusterPeers)
}

// GetClusterPeerMultiaddr reads the IPFS Cluster peer ID and returns its multiaddress
// Returns format: /ip4/<ip>/tcp/9098/p2p/<cluster-peer-id>
func (bi *BinaryInstaller) GetClusterPeerMultiaddr(clusterPath string, nodeIP string) (string, error) {
	return bi.ipfsCluster.GetClusterPeerMultiaddr(clusterPath, nodeIP)
}

// InitializeRQLiteDataDir initializes RQLite data directory
func (bi *BinaryInstaller) InitializeRQLiteDataDir(dataDir string) error {
	return bi.rqlite.InitializeDataDir(dataDir)
}

// InstallAnyoneClient installs the anyone-client npm package globally
func (bi *BinaryInstaller) InstallAnyoneClient() error {
	return bi.gateway.InstallAnyoneClient()
}

// InstallCoreDNS builds and installs CoreDNS with the custom RQLite plugin.
// Also disables systemd-resolved's stub listener so CoreDNS can bind to port 53.
func (bi *BinaryInstaller) InstallCoreDNS() error {
	if err := bi.coredns.DisableResolvedStubListener(); err != nil {
		fmt.Fprintf(bi.logWriter, "  ⚠️  Failed to disable systemd-resolved stub: %v\n", err)
	}
	return bi.coredns.Install()
}

// ConfigureCoreDNS creates CoreDNS configuration files
func (bi *BinaryInstaller) ConfigureCoreDNS(domain string, rqliteDSN string, ns1IP, ns2IP, ns3IP string) error {
	return bi.coredns.Configure(domain, rqliteDSN, ns1IP, ns2IP, ns3IP)
}

// SeedDNS seeds static DNS records into RQLite. Call after RQLite is running.
func (bi *BinaryInstaller) SeedDNS(domain string, rqliteDSN string, ns1IP, ns2IP, ns3IP string) error {
	return bi.coredns.SeedDNS(domain, rqliteDSN, ns1IP, ns2IP, ns3IP)
}

// InstallCaddy builds and installs Caddy with the custom orama DNS module
func (bi *BinaryInstaller) InstallCaddy() error {
	return bi.caddy.Install()
}

// ConfigureCaddy creates Caddy configuration files
func (bi *BinaryInstaller) ConfigureCaddy(domain string, email string, acmeEndpoint string, baseDomain string) error {
	return bi.caddy.Configure(domain, email, acmeEndpoint, baseDomain)
}

// EnableCaddyNtfyProxy tells the Caddy installer to emit a reverse-
// proxy block for `hostname` → localhost:<NtfyListenPort> on the next
// ConfigureCaddy() call. Used together with InstallNtfy /
// ConfigureNtfy when this node hosts the self-hosted ntfy server
// (feature #72).
func (bi *BinaryInstaller) EnableCaddyNtfyProxy(hostname string) {
	bi.caddy.EnableNtfyProxy(hostname)
}

// EnableCaddySNIRouterMode moves Caddy's HTTPS listener off :443 to :8443 on
// the next ConfigureCaddy() call, freeing :443 for the orama-sni-router
// (feat-124). Must be called BEFORE ConfigureCaddy.
func (bi *BinaryInstaller) EnableCaddySNIRouterMode() {
	bi.caddy.EnableSNIRouterMode()
}

// ConfigureSNIRouter writes the orama-sni-router YAML config (listen :443,
// fallback Caddy on :8443, turn_discovery for baseDomain). Feat-124.
func (bi *BinaryInstaller) ConfigureSNIRouter(baseDomain string) error {
	return bi.sniRouter.Configure(baseDomain)
}

// WriteSNIRouterUnit writes /etc/systemd/system/orama-sni-router.service.
func (bi *BinaryInstaller) WriteSNIRouterUnit() error {
	return bi.sniRouter.WriteSystemdUnit()
}

// SNIRouterServiceName returns the systemd unit name for lifecycle calls.
func (bi *BinaryInstaller) SNIRouterServiceName() string {
	return installers.SNIRouterServiceName
}

// InstallNtfy installs the self-hosted ntfy server (binary, user,
// systemd unit, data directory). Feature #72. Idempotent.
func (bi *BinaryInstaller) InstallNtfy() error {
	return bi.ntfy.Install()
}

// ConfigureNtfy writes /etc/ntfy/server.yml with the given public base
// URL (e.g. "https://push.dbrs.space"). Feature #72.
func (bi *BinaryInstaller) ConfigureNtfy(publicBaseURL string) error {
	return bi.ntfy.Configure(publicBaseURL)
}

// Mock system commands for testing (if needed)
var execCommand = exec.Command

// SetExecCommand allows mocking exec.Command in tests
func SetExecCommand(cmd func(name string, arg ...string) *exec.Cmd) {
	execCommand = cmd
}

// ResetExecCommand resets exec.Command to the default
func ResetExecCommand() {
	execCommand = exec.Command
}
