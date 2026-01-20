package installers

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// OlricInstaller handles Olric server installation
type OlricInstaller struct {
	*BaseInstaller
	version string
}

// NewOlricInstaller creates a new Olric installer
func NewOlricInstaller(arch string, logWriter io.Writer) *OlricInstaller {
	return &OlricInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		version:       "v0.7.0",
	}
}

// IsInstalled checks if Olric is already installed
func (oi *OlricInstaller) IsInstalled() bool {
	_, err := exec.LookPath("olric-server")
	return err == nil
}

// Install downloads and installs Olric server
func (oi *OlricInstaller) Install() error {
	if oi.IsInstalled() {
		fmt.Fprintf(oi.logWriter, "  ✓ Olric already installed\n")
		return nil
	}

	fmt.Fprintf(oi.logWriter, "  Installing Olric...\n")

	// Check if Go is available
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go not found - required to install Olric. Please install Go first")
	}

	cmd := exec.Command("go", "install", fmt.Sprintf("github.com/olric-data/olric/cmd/olric-server@%s", oi.version))
	cmd.Env = append(os.Environ(), "GOBIN=/usr/local/bin")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install Olric: %w", err)
	}

	fmt.Fprintf(oi.logWriter, "  ✓ Olric installed\n")
	return nil
}

// Configure is a placeholder for Olric configuration
func (oi *OlricInstaller) Configure() error {
	// Configuration is handled by the orchestrator
	return nil
}
