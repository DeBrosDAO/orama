package installers

import (
	"fmt"
	"io"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
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
		version:       constants.RQLiteVersion,
	}
}

// InitializeDataDir initializes RQLite data directory. root is the anchor
// dataDir lives under (rootfs); the directory is the orama user's.
func (ri *RQLiteInstaller) InitializeDataDir(root rootfs.Root, dataDir string) error {
	fmt.Fprintf(ri.logWriter, "    Initializing RQLite data dir...\n")

	if err := root.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create RQLite data directory: %w", err)
	}

	return nil
}
