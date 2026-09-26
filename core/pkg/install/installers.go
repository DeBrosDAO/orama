package install

import (
	"io"

	"github.com/DeBrosOfficial/network/pkg/install/installers"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
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
		coredns:     installers.NewCoreDNSInstaller(arch, logWriter, oramaHome),
		caddy:       installers.NewCaddyInstaller(arch, logWriter, oramaHome),
		ntfy:        installers.NewNtfyInstaller(arch, logWriter),
		sniRouter:   installers.NewSNIRouterInstaller(arch, logWriter, OramaDir),
	}
}

// ResolveBinaryPath finds the fully-qualified path to a required executable
func (bi *BinaryInstaller) ResolveBinaryPath(binary string, extraPaths ...string) (string, error) {
	return installers.ResolveBinaryPath(binary, extraPaths...)
}

// IPFSPeerInfo holds IPFS peer information for configuring Peering.Peers
type IPFSPeerInfo = installers.IPFSPeerInfo

// IPFSClusterPeerInfo contains IPFS Cluster peer information for cluster peer discovery
type IPFSClusterPeerInfo = installers.IPFSClusterPeerInfo

// InitializeIPFSRepo initializes an IPFS repository for a node (unified - no bootstrap/node distinction)
// If ipfsPeer is provided, configures Peering.Peers for peer discovery in private networks.
// root is the anchor the repo lives under (rootfs).
func (bi *BinaryInstaller) InitializeIPFSRepo(root rootfs.Root, ipfsRepoPath string, swarmKeyPath string, apiPort, gatewayPort, swarmPort int, bindIP string, ipfsPeer *IPFSPeerInfo) error {
	return bi.ipfs.InitializeRepo(root, ipfsRepoPath, swarmKeyPath, apiPort, gatewayPort, swarmPort, bindIP, ipfsPeer)
}

// InitializeIPFSClusterConfig initializes IPFS Cluster configuration (unified - no bootstrap/node distinction)
// This runs `ipfs-cluster-service init` to create the service.json configuration file.
// For existing installations, it ensures the cluster secret is up to date.
// clusterPeers should be in format: ["/ip4/<ip>/tcp/9098/p2p/<cluster-peer-id>"]
func (bi *BinaryInstaller) InitializeIPFSClusterConfig(root rootfs.Root, clusterPath, clusterSecret string, ipfsAPIPort int, swarmIP string, clusterPeers []string) error {
	return bi.ipfsCluster.InitializeConfig(root, clusterPath, clusterSecret, ipfsAPIPort, swarmIP, clusterPeers)
}

// InitializeRQLiteDataDir initializes RQLite data directory
func (bi *BinaryInstaller) InitializeRQLiteDataDir(root rootfs.Root, dataDir string) error {
	return bi.rqlite.InitializeDataDir(root, dataDir)
}

// ConfigureCoreDNS creates CoreDNS configuration files
func (bi *BinaryInstaller) ConfigureCoreDNS(domain string, rq rqlite.Endpoint) error {
	return bi.coredns.Configure(domain, rq)
}

// ConfigureCaddy creates Caddy configuration files
func (bi *BinaryInstaller) ConfigureCaddy(domain string, email string, acmeEndpoint string, baseDomain string, acmeCA string, clusterSecret string) error {
	return bi.caddy.Configure(domain, email, acmeEndpoint, baseDomain, acmeCA, clusterSecret)
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

// InstallNtfy installs the self-hosted ntfy server (binary, user, data
// directory). Feature #72. Idempotent.
func (bi *BinaryInstaller) InstallNtfy() error {
	return bi.ntfy.Install()
}

// ConfigureNtfy writes /etc/ntfy/server.yml with the given public base
// URL (e.g. "https://push.example.com"). Feature #72.
func (bi *BinaryInstaller) ConfigureNtfy(publicBaseURL string) error {
	return bi.ntfy.Configure(publicBaseURL)
}
