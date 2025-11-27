package production

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BinaryInstaller handles downloading and installing external binaries
type BinaryInstaller struct {
	arch      string
	logWriter io.Writer
}

// NewBinaryInstaller creates a new binary installer
func NewBinaryInstaller(arch string, logWriter io.Writer) *BinaryInstaller {
	return &BinaryInstaller{
		arch:      arch,
		logWriter: logWriter,
	}
}

// InstallRQLite downloads and installs RQLite
func (bi *BinaryInstaller) InstallRQLite() error {
	if _, err := exec.LookPath("rqlited"); err == nil {
		fmt.Fprintf(bi.logWriter, "  ✓ RQLite already installed\n")
		return nil
	}

	fmt.Fprintf(bi.logWriter, "  Installing RQLite...\n")

	version := "8.43.0"
	tarball := fmt.Sprintf("rqlite-v%s-linux-%s.tar.gz", version, bi.arch)
	url := fmt.Sprintf("https://github.com/rqlite/rqlite/releases/download/v%s/%s", version, tarball)

	// Download
	cmd := exec.Command("wget", "-q", url, "-O", "/tmp/"+tarball)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to download RQLite: %w", err)
	}

	// Extract
	cmd = exec.Command("tar", "-C", "/tmp", "-xzf", "/tmp/"+tarball)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract RQLite: %w", err)
	}

	// Copy binaries
	dir := fmt.Sprintf("/tmp/rqlite-v%s-linux-%s", version, bi.arch)
	if err := exec.Command("cp", dir+"/rqlited", "/usr/local/bin/").Run(); err != nil {
		return fmt.Errorf("failed to copy rqlited binary: %w", err)
	}
	if err := exec.Command("chmod", "+x", "/usr/local/bin/rqlited").Run(); err != nil {
		fmt.Fprintf(bi.logWriter, "    ⚠️  Warning: failed to chmod rqlited: %v\n", err)
	}

	// Ensure PATH includes /usr/local/bin
	os.Setenv("PATH", os.Getenv("PATH")+":/usr/local/bin")

	fmt.Fprintf(bi.logWriter, "  ✓ RQLite installed\n")
	return nil
}

// InstallIPFS downloads and installs IPFS (Kubo)
// Follows official steps from https://docs.ipfs.tech/install/command-line/
func (bi *BinaryInstaller) InstallIPFS() error {
	if _, err := exec.LookPath("ipfs"); err == nil {
		fmt.Fprintf(bi.logWriter, "  ✓ IPFS already installed\n")
		return nil
	}

	fmt.Fprintf(bi.logWriter, "  Installing IPFS (Kubo)...\n")

	// Follow official installation steps in order
	kuboVersion := "v0.38.2"
	tarball := fmt.Sprintf("kubo_%s_linux-%s.tar.gz", kuboVersion, bi.arch)
	url := fmt.Sprintf("https://dist.ipfs.tech/kubo/%s/%s", kuboVersion, tarball)
	tmpDir := "/tmp"
	tarPath := filepath.Join(tmpDir, tarball)
	kuboDir := filepath.Join(tmpDir, "kubo")

	// Step 1: Download the Linux binary from dist.ipfs.tech
	fmt.Fprintf(bi.logWriter, "    Step 1: Downloading Kubo v%s...\n", kuboVersion)
	cmd := exec.Command("wget", "-q", url, "-O", tarPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to download kubo from %s: %w", url, err)
	}

	// Verify tarball exists
	if _, err := os.Stat(tarPath); err != nil {
		return fmt.Errorf("kubo tarball not found after download at %s: %w", tarPath, err)
	}

	// Step 2: Unzip the file
	fmt.Fprintf(bi.logWriter, "    Step 2: Extracting Kubo archive...\n")
	cmd = exec.Command("tar", "-xzf", tarPath, "-C", tmpDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract kubo tarball: %w", err)
	}

	// Verify extraction
	if _, err := os.Stat(kuboDir); err != nil {
		return fmt.Errorf("kubo directory not found after extraction at %s: %w", kuboDir, err)
	}

	// Step 3: Move into the kubo folder (cd kubo)
	fmt.Fprintf(bi.logWriter, "    Step 3: Running installation script...\n")

	// Step 4: Run the installation script (sudo bash install.sh)
	installScript := filepath.Join(kuboDir, "install.sh")
	if _, err := os.Stat(installScript); err != nil {
		return fmt.Errorf("install.sh not found in extracted kubo directory at %s: %w", installScript, err)
	}

	cmd = exec.Command("bash", installScript)
	cmd.Dir = kuboDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to run install.sh: %v\n%s", err, string(output))
	}

	// Step 5: Test that Kubo has installed correctly
	fmt.Fprintf(bi.logWriter, "    Step 5: Verifying installation...\n")
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
		fmt.Fprintf(bi.logWriter, "      %s", string(output))
	}

	// Ensure PATH is updated for current process
	os.Setenv("PATH", os.Getenv("PATH")+":/usr/local/bin")

	fmt.Fprintf(bi.logWriter, "  ✓ IPFS installed successfully\n")
	return nil
}

// InstallIPFSCluster downloads and installs IPFS Cluster Service
func (bi *BinaryInstaller) InstallIPFSCluster() error {
	if _, err := exec.LookPath("ipfs-cluster-service"); err == nil {
		fmt.Fprintf(bi.logWriter, "  ✓ IPFS Cluster already installed\n")
		return nil
	}

	fmt.Fprintf(bi.logWriter, "  Installing IPFS Cluster Service...\n")

	// Check if Go is available
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go not found - required to install IPFS Cluster. Please install Go first")
	}

	cmd := exec.Command("go", "install", "github.com/ipfs-cluster/ipfs-cluster/cmd/ipfs-cluster-service@latest")
	cmd.Env = append(os.Environ(), "GOBIN=/usr/local/bin")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install IPFS Cluster: %w", err)
	}

	fmt.Fprintf(bi.logWriter, "  ✓ IPFS Cluster installed\n")
	return nil
}

// InstallOlric downloads and installs Olric server
func (bi *BinaryInstaller) InstallOlric() error {
	if _, err := exec.LookPath("olric-server"); err == nil {
		fmt.Fprintf(bi.logWriter, "  ✓ Olric already installed\n")
		return nil
	}

	fmt.Fprintf(bi.logWriter, "  Installing Olric...\n")

	// Check if Go is available
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go not found - required to install Olric. Please install Go first")
	}

	cmd := exec.Command("go", "install", "github.com/olric-data/olric/cmd/olric-server@v0.7.0")
	cmd.Env = append(os.Environ(), "GOBIN=/usr/local/bin")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install Olric: %w", err)
	}

	fmt.Fprintf(bi.logWriter, "  ✓ Olric installed\n")
	return nil
}

// InstallGo downloads and installs Go toolchain
func (bi *BinaryInstaller) InstallGo() error {
	if _, err := exec.LookPath("go"); err == nil {
		fmt.Fprintf(bi.logWriter, "  ✓ Go already installed\n")
		return nil
	}

	fmt.Fprintf(bi.logWriter, "  Installing Go...\n")

	goTarball := fmt.Sprintf("go1.22.5.linux-%s.tar.gz", bi.arch)
	goURL := fmt.Sprintf("https://go.dev/dl/%s", goTarball)

	// Download
	cmd := exec.Command("wget", "-q", goURL, "-O", "/tmp/"+goTarball)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to download Go: %w", err)
	}

	// Extract
	cmd = exec.Command("tar", "-C", "/usr/local", "-xzf", "/tmp/"+goTarball)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract Go: %w", err)
	}

	// Add to PATH
	newPath := os.Getenv("PATH") + ":/usr/local/go/bin"
	os.Setenv("PATH", newPath)

	// Verify installation
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go installed but not found in PATH after installation")
	}

	fmt.Fprintf(bi.logWriter, "  ✓ Go installed\n")
	return nil
}

// ResolveBinaryPath finds the fully-qualified path to a required executable
func (bi *BinaryInstaller) ResolveBinaryPath(binary string, extraPaths ...string) (string, error) {
	// First try to find in PATH
	if path, err := exec.LookPath(binary); err == nil {
		if abs, err := filepath.Abs(path); err == nil {
			return abs, nil
		}
		return path, nil
	}

	// Then try extra candidate paths
	for _, candidate := range extraPaths {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			if abs, err := filepath.Abs(candidate); err == nil {
				return abs, nil
			}
			return candidate, nil
		}
	}

	// Not found - generate error message
	checked := make([]string, 0, len(extraPaths))
	for _, candidate := range extraPaths {
		if candidate != "" {
			checked = append(checked, candidate)
		}
	}

	if len(checked) == 0 {
		return "", fmt.Errorf("required binary %q not found in path", binary)
	}

	return "", fmt.Errorf("required binary %q not found in path (also checked %s)", binary, strings.Join(checked, ", "))
}

// InstallDeBrosBinaries clones and builds DeBros binaries
func (bi *BinaryInstaller) InstallDeBrosBinaries(branch string, oramaHome string, skipRepoUpdate bool) error {
	fmt.Fprintf(bi.logWriter, "  Building DeBros binaries...\n")

	srcDir := filepath.Join(oramaHome, "src")
	binDir := filepath.Join(oramaHome, "bin")

	// Ensure directories exist
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		return fmt.Errorf("failed to create source directory %s: %w", srcDir, err)
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("failed to create bin directory %s: %w", binDir, err)
	}

	// Check if source directory has content (either git repo or pre-existing source)
	hasSourceContent := false
	if entries, err := os.ReadDir(srcDir); err == nil && len(entries) > 0 {
		hasSourceContent = true
	}

	// Check if git repository is already initialized
	isGitRepo := false
	if _, err := os.Stat(filepath.Join(srcDir, ".git")); err == nil {
		isGitRepo = true
	}

	// Handle repository update/clone based on skipRepoUpdate flag
	if skipRepoUpdate {
		fmt.Fprintf(bi.logWriter, "    Skipping repo clone/pull (--no-pull flag)\n")
		if !hasSourceContent {
			return fmt.Errorf("cannot skip pull: source directory is empty at %s (need to populate it first)", srcDir)
		}
		fmt.Fprintf(bi.logWriter, "    Using existing source at %s (skipping git operations)\n", srcDir)
		// Skip to build step - don't execute any git commands
	} else {
		// Clone repository if not present, otherwise update it
		if !isGitRepo {
			fmt.Fprintf(bi.logWriter, "    Cloning repository...\n")
			cmd := exec.Command("git", "clone", "--branch", branch, "--depth", "1", "https://github.com/DeBrosOfficial/network.git", srcDir)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to clone repository: %w", err)
			}
		} else {
			fmt.Fprintf(bi.logWriter, "    Updating repository to latest changes...\n")
			if output, err := exec.Command("git", "-C", srcDir, "fetch", "origin", branch).CombinedOutput(); err != nil {
				return fmt.Errorf("failed to fetch repository updates: %v\n%s", err, string(output))
			}
			if output, err := exec.Command("git", "-C", srcDir, "reset", "--hard", "origin/"+branch).CombinedOutput(); err != nil {
				return fmt.Errorf("failed to reset repository: %v\n%s", err, string(output))
			}
			if output, err := exec.Command("git", "-C", srcDir, "clean", "-fd").CombinedOutput(); err != nil {
				return fmt.Errorf("failed to clean repository: %v\n%s", err, string(output))
			}
		}
	}

	// Build binaries
	fmt.Fprintf(bi.logWriter, "    Building binaries...\n")
	cmd := exec.Command("make", "build")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "HOME="+oramaHome, "PATH="+os.Getenv("PATH")+":/usr/local/go/bin")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build: %v\n%s", err, string(output))
	}

	// Copy binaries
	fmt.Fprintf(bi.logWriter, "    Copying binaries...\n")
	srcBinDir := filepath.Join(srcDir, "bin")

	// Check if source bin directory exists
	if _, err := os.Stat(srcBinDir); os.IsNotExist(err) {
		return fmt.Errorf("source bin directory does not exist at %s - build may have failed", srcBinDir)
	}

	// Check if there are any files to copy
	entries, err := os.ReadDir(srcBinDir)
	if err != nil {
		return fmt.Errorf("failed to read source bin directory: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("source bin directory is empty - build may have failed")
	}

	// Copy each binary individually to avoid wildcard expansion issues
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		srcPath := filepath.Join(srcBinDir, entry.Name())
		dstPath := filepath.Join(binDir, entry.Name())

		// Read source file
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("failed to read binary %s: %w", entry.Name(), err)
		}

		// Write destination file
		if err := os.WriteFile(dstPath, data, 0755); err != nil {
			return fmt.Errorf("failed to write binary %s: %w", entry.Name(), err)
		}
	}

	if err := exec.Command("chmod", "-R", "755", binDir).Run(); err != nil {
		fmt.Fprintf(bi.logWriter, "    ⚠️  Warning: failed to chmod bin directory: %v\n", err)
	}
	if err := exec.Command("chown", "-R", "debros:debros", binDir).Run(); err != nil {
		fmt.Fprintf(bi.logWriter, "    ⚠️  Warning: failed to chown bin directory: %v\n", err)
	}

	fmt.Fprintf(bi.logWriter, "  ✓ DeBros binaries installed\n")
	return nil
}

// InstallSystemDependencies installs system-level dependencies via apt
func (bi *BinaryInstaller) InstallSystemDependencies() error {
	fmt.Fprintf(bi.logWriter, "  Installing system dependencies...\n")

	// Update package list
	cmd := exec.Command("apt-get", "update")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(bi.logWriter, "    Warning: apt update failed\n")
	}

	// Install dependencies including Node.js for anyone-client
	cmd = exec.Command("apt-get", "install", "-y", "curl", "git", "make", "build-essential", "wget", "nodejs", "npm")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install dependencies: %w", err)
	}

	fmt.Fprintf(bi.logWriter, "  ✓ System dependencies installed\n")
	return nil
}

// InitializeIPFSRepo initializes an IPFS repository for a node (unified - no bootstrap/node distinction)
func (bi *BinaryInstaller) InitializeIPFSRepo(ipfsRepoPath string, swarmKeyPath string, apiPort, gatewayPort, swarmPort int) error {
	configPath := filepath.Join(ipfsRepoPath, "config")
	repoExists := false
	if _, err := os.Stat(configPath); err == nil {
		repoExists = true
		fmt.Fprintf(bi.logWriter, "    IPFS repo already exists, ensuring configuration...\n")
	} else {
		fmt.Fprintf(bi.logWriter, "    Initializing IPFS repo...\n")
	}

	if err := os.MkdirAll(ipfsRepoPath, 0755); err != nil {
		return fmt.Errorf("failed to create IPFS repo directory: %w", err)
	}

	// Resolve IPFS binary path
	ipfsBinary, err := bi.ResolveBinaryPath("ipfs", "/usr/local/bin/ipfs", "/usr/bin/ipfs")
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
	fmt.Fprintf(bi.logWriter, "    Configuring IPFS addresses (API: %d, Gateway: %d, Swarm: %d)...\n", apiPort, gatewayPort, swarmPort)
	if err := bi.configureIPFSAddresses(ipfsRepoPath, apiPort, gatewayPort, swarmPort); err != nil {
		return fmt.Errorf("failed to configure IPFS addresses: %w", err)
	}

	// Always disable AutoConf for private swarm when swarm.key is present
	// This is critical - IPFS will fail to start if AutoConf is enabled on a private network
	// We do this even for existing repos to fix repos initialized before this fix was applied
	if swarmKeyExists {
		fmt.Fprintf(bi.logWriter, "    Disabling AutoConf for private swarm...\n")
		cmd := exec.Command(ipfsBinary, "config", "--json", "AutoConf.Enabled", "false")
		cmd.Env = append(os.Environ(), "IPFS_PATH="+ipfsRepoPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to disable AutoConf: %v\n%s", err, string(output))
		}

		// Clear AutoConf placeholders from config to prevent Kubo startup errors
		// When AutoConf is disabled, 'auto' placeholders must be replaced with explicit values or empty
		fmt.Fprintf(bi.logWriter, "    Clearing AutoConf placeholders from IPFS config...\n")

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
			fmt.Fprintf(bi.logWriter, "      %s...\n", step.desc)
			cmd := exec.Command(ipfsBinary, step.args...)
			cmd.Env = append(os.Environ(), "IPFS_PATH="+ipfsRepoPath)
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed while %s: %v\n%s", step.desc, err, string(output))
			}
		}
	}

	// Fix ownership (best-effort, don't fail if it doesn't work)
	if err := exec.Command("chown", "-R", "debros:debros", ipfsRepoPath).Run(); err != nil {
		fmt.Fprintf(bi.logWriter, "    ⚠️  Warning: failed to chown IPFS repo: %v\n", err)
	}

	return nil
}

// configureIPFSAddresses configures the IPFS API, Gateway, and Swarm addresses in the config file
func (bi *BinaryInstaller) configureIPFSAddresses(ipfsRepoPath string, apiPort, gatewayPort, swarmPort int) error {
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

// InitializeIPFSClusterConfig initializes IPFS Cluster configuration (unified - no bootstrap/node distinction)
// This runs `ipfs-cluster-service init` to create the service.json configuration file.
// For existing installations, it ensures the cluster secret is up to date.
// clusterPeers should be in format: ["/ip4/<ip>/tcp/9098/p2p/<cluster-peer-id>"]
func (bi *BinaryInstaller) InitializeIPFSClusterConfig(clusterPath, clusterSecret string, ipfsAPIPort int, clusterPeers []string) error {
	serviceJSONPath := filepath.Join(clusterPath, "service.json")
	configExists := false
	if _, err := os.Stat(serviceJSONPath); err == nil {
		configExists = true
		fmt.Fprintf(bi.logWriter, "    IPFS Cluster config already exists, ensuring it's up to date...\n")
	} else {
		fmt.Fprintf(bi.logWriter, "    Preparing IPFS Cluster path...\n")
	}

	if err := os.MkdirAll(clusterPath, 0755); err != nil {
		return fmt.Errorf("failed to create IPFS Cluster directory: %w", err)
	}

	// Fix ownership before running init (best-effort)
	if err := exec.Command("chown", "-R", "debros:debros", clusterPath).Run(); err != nil {
		fmt.Fprintf(bi.logWriter, "    ⚠️  Warning: failed to chown cluster path before init: %v\n", err)
	}

	// Resolve ipfs-cluster-service binary path
	clusterBinary, err := bi.ResolveBinaryPath("ipfs-cluster-service", "/usr/local/bin/ipfs-cluster-service", "/usr/bin/ipfs-cluster-service")
	if err != nil {
		return fmt.Errorf("ipfs-cluster-service binary not found: %w", err)
	}

	// Initialize cluster config if it doesn't exist
	if !configExists {
		// Initialize cluster config with ipfs-cluster-service init
		// This creates the service.json file with all required sections
		fmt.Fprintf(bi.logWriter, "    Initializing IPFS Cluster config...\n")
		cmd := exec.Command(clusterBinary, "init", "--force")
		cmd.Env = append(os.Environ(), "IPFS_CLUSTER_PATH="+clusterPath)
		// Pass CLUSTER_SECRET to init so it writes the correct secret to service.json directly
		if clusterSecret != "" {
			cmd.Env = append(cmd.Env, "CLUSTER_SECRET="+clusterSecret)
		}
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to initialize IPFS Cluster config: %v\n%s", err, string(output))
		}
	}

	// Always update the cluster secret, IPFS port, and peer addresses (for both new and existing configs)
	// This ensures existing installations get the secret and port synchronized
	// We do this AFTER init to ensure our secret takes precedence
	if clusterSecret != "" {
		fmt.Fprintf(bi.logWriter, "    Updating cluster secret, IPFS port, and peer addresses...\n")
		if err := bi.updateClusterConfig(clusterPath, clusterSecret, ipfsAPIPort, clusterPeers); err != nil {
			return fmt.Errorf("failed to update cluster config: %w", err)
		}

		// Verify the secret was written correctly
		if err := bi.verifyClusterSecret(clusterPath, clusterSecret); err != nil {
			return fmt.Errorf("cluster secret verification failed: %w", err)
		}
		fmt.Fprintf(bi.logWriter, "    ✓ Cluster secret verified\n")
	}

	// Fix ownership again after updates (best-effort)
	if err := exec.Command("chown", "-R", "debros:debros", clusterPath).Run(); err != nil {
		fmt.Fprintf(bi.logWriter, "    ⚠️  Warning: failed to chown cluster path after updates: %v\n", err)
	}

	return nil
}

// updateClusterConfig updates the secret, IPFS port, and peer addresses in IPFS Cluster service.json
func (bi *BinaryInstaller) updateClusterConfig(clusterPath, secret string, ipfsAPIPort int, bootstrapClusterPeers []string) error {
	serviceJSONPath := filepath.Join(clusterPath, "service.json")

	// Read existing config
	data, err := os.ReadFile(serviceJSONPath)
	if err != nil {
		return fmt.Errorf("failed to read service.json: %w", err)
	}

	// Parse JSON
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse service.json: %w", err)
	}

	// Update cluster secret and peer addresses
	if cluster, ok := config["cluster"].(map[string]interface{}); ok {
		cluster["secret"] = secret
		// Configure peer addresses for cluster discovery
		// This allows nodes to find and connect to each other
		if len(bootstrapClusterPeers) > 0 {
			cluster["peer_addresses"] = bootstrapClusterPeers
		}
	} else {
		clusterConfig := map[string]interface{}{
			"secret": secret,
		}
		if len(bootstrapClusterPeers) > 0 {
			clusterConfig["peer_addresses"] = bootstrapClusterPeers
		}
		config["cluster"] = clusterConfig
	}

	// Update IPFS port in IPFS Proxy configuration
	ipfsNodeMultiaddr := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsAPIPort)
	if api, ok := config["api"].(map[string]interface{}); ok {
		if ipfsproxy, ok := api["ipfsproxy"].(map[string]interface{}); ok {
			ipfsproxy["node_multiaddress"] = ipfsNodeMultiaddr
		}
	}

	// Update IPFS port in IPFS Connector configuration
	if ipfsConnector, ok := config["ipfs_connector"].(map[string]interface{}); ok {
		if ipfshttp, ok := ipfsConnector["ipfshttp"].(map[string]interface{}); ok {
			ipfshttp["node_multiaddress"] = ipfsNodeMultiaddr
		}
	}

	// Write back
	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal service.json: %w", err)
	}

	if err := os.WriteFile(serviceJSONPath, updatedData, 0644); err != nil {
		return fmt.Errorf("failed to write service.json: %w", err)
	}

	return nil
}

// verifyClusterSecret verifies that the secret in service.json matches the expected value
func (bi *BinaryInstaller) verifyClusterSecret(clusterPath, expectedSecret string) error {
	serviceJSONPath := filepath.Join(clusterPath, "service.json")

	data, err := os.ReadFile(serviceJSONPath)
	if err != nil {
		return fmt.Errorf("failed to read service.json for verification: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse service.json for verification: %w", err)
	}

	if cluster, ok := config["cluster"].(map[string]interface{}); ok {
		if secret, ok := cluster["secret"].(string); ok {
			if secret != expectedSecret {
				return fmt.Errorf("secret mismatch: expected %s, got %s", expectedSecret, secret)
			}
			return nil
		}
		return fmt.Errorf("secret not found in cluster config")
	}

	return fmt.Errorf("cluster section not found in service.json")
}

// GetClusterPeerMultiaddr reads the IPFS Cluster peer ID and returns its multiaddress
// Returns format: /ip4/<ip>/tcp/9098/p2p/<cluster-peer-id>
func (bi *BinaryInstaller) GetClusterPeerMultiaddr(clusterPath string, nodeIP string) (string, error) {
	identityPath := filepath.Join(clusterPath, "identity.json")

	// Read identity file
	data, err := os.ReadFile(identityPath)
	if err != nil {
		return "", fmt.Errorf("failed to read identity.json: %w", err)
	}

	// Parse JSON
	var identity map[string]interface{}
	if err := json.Unmarshal(data, &identity); err != nil {
		return "", fmt.Errorf("failed to parse identity.json: %w", err)
	}

	// Get peer ID
	peerID, ok := identity["id"].(string)
	if !ok || peerID == "" {
		return "", fmt.Errorf("peer ID not found in identity.json")
	}

	// Construct multiaddress: /ip4/<ip>/tcp/9098/p2p/<peer-id>
	// Port 9098 is the default cluster listen port
	multiaddr := fmt.Sprintf("/ip4/%s/tcp/9098/p2p/%s", nodeIP, peerID)
	return multiaddr, nil
}

// InitializeRQLiteDataDir initializes RQLite data directory
func (bi *BinaryInstaller) InitializeRQLiteDataDir(dataDir string) error {
	fmt.Fprintf(bi.logWriter, "    Initializing RQLite data dir...\n")

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create RQLite data directory: %w", err)
	}

	if err := exec.Command("chown", "-R", "debros:debros", dataDir).Run(); err != nil {
		fmt.Fprintf(bi.logWriter, "    ⚠️  Warning: failed to chown RQLite data dir: %v\n", err)
	}
	return nil
}

// InstallAnyoneClient installs the anyone-client npm package globally
func (bi *BinaryInstaller) InstallAnyoneClient() error {
	// Check if anyone-client is already available via npx (more reliable for scoped packages)
	if cmd := exec.Command("npx", "--yes", "@anyone-protocol/anyone-client", "--version"); cmd.Run() == nil {
		fmt.Fprintf(bi.logWriter, "  ✓ anyone-client already installed\n")
		return nil
	}

	fmt.Fprintf(bi.logWriter, "  Installing anyone-client...\n")

	// Initialize NPM cache structure to ensure all directories exist
	// This prevents "mkdir" errors when NPM tries to create nested cache directories
	fmt.Fprintf(bi.logWriter, "    Initializing NPM cache...\n")
	
	// Create nested cache directories with proper permissions
	debrosHome := "/home/debros"
	npmCacheDirs := []string{
		filepath.Join(debrosHome, ".npm"),
		filepath.Join(debrosHome, ".npm", "_cacache"),
		filepath.Join(debrosHome, ".npm", "_cacache", "tmp"),
		filepath.Join(debrosHome, ".npm", "_logs"),
	}
	
	for _, dir := range npmCacheDirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			fmt.Fprintf(bi.logWriter, "    ⚠️  Failed to create %s: %v\n", dir, err)
			continue
		}
		// Fix ownership to debros user (sequential to avoid race conditions)
		if err := exec.Command("chown", "debros:debros", dir).Run(); err != nil {
			fmt.Fprintf(bi.logWriter, "    ⚠️  Warning: failed to chown %s: %v\n", dir, err)
		}
		if err := exec.Command("chmod", "700", dir).Run(); err != nil {
			fmt.Fprintf(bi.logWriter, "    ⚠️  Warning: failed to chmod %s: %v\n", dir, err)
		}
	}
	
	// Recursively fix ownership of entire .npm directory to ensure all nested files are owned by debros
	if err := exec.Command("chown", "-R", "debros:debros", filepath.Join(debrosHome, ".npm")).Run(); err != nil {
		fmt.Fprintf(bi.logWriter, "    ⚠️  Warning: failed to chown .npm directory: %v\n", err)
	}
	
	// Run npm cache verify as debros user with proper environment
	cacheInitCmd := exec.Command("sudo", "-u", "debros", "npm", "cache", "verify", "--silent")
	cacheInitCmd.Env = append(os.Environ(), "HOME="+debrosHome)
	if err := cacheInitCmd.Run(); err != nil {
		fmt.Fprintf(bi.logWriter, "    ⚠️  NPM cache verify warning: %v (continuing anyway)\n", err)
	}

	// Install anyone-client globally via npm (using scoped package name)
	cmd := exec.Command("npm", "install", "-g", "@anyone-protocol/anyone-client")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install anyone-client: %w\n%s", err, string(output))
	}

	// Verify installation - try npx first (most reliable for scoped packages)
	verifyCmd := exec.Command("npx", "--yes", "@anyone-protocol/anyone-client", "--version")
	if err := verifyCmd.Run(); err != nil {
		// Fallback: check if binary exists in common locations
		possiblePaths := []string{
			"/usr/local/bin/anyone-client",
			"/usr/bin/anyone-client",
		}
		found := false
		for _, path := range possiblePaths {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				found = true
				break
			}
		}
		if !found {
			// Try npm bin -g to find global bin directory
			cmd := exec.Command("npm", "bin", "-g")
			if output, err := cmd.Output(); err == nil {
				npmBinDir := strings.TrimSpace(string(output))
				candidate := filepath.Join(npmBinDir, "anyone-client")
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
					found = true
				}
			}
		}
		if !found {
			return fmt.Errorf("anyone-client installation verification failed - package may not provide a binary, but npx should work")
		}
	}

	fmt.Fprintf(bi.logWriter, "  ✓ anyone-client installed\n")
	return nil
}
