// Package cosmovisor lays out the chain binary the way cosmovisor runs it:
//
//	<home>/cosmovisor/genesis/bin/oramad
//	<home>/cosmovisor/upgrades/<name>/bin/oramad
//	<home>/cosmovisor/current -> genesis or upgrades/<name>
//
// It places binaries and nothing else. It does not import cosmovisor, and
// it never repoints current once it exists: cosmovisor moves current at the
// upgrade height. Every binary it places is verified by the caller's check
// on the copy it is about to publish, so the bytes checked are the bytes
// renamed into place.
package cosmovisor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// Directory names inside DAEMON_HOME, fixed by cosmovisor.
const (
	rootDir     = "cosmovisor"
	genesisDir  = "genesis"
	upgradesDir = "upgrades"
	binDir      = "bin"
	currentLink = "current"

	// dirPerm and binaryPerm: root writes the binaries, the chain account
	// reads and runs them.
	dirPerm    fs.FileMode = 0o755
	binaryPerm fs.FileMode = 0o755
	// stagingPattern names the temporary copy beside the final binary.
	stagingPattern = ".staging-*"
)

// upgradeName is a plan name cosmovisor maps to itself: cosmovisor
// lowercases the name and URI-escapes it, so only lowercase letters,
// digits, dot, dash and underscore name the same directory on both sides.
var upgradeName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Layout is one DAEMON_HOME.
type Layout struct {
	Home   string
	Daemon string
	// Chown gives a directory cosmovisor writes (the cosmovisor root, for
	// current, and an upgrade directory, for upgrade-info.json) to the
	// chain account.
	Chown func(path string) error
}

// Root is <home>/cosmovisor.
func (l Layout) Root() string { return filepath.Join(l.Home, rootDir) }

// GenesisBinary is the binary cosmovisor runs before any upgrade.
func (l Layout) GenesisBinary() string {
	return filepath.Join(l.Root(), genesisDir, binDir, l.Daemon)
}

// UpgradeBinary is the binary cosmovisor switches to for plan name.
func (l Layout) UpgradeBinary(name string) (string, error) {
	if !upgradeName.MatchString(name) {
		return "", fmt.Errorf("upgrade name %q must be 1-64 lowercase letters, digits, '.', '-' or '_', starting with a letter or digit", name)
	}
	return filepath.Join(l.Root(), upgradesDir, name, binDir, l.Daemon), nil
}

// Current is the symlink cosmovisor runs through.
func (l Layout) Current() string { return filepath.Join(l.Root(), currentLink) }

// StageGenesis places the genesis binary and, when current does not exist
// yet, points it at genesis as `cosmovisor init` does.
// An existing genesis binary is refused, as a staged upgrade is.
func (l Layout) StageGenesis(src string, verify func(path string) error) (string, error) {
	dst := l.GenesisBinary()
	if err := refuseExisting(dst); err != nil {
		return "", err
	}
	if err := l.ensureRoot(); err != nil {
		return "", err
	}
	if err := place(src, dst, verify); err != nil {
		return "", err
	}
	if _, err := os.Lstat(l.Current()); err == nil {
		return dst, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("stat %s: %w", l.Current(), err)
	}
	if err := os.Symlink(genesisDir, l.Current()); err != nil {
		return "", fmt.Errorf("point %s at %s: %w", l.Current(), genesisDir, err)
	}
	return dst, nil
}

// StageUpgrade places the binary for the upgrade plan name. A binary
// already staged for name is refused: a staged upgrade is replaced by
// removing it on purpose, not by staging over it.
func (l Layout) StageUpgrade(name, src string, verify func(path string) error) (string, error) {
	dst, err := l.UpgradeBinary(name)
	if err != nil {
		return "", err
	}
	if err := refuseExisting(dst); err != nil {
		return "", err
	}
	if err := l.ensureRoot(); err != nil {
		return "", err
	}
	upgradeDir := filepath.Dir(filepath.Dir(dst))
	if err := os.MkdirAll(upgradeDir, dirPerm); err != nil {
		return "", fmt.Errorf("create %s: %w", upgradeDir, err)
	}
	if err := l.Chown(upgradeDir); err != nil {
		return "", err
	}
	if err := place(src, dst, verify); err != nil {
		return "", err
	}
	return dst, nil
}

// refuseExisting refuses to stage over a binary that is already there.
func refuseExisting(dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s is already staged; remove it first to stage a different binary", dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", dst, err)
	}
	return nil
}

// ensureRoot creates the cosmovisor root and hands it to the chain account,
// which moves current inside it.
func (l Layout) ensureRoot() error {
	if l.Home == "" || l.Daemon == "" || l.Chown == nil {
		return fmt.Errorf("a cosmovisor layout needs a home, a daemon name and a chown")
	}
	if err := os.MkdirAll(l.Root(), dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", l.Root(), err)
	}
	return l.Chown(l.Root())
}

// place copies src next to dst, runs verify on that copy, and renames it
// into place, so what was verified is what is published.
func place(src, dst string, verify func(path string) error) error {
	if verify == nil {
		return fmt.Errorf("staging %s needs a verification", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
	}
	tmp, err := copyBeside(src, dst)
	if err != nil {
		return err
	}
	if err := verify(tmp); err != nil {
		return errors.Join(fmt.Errorf("%s did not verify, nothing was staged: %w", src, err), os.Remove(tmp))
	}
	if err := os.Rename(tmp, dst); err != nil {
		return errors.Join(fmt.Errorf("rename %s to %s: %w", tmp, dst, err), os.Remove(tmp))
	}
	return nil
}

// copyBeside writes src to a synced temporary file in dst's directory.
func copyBeside(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), stagingPattern)
	if err != nil {
		return "", fmt.Errorf("create a staging file for %s: %w", dst, err)
	}
	_, cerr := io.Copy(out, in)
	serr := out.Sync()
	xerr := out.Close()
	if err := errors.Join(cerr, serr, xerr, os.Chmod(out.Name(), binaryPerm)); err != nil {
		return "", errors.Join(fmt.Errorf("copy %s: %w", src, err), os.Remove(out.Name()))
	}
	return out.Name(), nil
}
