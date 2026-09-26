package install

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// privHelperBinary is the helper's name in the release's bin/.
const privHelperBinary = "orama-privhelper"

// Where the helper and its units are installed. Variables only so a test can
// point them at a temporary directory and a stand-in for systemctl; nothing
// else assigns them.
var (
	privHelperDest = privhelper.Path
	systemdUnitDir = "/etc/systemd/system"
	// legacySudoersPath held the wildcard sudoers rules the helper replaced.
	legacySudoersPath = "/etc/sudoers.d/orama-namespaces"
	chownRoot         = func(path string) error { return os.Chown(path, 0, 0) }
	runSystemctl      = func(args ...string) ([]byte, error) { return exec.Command("systemctl", args...).CombinedOutput() }
)

// EnsurePrivHelper installs orama-privhelper from the release's bin/ and the
// socket that serves it, and starts the socket — in that order, so nothing
// ever listens for a helper that is not there. Every step is fatal: every root
// action the running node takes goes through this socket, and a node without
// it cannot start a single namespace service.
//
// It runs under the NEW binary: at the end of Phase 2b on install, and after
// the post-swap re-exec on upgrade. The pre-re-exec part of an upgrade is the
// previous release's code, which knows nothing of the helper; only a step
// compiled into this release can install it.
//
// It takes the archive lock, so the helper it copies from bin/ is the one the
// archive verified with, even while `orama node stage-archive` runs. Phase 2b,
// which already holds the lock, calls ensurePrivHelper.
func (ps *ProductionSetup) EnsurePrivHelper() (err error) {
	unlock, err := lockArchive(ps.oramaHome)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return ps.ensurePrivHelper()
}

// ensurePrivHelper is EnsurePrivHelper for a caller holding the archive lock.
func (ps *ProductionSetup) ensurePrivHelper() error {
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

	for name, content := range map[string]string{
		privhelper.SocketUnitName:  privhelper.SocketUnit,
		privhelper.ServiceUnitName: privhelper.ServiceUnit,
	} {
		path := filepath.Join(systemdUnitDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", privhelper.SocketUnitName},
		// restart, not start: an upgrade that changes the socket unit must
		// take effect now. Requests in flight run in their own instances.
		{"restart", privhelper.SocketUnitName},
	} {
		if out, err := runSystemctl(args...); err != nil {
			return fmt.Errorf("systemctl %v: %w: %s", args, err, out)
		}
	}

	// The wildcard rules the helper replaced; sudo-rs refuses to load them.
	if err := os.Remove(legacySudoersPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove the old sudoers rules %s: %w", legacySudoersPath, err)
	}

	ps.logf("  ✓ %s installed; %s listening on %s", privHelperDest, privhelper.SocketUnitName, privhelper.SocketPath)
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
