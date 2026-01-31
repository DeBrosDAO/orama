package production

import (
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
}

// NewBinaryInstaller creates a new binary installer
func NewBinaryInstaller(arch string, logWriter io.Writer) *BinaryInstaller {
	oramaHome := "/home/debros"
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

// InstallDeBrosBinaries clones and builds DeBros binaries
func (bi *BinaryInstaller) InstallDeBrosBinaries(branch string, oramaHome string, skipRepoUpdate bool) error {
	return bi.gateway.InstallDeBrosBinaries(branch, oramaHome, skipRepoUpdate)
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
func (bi *BinaryInstaller) InitializeIPFSRepo(ipfsRepoPath string, swarmKeyPath string, apiPort, gatewayPort, swarmPort int, ipfsPeer *IPFSPeerInfo) error {
	return bi.ipfs.InitializeRepo(ipfsRepoPath, swarmKeyPath, apiPort, gatewayPort, swarmPort, ipfsPeer)
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

// InstallCoreDNS builds and installs CoreDNS with the custom RQLite plugin
func (bi *BinaryInstaller) InstallCoreDNS() error {
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
