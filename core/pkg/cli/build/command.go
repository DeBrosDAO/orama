package build

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Flags represents build command flags.
type Flags struct {
	Arch    string
	Output  string
	Verbose bool
	Sign    bool // Sign the archive manifest with rootwallet
}

// Handle is the entry point for the build command.
// Run executes the build command.
func Run(flags *Flags) error {
	return NewBuilder(flags).Build()
}

// findProjectRoot walks up from the current directory looking for go.mod.
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// Verify it's the network project
			if _, err := os.Stat(filepath.Join(dir, "cmd", "cli")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("could not find project root (no go.mod with cmd/cli found)")
}

// detectHostArch returns the host architecture in Go naming convention.
func detectHostArch() string {
	return runtime.GOARCH
}
