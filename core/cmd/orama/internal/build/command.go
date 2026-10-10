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
	// addresses (signer rotation, docs/whitepaper/technical-reference/vol1/16-secrets-and-keys.md).
	Signers []string
	// SkipGlobalLayer leaves out the chain node (oramad, its verifier,
	// orama-global) and the cosmovisor release. The archive then serves a
	// cluster-only install, and `orama global install` refuses it. The default
	// builds the layer and fails when the toolchain is missing.
	SkipGlobalLayer bool
	// ReleaseRoot is the path of a TUF root.json to put in the signed
	// manifest: a node that installs the build adopts it as its release root,
	// the way it takes a signer rotation.
	ReleaseRoot string
	// LocalReleaseRepo builds the orama CLI with the localrepo tag, so that a process with
	// ORAMA_ALLOW_LOCAL_RELEASE_REPO=1 may fetch a release repository from a loopback or private
	// address. Only the fleet e2e suite, which serves one from a node's loopback, passes it; an
	// archive built with it must never reach a network that is not a test fleet.
	LocalReleaseRepo bool
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
