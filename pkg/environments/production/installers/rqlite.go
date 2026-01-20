package installers

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// RQLiteInstaller handles RQLite installation
type RQLiteInstaller struct {
	*BaseInstaller
	version string
}

// NewRQLiteInstaller creates a new RQLite installer
func NewRQLiteInstaller(arch string, logWriter io.Writer) *RQLiteInstaller {
	return &RQLiteInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		version:       "8.43.0",
	}
}

// IsInstalled checks if RQLite is already installed
func (ri *RQLiteInstaller) IsInstalled() bool {
	_, err := exec.LookPath("rqlited")
	return err == nil
}

// Install downloads and installs RQLite
func (ri *RQLiteInstaller) Install() error {
	if ri.IsInstalled() {
		fmt.Fprintf(ri.logWriter, "  ✓ RQLite already installed\n")
		return nil
	}

	fmt.Fprintf(ri.logWriter, "  Installing RQLite...\n")

	tarball := fmt.Sprintf("rqlite-v%s-linux-%s.tar.gz", ri.version, ri.arch)
	url := fmt.Sprintf("https://github.com/rqlite/rqlite/releases/download/v%s/%s", ri.version, tarball)

	// Download
	if err := DownloadFile(url, "/tmp/"+tarball); err != nil {
		return fmt.Errorf("failed to download RQLite: %w", err)
	}

	// Extract
	if err := ExtractTarball("/tmp/"+tarball, "/tmp"); err != nil {
		return fmt.Errorf("failed to extract RQLite: %w", err)
	}

	// Copy binaries
	dir := fmt.Sprintf("/tmp/rqlite-v%s-linux-%s", ri.version, ri.arch)
	if err := exec.Command("cp", dir+"/rqlited", "/usr/local/bin/").Run(); err != nil {
		return fmt.Errorf("failed to copy rqlited binary: %w", err)
	}
	if err := exec.Command("chmod", "+x", "/usr/local/bin/rqlited").Run(); err != nil {
		fmt.Fprintf(ri.logWriter, "    ⚠️  Warning: failed to chmod rqlited: %v\n", err)
	}

	// Ensure PATH includes /usr/local/bin
	os.Setenv("PATH", os.Getenv("PATH")+":/usr/local/bin")

	fmt.Fprintf(ri.logWriter, "  ✓ RQLite installed\n")
	return nil
}

// Configure initializes RQLite data directory
func (ri *RQLiteInstaller) Configure() error {
	// Configuration is handled by the orchestrator
	return nil
}

// InitializeDataDir initializes RQLite data directory
func (ri *RQLiteInstaller) InitializeDataDir(dataDir string) error {
	fmt.Fprintf(ri.logWriter, "    Initializing RQLite data dir...\n")

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create RQLite data directory: %w", err)
	}

	if err := exec.Command("chown", "-R", "debros:debros", dataDir).Run(); err != nil {
		fmt.Fprintf(ri.logWriter, "    ⚠️  Warning: failed to chown RQLite data dir: %v\n", err)
	}
	return nil
}
