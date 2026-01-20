package installers

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// IPFSInstaller handles IPFS (Kubo) installation
type IPFSInstaller struct {
	*BaseInstaller
	version string
}

// NewIPFSInstaller creates a new IPFS installer
func NewIPFSInstaller(arch string, logWriter io.Writer) *IPFSInstaller {
	return &IPFSInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		version:       "v0.38.2",
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
					os.Chmod(loc, 0755)
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
func (ii *IPFSInstaller) InitializeRepo(ipfsRepoPath string, swarmKeyPath string, apiPort, gatewayPort, swarmPort int, ipfsPeer *IPFSPeerInfo) error {
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
	// This ensures the ports are set correctly and avoids conflicts with RQLite on port 5001
	fmt.Fprintf(ii.logWriter, "    Configuring IPFS addresses (API: %d, Gateway: %d, Swarm: %d)...\n", apiPort, gatewayPort, swarmPort)
	if err := ii.configureAddresses(ipfsRepoPath, apiPort, gatewayPort, swarmPort); err != nil {
		return fmt.Errorf("failed to configure IPFS addresses: %w", err)
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

	// Fix ownership (best-effort, don't fail if it doesn't work)
	if err := exec.Command("chown", "-R", "debros:debros", ipfsRepoPath).Run(); err != nil {
		fmt.Fprintf(ii.logWriter, "    ⚠️  Warning: failed to chown IPFS repo: %v\n", err)
	}

	return nil
}

// configureAddresses configures the IPFS API, Gateway, and Swarm addresses in the config file
func (ii *IPFSInstaller) configureAddresses(ipfsRepoPath string, apiPort, gatewayPort, swarmPort int) error {
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
	// Swarm binds to all interfaces for peer connections
	addresses["API"] = []string{
		fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", apiPort),
	}
	addresses["Gateway"] = []string{
		fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", gatewayPort),
	}
	addresses["Swarm"] = []string{
		fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", swarmPort),
		fmt.Sprintf("/ip6/::/tcp/%d", swarmPort),
	}

	config["Addresses"] = addresses

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
