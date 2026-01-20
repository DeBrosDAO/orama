package installers

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GatewayInstaller handles DeBros binary installation (including gateway)
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
	// Check if binaries exist (gateway is embedded in orama-node)
	return false // Always build to ensure latest version
}

// Install clones and builds DeBros binaries
func (gi *GatewayInstaller) Install() error {
	// This is a placeholder - actual installation is handled by InstallDeBrosBinaries
	return nil
}

// Configure is a placeholder for gateway configuration
func (gi *GatewayInstaller) Configure() error {
	// Configuration is handled by the orchestrator
	return nil
}

// InstallDeBrosBinaries clones and builds DeBros binaries
func (gi *GatewayInstaller) InstallDeBrosBinaries(branch string, oramaHome string, skipRepoUpdate bool) error {
	fmt.Fprintf(gi.logWriter, "  Building DeBros binaries...\n")

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
		fmt.Fprintf(gi.logWriter, "    Skipping repo clone/pull (--no-pull flag)\n")
		if !hasSourceContent {
			return fmt.Errorf("cannot skip pull: source directory is empty at %s (need to populate it first)", srcDir)
		}
		fmt.Fprintf(gi.logWriter, "    Using existing source at %s (skipping git operations)\n", srcDir)
		// Skip to build step - don't execute any git commands
	} else {
		// Clone repository if not present, otherwise update it
		if !isGitRepo {
			fmt.Fprintf(gi.logWriter, "    Cloning repository...\n")
			cmd := exec.Command("git", "clone", "--branch", branch, "--depth", "1", "https://github.com/DeBrosOfficial/network.git", srcDir)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to clone repository: %w", err)
			}
		} else {
			fmt.Fprintf(gi.logWriter, "    Updating repository to latest changes...\n")
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
	fmt.Fprintf(gi.logWriter, "    Building binaries...\n")
	cmd := exec.Command("make", "build")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "HOME="+oramaHome, "PATH="+os.Getenv("PATH")+":/usr/local/go/bin")
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
		fmt.Fprintf(gi.logWriter, "    ⚠️  Warning: failed to chmod bin directory: %v\n", err)
	}
	if err := exec.Command("chown", "-R", "debros:debros", binDir).Run(); err != nil {
		fmt.Fprintf(gi.logWriter, "    ⚠️  Warning: failed to chown bin directory: %v\n", err)
	}

	// Grant CAP_NET_BIND_SERVICE to orama-node to allow binding to ports 80/443 without root
	nodeBinary := filepath.Join(binDir, "orama-node")
	if _, err := os.Stat(nodeBinary); err == nil {
		if err := exec.Command("setcap", "cap_net_bind_service=+ep", nodeBinary).Run(); err != nil {
			fmt.Fprintf(gi.logWriter, "    ⚠️  Warning: failed to setcap on orama-node: %v\n", err)
			fmt.Fprintf(gi.logWriter, "    ⚠️  Gateway may not be able to bind to port 80/443\n")
		} else {
			fmt.Fprintf(gi.logWriter, "    ✓ Set CAP_NET_BIND_SERVICE on orama-node\n")
		}
	}

	fmt.Fprintf(gi.logWriter, "  ✓ DeBros binaries installed\n")
	return nil
}

// InstallGo downloads and installs Go toolchain
func (gi *GatewayInstaller) InstallGo() error {
	if _, err := exec.LookPath("go"); err == nil {
		fmt.Fprintf(gi.logWriter, "  ✓ Go already installed\n")
		return nil
	}

	fmt.Fprintf(gi.logWriter, "  Installing Go...\n")

	goTarball := fmt.Sprintf("go1.22.5.linux-%s.tar.gz", gi.arch)
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

	// Install dependencies including Node.js for anyone-client
	cmd = exec.Command("apt-get", "install", "-y", "curl", "git", "make", "build-essential", "wget", "nodejs", "npm")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install dependencies: %w", err)
	}

	fmt.Fprintf(gi.logWriter, "  ✓ System dependencies installed\n")
	return nil
}

// InstallAnyoneClient installs the anyone-client npm package globally
func (gi *GatewayInstaller) InstallAnyoneClient() error {
	// Check if anyone-client is already available via npx (more reliable for scoped packages)
	// Note: the CLI binary is "anyone-client", not the full scoped package name
	if cmd := exec.Command("npx", "anyone-client", "--help"); cmd.Run() == nil {
		fmt.Fprintf(gi.logWriter, "  ✓ anyone-client already installed\n")
		return nil
	}

	fmt.Fprintf(gi.logWriter, "  Installing anyone-client...\n")

	// Initialize NPM cache structure to ensure all directories exist
	// This prevents "mkdir" errors when NPM tries to create nested cache directories
	fmt.Fprintf(gi.logWriter, "    Initializing NPM cache...\n")

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
			fmt.Fprintf(gi.logWriter, "    ⚠️  Failed to create %s: %v\n", dir, err)
			continue
		}
		// Fix ownership to debros user (sequential to avoid race conditions)
		if err := exec.Command("chown", "debros:debros", dir).Run(); err != nil {
			fmt.Fprintf(gi.logWriter, "    ⚠️  Warning: failed to chown %s: %v\n", dir, err)
		}
		if err := exec.Command("chmod", "700", dir).Run(); err != nil {
			fmt.Fprintf(gi.logWriter, "    ⚠️  Warning: failed to chmod %s: %v\n", dir, err)
		}
	}

	// Recursively fix ownership of entire .npm directory to ensure all nested files are owned by debros
	if err := exec.Command("chown", "-R", "debros:debros", filepath.Join(debrosHome, ".npm")).Run(); err != nil {
		fmt.Fprintf(gi.logWriter, "    ⚠️  Warning: failed to chown .npm directory: %v\n", err)
	}

	// Run npm cache verify as debros user with proper environment
	cacheInitCmd := exec.Command("sudo", "-u", "debros", "npm", "cache", "verify", "--silent")
	cacheInitCmd.Env = append(os.Environ(), "HOME="+debrosHome)
	if err := cacheInitCmd.Run(); err != nil {
		fmt.Fprintf(gi.logWriter, "    ⚠️  NPM cache verify warning: %v (continuing anyway)\n", err)
	}

	// Install anyone-client globally via npm (using scoped package name)
	cmd := exec.Command("npm", "install", "-g", "@anyone-protocol/anyone-client")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install anyone-client: %w\n%s", err, string(output))
	}

	// Create terms-agreement file to bypass interactive prompt when running as a service
	termsFile := filepath.Join(debrosHome, "terms-agreement")
	if err := os.WriteFile(termsFile, []byte("agreed"), 0644); err != nil {
		fmt.Fprintf(gi.logWriter, "    ⚠️  Warning: failed to create terms-agreement: %v\n", err)
	} else {
		if err := exec.Command("chown", "debros:debros", termsFile).Run(); err != nil {
			fmt.Fprintf(gi.logWriter, "    ⚠️  Warning: failed to chown terms-agreement: %v\n", err)
		}
	}

	// Verify installation - try npx with the correct CLI name (anyone-client, not full scoped package name)
	verifyCmd := exec.Command("npx", "anyone-client", "--help")
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

	fmt.Fprintf(gi.logWriter, "  ✓ anyone-client installed\n")
	return nil
}
