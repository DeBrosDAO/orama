package production

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BinaryInstaller handles downloading and installing external binaries
type BinaryInstaller struct {
	arch      string
	logWriter interface{} // io.Writer
}

// NewBinaryInstaller creates a new binary installer
func NewBinaryInstaller(arch string, logWriter interface{}) *BinaryInstaller {
	return &BinaryInstaller{
		arch:      arch,
		logWriter: logWriter,
	}
}

// InstallRQLite downloads and installs RQLite
func (bi *BinaryInstaller) InstallRQLite() error {
	if _, err := exec.LookPath("rqlited"); err == nil {
		fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ RQLite already installed\n")
		return nil
	}

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  Installing RQLite...\n")

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
	exec.Command("cp", dir+"/rqlited", "/usr/local/bin/").Run()
	exec.Command("chmod", "+x", "/usr/local/bin/rqlited").Run()

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ RQLite installed\n")
	return nil
}

// InstallIPFS downloads and installs IPFS (Kubo)
func (bi *BinaryInstaller) InstallIPFS() error {
	if _, err := exec.LookPath("ipfs"); err == nil {
		fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ IPFS already installed\n")
		return nil
	}

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  Installing IPFS (Kubo)...\n")

	// Use official install script
	cmd := exec.Command("bash", "-c", "curl -fsSL https://dist.ipfs.tech/kubo/v0.27.0/install.sh | bash")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install IPFS: %w", err)
	}

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ IPFS installed\n")
	return nil
}

// InstallIPFSCluster downloads and installs IPFS Cluster Service
func (bi *BinaryInstaller) InstallIPFSCluster() error {
	if _, err := exec.LookPath("ipfs-cluster-service"); err == nil {
		fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ IPFS Cluster already installed\n")
		return nil
	}

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  Installing IPFS Cluster Service...\n")

	// Check if Go is available
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("Go not found - required to install IPFS Cluster. Please install Go first")
	}

	cmd := exec.Command("go", "install", "github.com/ipfs-cluster/ipfs-cluster/cmd/ipfs-cluster-service@latest")
	cmd.Env = append(os.Environ(), "GOBIN=/usr/local/bin")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install IPFS Cluster: %w", err)
	}

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ IPFS Cluster installed\n")
	return nil
}

// InstallOlric downloads and installs Olric server
func (bi *BinaryInstaller) InstallOlric() error {
	if _, err := exec.LookPath("olric-server"); err == nil {
		fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ Olric already installed\n")
		return nil
	}

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  Installing Olric...\n")

	// Check if Go is available
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("Go not found - required to install Olric. Please install Go first")
	}

	cmd := exec.Command("go", "install", "github.com/olric-data/olric/cmd/olric-server@v0.7.0")
	cmd.Env = append(os.Environ(), "GOBIN=/usr/local/bin")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install Olric: %w", err)
	}

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ Olric installed\n")
	return nil
}

// InstallGo downloads and installs Go toolchain
func (bi *BinaryInstaller) InstallGo() error {
	if _, err := exec.LookPath("go"); err == nil {
		fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ Go already installed\n")
		return nil
	}

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  Installing Go...\n")

	goTarball := fmt.Sprintf("go1.21.6.linux-%s.tar.gz", bi.arch)
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
	os.Setenv("PATH", os.Getenv("PATH")+":/usr/local/go/bin")

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ Go installed\n")
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
func (bi *BinaryInstaller) InstallDeBrosBinaries(branch string, debrosHome string) error {
	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  Building DeBros binaries...\n")

	srcDir := filepath.Join(debrosHome, "src")
	binDir := filepath.Join(debrosHome, "bin")

	// Ensure directories exist
	os.MkdirAll(srcDir, 0755)
	os.MkdirAll(binDir, 0755)

	// Clone repository if not present
	if _, err := os.Stat(filepath.Join(srcDir, "Makefile")); os.IsNotExist(err) {
		fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "    Cloning repository...\n")
		cmd := exec.Command("git", "clone", "--branch", branch, "--depth", "1", "https://github.com/DeBrosOfficial/network.git", srcDir)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to clone repository: %w", err)
		}
	}

	// Build binaries
	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "    Building binaries...\n")
	cmd := exec.Command("make", "build")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "HOME="+debrosHome, "PATH="+os.Getenv("PATH")+":/usr/local/go/bin")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build: %v\n%s", err, string(output))
	}

	// Copy binaries
	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "    Copying binaries...\n")
	cmd = exec.Command("sh", "-c", fmt.Sprintf("cp -r %s/bin/* %s/", srcDir, binDir))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to copy binaries: %w", err)
	}

	exec.Command("chmod", "-R", "755", binDir).Run()
	exec.Command("chown", "-R", "debros:debros", binDir).Run()

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ DeBros binaries installed\n")
	return nil
}

// InstallSystemDependencies installs system-level dependencies via apt
func (bi *BinaryInstaller) InstallSystemDependencies() error {
	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  Installing system dependencies...\n")

	// Update package list
	cmd := exec.Command("apt-get", "update")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "    Warning: apt update failed\n")
	}

	// Install dependencies
	cmd = exec.Command("apt-get", "install", "-y", "curl", "git", "make", "build-essential", "wget")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install dependencies: %w", err)
	}

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "  ✓ System dependencies installed\n")
	return nil
}

// InitializeIPFSRepo initializes an IPFS repository for a node
func (bi *BinaryInstaller) InitializeIPFSRepo(nodeType, ipfsRepoPath string, swarmKeyPath string) error {
	configPath := filepath.Join(ipfsRepoPath, "config")
	if _, err := os.Stat(configPath); err == nil {
		// Already initialized
		return nil
	}

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "    Initializing IPFS repo for %s...\n", nodeType)

	if err := os.MkdirAll(ipfsRepoPath, 0755); err != nil {
		return fmt.Errorf("failed to create IPFS repo directory: %w", err)
	}

	// Resolve IPFS binary path
	ipfsBinary, err := bi.ResolveBinaryPath("ipfs", "/usr/local/bin/ipfs", "/usr/bin/ipfs")
	if err != nil {
		return err
	}

	// Initialize IPFS with the correct repo path
	cmd := exec.Command(ipfsBinary, "init", "--profile=server", "--repo-dir="+ipfsRepoPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to initialize IPFS: %v\n%s", err, string(output))
	}

	// Copy swarm key if present
	if data, err := os.ReadFile(swarmKeyPath); err == nil {
		if err := os.WriteFile(filepath.Join(ipfsRepoPath, "swarm.key"), data, 0600); err != nil {
			return fmt.Errorf("failed to copy swarm key: %w", err)
		}
	}

	// Fix ownership
	exec.Command("chown", "-R", "debros:debros", ipfsRepoPath).Run()

	return nil
}

// InitializeIPFSClusterConfig initializes IPFS Cluster configuration
// Note: This is a placeholder config. The full initialization will occur via `ipfs-cluster-service init`
// which is run during Phase2cInitializeServices with the IPFS_CLUSTER_PATH env var set.
func (bi *BinaryInstaller) InitializeIPFSClusterConfig(nodeType, clusterPath, clusterSecret string, ipfsAPIPort int) error {
	serviceJSONPath := filepath.Join(clusterPath, "service.json")
	if _, err := os.Stat(serviceJSONPath); err == nil {
		// Already initialized
		return nil
	}

	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "    Preparing IPFS Cluster path for %s...\n", nodeType)

	if err := os.MkdirAll(clusterPath, 0755); err != nil {
		return fmt.Errorf("failed to create IPFS Cluster directory: %w", err)
	}

	exec.Command("chown", "-R", "debros:debros", clusterPath).Run()
	return nil
}

// InitializeRQLiteDataDir initializes RQLite data directory
func (bi *BinaryInstaller) InitializeRQLiteDataDir(nodeType, dataDir string) error {
	fmt.Fprintf(bi.logWriter.(interface{ Write([]byte) (int, error) }), "    Initializing RQLite data dir for %s...\n", nodeType)

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create RQLite data directory: %w", err)
	}

	exec.Command("chown", "-R", "debros:debros", dataDir).Run()
	return nil
}
