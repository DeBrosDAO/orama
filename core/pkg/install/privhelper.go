package install

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// privHelperBinary is the helper's name in the release's bin/.
const privHelperBinary = "orama-privhelper"

// Where the helper and its grant are installed. Variables only so a test can
// point them at a temporary directory; nothing else assigns them.
var (
	privHelperDest   = privhelper.Path
	oramaSudoersPath = "/etc/sudoers.d/orama-namespaces"
	chownRoot        = func(path string) error { return os.Chown(path, 0, 0) }
)

// EnsurePrivHelper installs orama-privhelper from the release's bin/ and then
// the sudoers grant that names it — in that order, so the grant never points
// at a file that is not there. Both steps are fatal: every root action the
// running node takes goes through this pair, and a node without it cannot
// start a single namespace service.
//
// It runs under the NEW binary: at the end of Phase 2b on install, and after
// the post-swap re-exec on upgrade. The pre-re-exec part of an upgrade is the
// previous release's code, which knows nothing of the helper and writes the
// old wildcard rules; only a step compiled into this release can replace them.
func (ps *ProductionSetup) EnsurePrivHelper() error {
	src := filepath.Join(ps.oramaHome, "bin", privHelperBinary)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("%s is missing from the release (%w); rebuild the archive with this version of `orama build`", src, err)
	}
	if err := copyBinary(src, privHelperDest); err != nil {
		return fmt.Errorf("install %s: %w", privHelperDest, err)
	}
	if err := sameContent(src, privHelperDest); err != nil {
		return fmt.Errorf("install %s: %w", privHelperDest, err)
	}
	if err := chownRoot(privHelperDest); err != nil {
		return fmt.Errorf("chown %s: %w", privHelperDest, err)
	}
	if err := os.Chmod(privHelperDest, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", privHelperDest, err)
	}
	if err := writeSudoersFile(oramaSudoersPath, privhelper.SudoersRule("orama")); err != nil {
		return fmt.Errorf("grant the orama user %s: %w", privHelperDest, err)
	}
	ps.logf("  ✓ %s installed and granted to the orama user", privHelperDest)
	return nil
}

// sameContent checks that the copy at b is byte-identical to a: a truncated
// helper would pass every later step and fail only when the node needs root.
func sameContent(a, b string) error {
	ha, err := fileSHA256(a)
	if err != nil {
		return err
	}
	hb, err := fileSHA256(b)
	if err != nil {
		return err
	}
	if ha != hb {
		return fmt.Errorf("copy of %s differs from its source", a)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
