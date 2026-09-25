package installers

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// GatewayInstaller handles Orama binary installation (including gateway)
type GatewayInstaller struct {
	*BaseInstaller
}

// NewGatewayInstaller creates a new gateway installer
func NewGatewayInstaller(arch string, logWriter io.Writer) *GatewayInstaller {
	return &GatewayInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
	}
}

// IsInstalled checks if gateway binaries are already installed
func (gi *GatewayInstaller) IsInstalled() bool {
	// Always rebuild so the node and the standalone gateway binary stay in step.
	return false
}

// Install clones and builds Orama binaries
func (gi *GatewayInstaller) Install() error {
	// This is a placeholder - actual installation is handled by InstallDeBrosBinaries
	return nil
}

// Configure is a placeholder for gateway configuration
func (gi *GatewayInstaller) Configure() error {
	// Configuration is handled by the orchestrator
	return nil
}

// InstallDeBrosBinaries builds Orama binaries from source at /opt/orama/src.
// Source must already be present (uploaded via SCP archive).
func (gi *GatewayInstaller) InstallDeBrosBinaries(oramaHome string) error {
	fmt.Fprintf(gi.logWriter, "  Building Orama binaries...\n")

	srcDir := filepath.Join(oramaHome, "src")
	binDir := filepath.Join(oramaHome, "bin")

	// Ensure directories exist
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		return fmt.Errorf("failed to create source directory %s: %w", srcDir, err)
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("failed to create bin directory %s: %w", binDir, err)
	}

	// Verify source exists
	if entries, err := os.ReadDir(srcDir); err != nil || len(entries) == 0 {
		return fmt.Errorf("source directory is empty at %s (upload source archive first)", srcDir)
	}

	// Build binaries
	fmt.Fprintf(gi.logWriter, "    Building binaries...\n")
	cmd := exec.Command("make", "build")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "HOME="+oramaHome, "PATH="+os.Getenv("PATH")+":/usr/local/go/bin", "GOPROXY=https://proxy.golang.org|direct", "GONOSUMDB=*")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build: %v\n%s", err, string(output))
	}

	// Copy binaries
	fmt.Fprintf(gi.logWriter, "    Copying binaries...\n")
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

	// Copy each binary individually to avoid wildcard expansion issues.
	// We remove the destination first to avoid "text file busy" errors when
	// overwriting a binary that is currently executing (e.g., the orama CLI
	// running this upgrade). On Linux, removing a running binary is safe —
	// the kernel keeps the inode alive until the process exits.
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

		// Remove existing binary first to avoid "text file busy" on running executables
		_ = os.Remove(dstPath)

		// Write destination file
		if err := os.WriteFile(dstPath, data, 0755); err != nil {
			return fmt.Errorf("failed to write binary %s: %w", entry.Name(), err)
		}
	}

	if err := exec.Command("chmod", "-R", "755", binDir).Run(); err != nil {
		fmt.Fprintf(gi.logWriter, "    ⚠️  Warning: failed to chmod bin directory: %v\n", err)
	}

	fmt.Fprintf(gi.logWriter, "  ✓ Orama binaries installed\n")
	return nil
}

// InstallGo downloads and installs Go toolchain
func (gi *GatewayInstaller) InstallGo() error {
	requiredVersion := constants.GoVersion
	if goPath, err := exec.LookPath("go"); err == nil {
		// Check version - upgrade if too old
		out, _ := exec.Command(goPath, "version").Output()
		if strings.Contains(string(out), "go"+requiredVersion) {
			fmt.Fprintf(gi.logWriter, "  ✓ Go already installed (%s)\n", strings.TrimSpace(string(out)))
			return nil
		}
		fmt.Fprintf(gi.logWriter, "  Upgrading Go (current: %s, need %s)...\n", strings.TrimSpace(string(out)), requiredVersion)
		os.RemoveAll("/usr/local/go")
	} else {
		fmt.Fprintf(gi.logWriter, "  Installing Go...\n")
	}

	// Always remove old Go installation to avoid mixing versions
	os.RemoveAll("/usr/local/go")

	goTarball := fmt.Sprintf("go%s.linux-%s.tar.gz", requiredVersion, gi.arch)
	goURL := fmt.Sprintf("https://go.dev/dl/%s", goTarball)

	// Download
	if err := DownloadFile(goURL, "/tmp/"+goTarball); err != nil {
		return fmt.Errorf("failed to download Go: %w", err)
	}

	// Extract
	if err := ExtractTarball("/tmp/"+goTarball, "/usr/local"); err != nil {
		return fmt.Errorf("failed to extract Go: %w", err)
	}

	// Add to PATH
	newPath := os.Getenv("PATH") + ":/usr/local/go/bin"
	os.Setenv("PATH", newPath)

	// Verify installation
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go installed but not found in PATH after installation")
	}

	fmt.Fprintf(gi.logWriter, "  ✓ Go installed\n")
	return nil
}

// InstallSystemDependencies installs system-level dependencies via apt
func (gi *GatewayInstaller) InstallSystemDependencies() error {
	fmt.Fprintf(gi.logWriter, "  Installing system dependencies...\n")

	// Update package list
	cmd := exec.Command("apt-get", "update")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(gi.logWriter, "    Warning: apt update failed\n")
	}

	// Install dependencies including Node.js (node/npm deployments) and unzip for source downloads
	cmd = exec.Command("apt-get", "install", "-y", "curl", "make", "build-essential", "wget", "unzip", "nodejs", "npm")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install dependencies: %w", err)
	}

	fmt.Fprintf(gi.logWriter, "  ✓ System dependencies installed\n")
	return nil
}
