package build

import (
	"fmt"
	"os"
	"path/filepath"
)

// Flags represents build command flags.
type Flags struct {
	Arch    string
	Output  string
	Verbose bool
	// Unsigned skips signing. Nodes refuse an unsigned archive, so this is for
	// an archive that is only inspected locally; every archive meant for a node
	// is signed, which is why signing is the default.
	Unsigned bool
	// Signers, when set, goes into the signed manifest: a node that verifies
	// the archive against its current trust anchor then trusts exactly these
	// addresses (signer rotation, docs/SECURITY.md).
	Signers []string
}

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
			if _, err := os.Stat(filepath.Join(dir, "cmd", "orama")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("could not find project root (no go.mod with cmd/orama found)")
}
