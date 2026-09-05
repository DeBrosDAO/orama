package installers

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// ipfsStorageMaxDiskFraction is the share of the node's TOTAL disk used as the
// Datastore.StorageMax budget. This is an ADVISORY watermark, not a hard cap:
// kubo only auto-GCs against StorageMax when the daemon runs with --enable-gc,
// which it does not here — reclaim is driven by orama-ipfs-gc.timer, which
// collects every unpinned block regardless of StorageMax. So StorageMax mainly
// (a) sizes the monitoring denominator (repo_use_pct in `orama monitor`) and
// (b) bounds growth if watermark GC is ever enabled. kubo's built-in default is
// "10GB" regardless of disk size. Nodes have heterogeneous disks (observed
// 96GB–290GB on devnet), so the budget is computed per node rather than fixed.
// 50% is conservative — it assumes IPFS is the dominant consumer while leaving
// the rest for the OS, RQLite, Olric, and logs on a shared single-disk layout.
const ipfsStorageMaxDiskFraction = 0.5

// ipfsStorageMaxFloorGB is the lower bound for the computed StorageMax, matching
// kubo's own default so we never configure a budget smaller than out-of-the-box.
const ipfsStorageMaxFloorGB = 10

// ipfsStorageMaxForDisk computes the Datastore.StorageMax string (e.g. "145GB")
// for a filesystem of totalBytes, as a fraction of total disk floored at the
// kubo default. Pure helper so the sizing policy is unit-testable without a
// real filesystem. GB are decimal (1e9), matching how kubo parses "NNGB".
func ipfsStorageMaxForDisk(totalBytes uint64) string {
	gb := uint64(float64(totalBytes) * ipfsStorageMaxDiskFraction / 1e9)
	if gb < ipfsStorageMaxFloorGB {
		gb = ipfsStorageMaxFloorGB
	}
	return fmt.Sprintf("%dGB", gb)
}

// IPFSInstaller handles IPFS (Kubo) installation
type IPFSInstaller struct {
	*BaseInstaller
	version string
}

// NewIPFSInstaller creates a new IPFS installer
func NewIPFSInstaller(arch string, logWriter io.Writer) *IPFSInstaller {
	return &IPFSInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		version:       constants.IPFSKuboVersion,
	}
}

// IsInstalled checks if IPFS is already installed
func (ii *IPFSInstaller) IsInstalled() bool {
	_, err := exec.LookPath("ipfs")
	return err == nil
}

// Install downloads and installs IPFS (Kubo)
// Follows official steps from https://docs.ipfs.tech/install/command-line/
func (ii *IPFSInstaller) Install() error {
	if ii.IsInstalled() {
		fmt.Fprintf(ii.logWriter, "  ✓ IPFS already installed\n")
		return nil
	}

	fmt.Fprintf(ii.logWriter, "  Installing IPFS (Kubo)...\n")

	// Follow official installation steps in order
	tarball := fmt.Sprintf("kubo_%s_linux-%s.tar.gz", ii.version, ii.arch)
	url := fmt.Sprintf("https://dist.ipfs.tech/kubo/%s/%s", ii.version, tarball)
	tmpDir := "/tmp"
	tarPath := filepath.Join(tmpDir, tarball)
	kuboDir := filepath.Join(tmpDir, "kubo")

	// Step 1: Download the Linux binary from dist.ipfs.tech
	fmt.Fprintf(ii.logWriter, "    Step 1: Downloading Kubo %s...\n", ii.version)
	if err := DownloadFile(url, tarPath); err != nil {
		return fmt.Errorf("failed to download kubo from %s: %w", url, err)
	}

	// Verify tarball exists
	if _, err := os.Stat(tarPath); err != nil {
		return fmt.Errorf("kubo tarball not found after download at %s: %w", tarPath, err)
	}

	// Step 2: Unzip the file
	fmt.Fprintf(ii.logWriter, "    Step 2: Extracting Kubo archive...\n")
	if err := ExtractTarball(tarPath, tmpDir); err != nil {
		return fmt.Errorf("failed to extract kubo tarball: %w", err)
	}

	// Verify extraction
	if _, err := os.Stat(kuboDir); err != nil {
		return fmt.Errorf("kubo directory not found after extraction at %s: %w", kuboDir, err)
	}

	// Step 3: Move into the kubo folder (cd kubo)
	fmt.Fprintf(ii.logWriter, "    Step 3: Running installation script...\n")

	// Step 4: Run the installation script (sudo bash install.sh)
	installScript := filepath.Join(kuboDir, "install.sh")
	if _, err := os.Stat(installScript); err != nil {
		return fmt.Errorf("install.sh not found in extracted kubo directory at %s: %w", installScript, err)
	}

	cmd := exec.Command("bash", installScript)
	cmd.Dir = kuboDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to run install.sh: %v\n%s", err, string(output))
	}

	// Step 5: Test that Kubo has installed correctly
	fmt.Fprintf(ii.logWriter, "    Step 5: Verifying installation...\n")
	cmd = exec.Command("ipfs", "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// ipfs might not be in PATH yet in this process, check file directly
		ipfsLocations := []string{"/usr/local/bin/ipfs", "/usr/bin/ipfs"}
		found := false
		for _, loc := range ipfsLocations {
			if info, err := os.Stat(loc); err == nil && !info.IsDir() {
				found = true
				// Ensure it's executable
				if info.Mode()&0111 == 0 {
					if err := os.Chmod(loc, 0755); err != nil {
						return fmt.Errorf("failed to make ipfs executable at %s: %w", loc, err)
					}
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("ipfs binary not found after installation in %v", ipfsLocations)
		}
	} else {
		fmt.Fprintf(ii.logWriter, "      %s", string(output))
	}

	// Ensure PATH is updated for current process
	os.Setenv("PATH", os.Getenv("PATH")+":/usr/local/bin")

	fmt.Fprintf(ii.logWriter, "  ✓ IPFS installed successfully\n")
	return nil
}

// Configure is a placeholder for IPFS configuration
func (ii *IPFSInstaller) Configure() error {
	// Configuration is handled by InitializeRepo
	return nil
}

// InitializeRepo initializes an IPFS repository for a node (unified - no bootstrap/node distinction)
// If ipfsPeer is provided, configures Peering.Peers for peer discovery in private networks
func (ii *IPFSInstaller) InitializeRepo(ipfsRepoPath string, swarmKeyPath string, apiPort, gatewayPort, swarmPort int, bindIP string, ipfsPeer *IPFSPeerInfo) error {
	configPath := filepath.Join(ipfsRepoPath, "config")
	repoExists := false
	if _, err := os.Stat(configPath); err == nil {
		repoExists = true
		fmt.Fprintf(ii.logWriter, "    IPFS repo already exists, ensuring configuration...\n")
	} else {
		fmt.Fprintf(ii.logWriter, "    Initializing IPFS repo...\n")
	}

	if err := os.MkdirAll(ipfsRepoPath, 0755); err != nil {
		return fmt.Errorf("failed to create IPFS repo directory: %w", err)
	}

	// Resolve IPFS binary path
	ipfsBinary, err := ResolveBinaryPath("ipfs", "/usr/local/bin/ipfs", "/usr/bin/ipfs")
	if err != nil {
		return err
	}

	// Initialize IPFS if repo doesn't exist
	if !repoExists {
		cmd := exec.Command(ipfsBinary, "init", "--profile=server", "--repo-dir="+ipfsRepoPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to initialize IPFS: %v\n%s", err, string(output))
		}
	}

	// Copy swarm key if present
	swarmKeyExists := false
	if data, err := os.ReadFile(swarmKeyPath); err == nil {
		swarmKeyDest := filepath.Join(ipfsRepoPath, "swarm.key")
		if err := os.WriteFile(swarmKeyDest, data, 0600); err != nil {
			return fmt.Errorf("failed to copy swarm key: %w", err)
		}
		swarmKeyExists = true
	}

	// Configure IPFS addresses (API, Gateway, Swarm) by modifying the config file directly
	// This ensures the ports are set correctly and avoids conflicts with RQLite
	fmt.Fprintf(ii.logWriter, "    Configuring IPFS addresses (API: %d, Gateway: %d, Swarm: %d)...\n", apiPort, gatewayPort, swarmPort)
	if err := ii.configureAddresses(ipfsRepoPath, apiPort, gatewayPort, swarmPort, bindIP); err != nil {
		return fmt.Errorf("failed to configure IPFS addresses: %w", err)
	}

	// Set a disk-aware Datastore.StorageMax so GC has a real budget to reclaim
	// against (kubo's default is an unenforced 10GB). Reclaim itself is driven by
	// the orama-ipfs-gc.timer; the daemon runs without in-process GC.
	if err := ii.configureDatastore(ipfsRepoPath); err != nil {
		return fmt.Errorf("failed to configure IPFS datastore: %w", err)
	}

	// Always disable AutoConf for private swarm when swarm.key is present
	// This is critical - IPFS will fail to start if AutoConf is enabled on a private network
	// We do this even for existing repos to fix repos initialized before this fix was applied
	if swarmKeyExists {
		fmt.Fprintf(ii.logWriter, "    Disabling AutoConf for private swarm...\n")
		cmd := exec.Command(ipfsBinary, "config", "--json", "AutoConf.Enabled", "false")
		cmd.Env = append(os.Environ(), "IPFS_PATH="+ipfsRepoPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to disable AutoConf: %v\n%s", err, string(output))
		}

		// Clear AutoConf placeholders from config to prevent Kubo startup errors
		// When AutoConf is disabled, 'auto' placeholders must be replaced with explicit values or empty
		fmt.Fprintf(ii.logWriter, "    Clearing AutoConf placeholders from IPFS config...\n")

		type configCommand struct {
			desc string
			args []string
		}

		// List of config replacements to clear 'auto' placeholders
		cleanup := []configCommand{
			{"clearing Bootstrap peers", []string{"config", "Bootstrap", "--json", "[]"}},
			{"clearing Routing.DelegatedRouters", []string{"config", "Routing.DelegatedRouters", "--json", "[]"}},
			{"clearing Ipns.DelegatedPublishers", []string{"config", "Ipns.DelegatedPublishers", "--json", "[]"}},
			{"clearing DNS.Resolvers", []string{"config", "DNS.Resolvers", "--json", "{}"}},
		}

		for _, step := range cleanup {
			fmt.Fprintf(ii.logWriter, "      %s...\n", step.desc)
			cmd := exec.Command(ipfsBinary, step.args...)
			cmd.Env = append(os.Environ(), "IPFS_PATH="+ipfsRepoPath)
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed while %s: %v\n%s", step.desc, err, string(output))
			}
		}

		// Configure Peering.Peers if we have peer info (for private network discovery)
		if ipfsPeer != nil && ipfsPeer.PeerID != "" && len(ipfsPeer.Addrs) > 0 {
			fmt.Fprintf(ii.logWriter, "    Configuring Peering.Peers for private network discovery...\n")
			if err := ii.configurePeering(ipfsRepoPath, ipfsPeer); err != nil {
				return fmt.Errorf("failed to configure IPFS peering: %w", err)
			}
		}
	}

	return nil
}

// configureAddresses configures the IPFS API, Gateway, and Swarm addresses in the config file
func (ii *IPFSInstaller) configureAddresses(ipfsRepoPath string, apiPort, gatewayPort, swarmPort int, bindIP string) error {
	configPath := filepath.Join(ipfsRepoPath, "config")

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read IPFS config: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse IPFS config: %w", err)
	}

	// Get existing Addresses section or create new one
	// This preserves any existing settings like Announce, AppendAnnounce, NoAnnounce
	addresses, ok := config["Addresses"].(map[string]interface{})
	if !ok {
		addresses = make(map[string]interface{})
	}

	// Update specific address fields while preserving others
	// Bind API and Gateway to localhost only for security
	// Swarm binds to the WireGuard IP so it's only reachable over the VPN
	addresses["API"] = []string{
		fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", apiPort),
	}
	addresses["Gateway"] = []string{
		fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", gatewayPort),
	}
	addresses["Swarm"] = []string{
		fmt.Sprintf("/ip4/%s/tcp/%d", bindIP, swarmPort),
	}
	// Clear NoAnnounce — the server profile blocks private IPs (10.0.0.0/8, etc.)
	// which prevents nodes from advertising their WireGuard swarm addresses via DHT
	addresses["NoAnnounce"] = []string{}

	config["Addresses"] = addresses

	// Clear Swarm.AddrFilters — the server profile blocks private IPs (10.0.0.0/8, 172.16.0.0/12, etc.)
	// which prevents IPFS from connecting over our WireGuard mesh (10.0.0.x)
	swarm, ok := config["Swarm"].(map[string]interface{})
	if !ok {
		swarm = make(map[string]interface{})
	}
	swarm["AddrFilters"] = []interface{}{}
	// Disable Websocket transport (not supported in private networks)
	transports, _ := swarm["Transports"].(map[string]interface{})
	if transports == nil {
		transports = make(map[string]interface{})
	}
	network, _ := transports["Network"].(map[string]interface{})
	if network == nil {
		network = make(map[string]interface{})
	}
	network["Websocket"] = false
	transports["Network"] = network
	swarm["Transports"] = transports
	config["Swarm"] = swarm

	// Disable AutoTLS (incompatible with private networks)
	autoTLS := map[string]interface{}{"Enabled": false}
	config["AutoTLS"] = autoTLS

	// Use DHT routing (Routing.Type=auto is incompatible with private networks)
	config["Routing"] = map[string]interface{}{"Type": "dht"}

	// Write config back
	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal IPFS config: %w", err)
	}

	if err := os.WriteFile(configPath, updatedData, 0600); err != nil {
		return fmt.Errorf("failed to write IPFS config: %w", err)
	}

	return nil
}

// setDatastoreStorageMax returns the IPFS config JSON with Datastore.StorageMax
// set to storageMax, preserving every other field (including other Datastore
// keys such as GCPeriod / StorageGCWatermark). Pure helper for testability.
func setDatastoreStorageMax(configData []byte, storageMax string) ([]byte, error) {
	var config map[string]interface{}
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse IPFS config: %w", err)
	}

	datastore, ok := config["Datastore"].(map[string]interface{})
	if !ok {
		datastore = make(map[string]interface{})
	}
	datastore["StorageMax"] = storageMax
	config["Datastore"] = datastore

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal IPFS config: %w", err)
	}
	return out, nil
}

// configureDatastore sets Datastore.StorageMax to a disk-aware budget so IPFS GC
// has a real target to reclaim against (kubo's default is an unenforced 10GB
// regardless of disk size). Idempotent: runs on every install/upgrade via
// InitializeRepo. Preserves all other config fields.
func (ii *IPFSInstaller) configureDatastore(ipfsRepoPath string) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(ipfsRepoPath, &stat); err != nil {
		return fmt.Errorf("failed to stat filesystem for IPFS repo %s: %w", ipfsRepoPath, err)
	}
	totalBytes := stat.Blocks * uint64(stat.Bsize)
	storageMax := ipfsStorageMaxForDisk(totalBytes)

	configPath := filepath.Join(ipfsRepoPath, "config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read IPFS config: %w", err)
	}
	updated, err := setDatastoreStorageMax(data, storageMax)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, updated, 0600); err != nil {
		return fmt.Errorf("failed to write IPFS config: %w", err)
	}

	fmt.Fprintf(ii.logWriter, "    IPFS Datastore.StorageMax set to %s (%.0f%% of %dGB disk)...\n",
		storageMax, ipfsStorageMaxDiskFraction*100, totalBytes/1_000_000_000)
	return nil
}

// configurePeering configures Peering.Peers in the IPFS config for private network discovery
// This allows nodes in a private swarm to find each other even without bootstrap peers
func (ii *IPFSInstaller) configurePeering(ipfsRepoPath string, peer *IPFSPeerInfo) error {
	configPath := filepath.Join(ipfsRepoPath, "config")

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read IPFS config: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse IPFS config: %w", err)
	}

	// Get existing Peering section or create new one
	peering, ok := config["Peering"].(map[string]interface{})
	if !ok {
		peering = make(map[string]interface{})
	}

	// Create peer entry
	peerEntry := map[string]interface{}{
		"ID":    peer.PeerID,
		"Addrs": peer.Addrs,
	}

	// Set Peering.Peers
	peering["Peers"] = []interface{}{peerEntry}
	config["Peering"] = peering

	fmt.Fprintf(ii.logWriter, "      Adding peer: %s (%d addresses)\n", peer.PeerID, len(peer.Addrs))

	// Write config back
	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal IPFS config: %w", err)
	}

	if err := os.WriteFile(configPath, updatedData, 0600); err != nil {
		return fmt.Errorf("failed to write IPFS config: %w", err)
	}

	return nil
}
