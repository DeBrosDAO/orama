//go:build unix

package cosmovisor

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	// binaryPerm: root writes the binaries, the chain account runs them.
	binaryPerm = 0o755
	// stagingPerm is the root-only directory a binary is written and
	// verified in before it is linked into place.
	stagingPerm = 0o700
	// stagingFilePerm is the copy's mode until it has been written.
	stagingFilePerm = 0o600
	// stagingPrefix and stagingSuffixBytes name the staging directory.
	stagingPrefix      = ".staging-"
	stagingSuffixBytes = 8
	// createFlags make the staged copy; O_EXCL never reuses an entry.
	createFlags = unix.O_RDWR | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC
)

// StageGenesis places the genesis binary, creates upgrades/ so the unit can
// mount it read-only, and, when current does not exist yet, creates
// current -> genesis owned by the chain account, as `cosmovisor init` does.
// An existing genesis binary is refused.
func (l Layout) StageGenesis(src string, verify Verify) (dst string, err error) {
	homeFD, rootFD, err := l.openTop()
	if err != nil {
		return "", err
	}
	genesisFD, binFD := -1, -1
	defer func() { err = errors.Join(err, closeAll(homeFD, rootFD, genesisFD, binFD)) }()
	upgradesFD, err := l.openDir(rootFD, upgradesDir, true, false)
	if err != nil {
		return "", err
	}
	if err := unix.Close(upgradesFD); err != nil {
		return "", fmt.Errorf("close %s: %w", upgradesDir, err)
	}
	if genesisFD, err = l.openDir(rootFD, genesisDir, true, false); err != nil {
		return "", err
	}
	if binFD, err = l.openDir(genesisFD, binDir, true, false); err != nil {
		return "", err
	}
	dst = l.GenesisBinary()
	if err := l.place(rootFD, binFD, src, dst, verify); err != nil {
		return "", err
	}
	return dst, l.linkCurrent(rootFD)
}

// StageUpgrade places the binary for the upgrade plan name and the
// upgrade-info.json link cosmovisor writes through. A binary already staged
// for name is refused: a staged upgrade is replaced by removing it on
// purpose, not by staging over it.
func (l Layout) StageUpgrade(name, src string, verify Verify) (dst string, err error) {
	if err := checkUpgradeName(name); err != nil {
		return "", err
	}
	homeFD, rootFD, err := l.openTop()
	if err != nil {
		return "", err
	}
	upgradesFD, nameFD, binFD := -1, -1, -1
	defer func() { err = errors.Join(err, closeAll(homeFD, rootFD, upgradesFD, nameFD, binFD)) }()
	if upgradesFD, err = l.openDir(rootFD, upgradesDir, true, false); err != nil {
		return "", err
	}
	nameExisted, err := entryExists(upgradesFD, name)
	if err != nil {
		return "", err
	}
	if nameFD, err = l.openDir(upgradesFD, name, true, false); err != nil {
		return "", err
	}
	binExisted, err := entryExists(nameFD, binDir)
	if err != nil {
		return "", err
	}
	if binFD, err = l.openDir(nameFD, binDir, true, false); err != nil {
		return "", err
	}
	dst, _ = l.UpgradeBinary(name)
	if err := l.place(rootFD, binFD, src, dst, verify); err != nil {
		// A refused stage leaves no upgrade directory behind: cosmovisor
		// treats upgrades/<name> as a staged upgrade.
		if !binExisted {
			err = errors.Join(err, removeEmptyDir(nameFD, binDir))
		}
		if !nameExisted {
			err = errors.Join(err, removeEmptyDir(upgradesFD, name))
		}
		return "", err
	}
	return dst, linkUpgradeInfo(nameFD, name)
}

// openTop opens the home and cosmovisor/ after checking the layout.
func (l Layout) openTop() (homeFD, rootFD int, err error) {
	if err := l.check(); err != nil {
		return -1, -1, err
	}
	if homeFD, err = l.openHome(); err != nil {
		return -1, -1, err
	}
	if rootFD, err = l.openRoot(homeFD); err != nil {
		return -1, -1, errors.Join(err, unix.Close(homeFD))
	}
	return homeFD, rootFD, nil
}

// place writes src into a fresh root-only staging directory under
// cosmovisor/, syncs it, sets its mode through the descriptor, verifies
// that descriptor, and hard-links it into binFD. The link fails if the
// binary already exists, so nothing is ever replaced.
func (l Layout) place(rootFD, binFD int, src, dst string, verify Verify) (err error) {
	if verify == nil {
		return fmt.Errorf("staging %s needs a verification", dst)
	}
	if err := refuseExisting(binFD, l.Daemon, dst); err != nil {
		return err
	}
	stageName, stageFD, err := makeStaging(rootFD)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, unlinkIfPresent(stageFD, l.Daemon), unix.Close(stageFD),
			unix.Unlinkat(rootFD, stageName, unix.AT_REMOVEDIR))
	}()
	f, err := writeStaged(stageFD, l.Daemon, src)
	if err != nil {
		return err
	}
	verr := verify(f)
	if err := errors.Join(verr, f.Close()); err != nil {
		return fmt.Errorf("%s did not verify, nothing was staged: %w", src, err)
	}
	if err := unix.Linkat(stageFD, l.Daemon, binFD, l.Daemon, 0); err != nil {
		return fmt.Errorf("link the verified binary to %s: %w", dst, err)
	}
	if err := unix.Fsync(binFD); err != nil {
		return fmt.Errorf("sync %s: %w", filepath.Dir(dst), err)
	}
	return nil
}

// makeStaging creates and opens a randomly named 0700 directory.
func makeStaging(rootFD int) (string, int, error) {
	suffix := make([]byte, stagingSuffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		return "", -1, fmt.Errorf("name a staging directory: %w", err)
	}
	name := stagingPrefix + hex.EncodeToString(suffix)
	if err := unix.Mkdirat(rootFD, name, stagingPerm); err != nil {
		return "", -1, fmt.Errorf("create the staging directory %s: %w", name, err)
	}
	fd, err := unix.Openat(rootFD, name, dirFlags, 0)
	if err != nil {
		return "", -1, errors.Join(fmt.Errorf("open the staging directory %s: %w", name, err),
			unix.Unlinkat(rootFD, name, unix.AT_REMOVEDIR))
	}
	return name, fd, nil
}

// writeStaged copies src into a new file in stageFD, syncs it and gives it
// binaryPerm through its descriptor. The file is returned open.
func writeStaged(stageFD int, name, src string) (*os.File, error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	fd, err := unix.Openat(stageFD, name, createFlags, stagingFilePerm)
	if err != nil {
		return nil, fmt.Errorf("create the staged copy of %s: %w", src, err)
	}
	f := os.NewFile(uintptr(fd), name)
	_, cerr := io.Copy(f, in)
	if err := errors.Join(cerr, f.Sync(), f.Chmod(binaryPerm)); err != nil {
		return nil, errors.Join(fmt.Errorf("copy %s: %w", src, err), f.Close())
	}
	return f, nil
}

// linkCurrent creates current -> genesis when there is no current, owned by
// the chain account so cosmovisor can replace it. An existing current is
// cosmovisor's and is left alone.
func (l Layout) linkCurrent(rootFD int) error {
	err := unix.Symlinkat(genesisDir, rootFD, currentLink)
	if errors.Is(err, unix.EEXIST) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("point %s at %s: %w", l.Current(), genesisDir, err)
	}
	if err := unix.Fchownat(rootFD, currentLink, l.ChainUID, l.ChainGID, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("give %s to the chain account: %w", l.Current(), err)
	}
	return nil
}

// linkUpgradeInfo places upgrades/<name>/upgrade-info.json as a link into
// the chain home, or accepts the same link from an earlier run.
func linkUpgradeInfo(nameFD int, name string) error {
	target := upgradeInfoToHome + fmt.Sprintf(upgradeInfoTargetFormat, name)
	err := unix.Symlinkat(target, nameFD, upgradeInfoName)
	if !errors.Is(err, unix.EEXIST) {
		if err != nil {
			return fmt.Errorf("link %s/%s: %w", name, upgradeInfoName, err)
		}
		return nil
	}
	buf := make([]byte, len(target)+1)
	n, err := unix.Readlinkat(nameFD, upgradeInfoName, buf)
	if err != nil {
		return fmt.Errorf("read %s/%s: %w", name, upgradeInfoName, err)
	}
	if string(buf[:n]) != target {
		return fmt.Errorf("%s/%s exists and is not the link to %s", name, upgradeInfoName, target)
	}
	return nil
}

// unlinkIfPresent removes a file that a failed step may not have created.
func unlinkIfPresent(dirFD int, name string) error {
	if err := unix.Unlinkat(dirFD, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("remove the staged %s: %w", name, err)
	}
	return nil
}

// entryExists reports whether name is present in dirFD, without following a
// symlink.
func entryExists(dirFD int, name string) (bool, error) {
	var st unix.Stat_t
	err := unix.Fstatat(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", name, err)
	}
	return true, nil
}

// removeEmptyDir removes a directory this call created.
func removeEmptyDir(parentFD int, name string) error {
	if err := unix.Unlinkat(parentFD, name, unix.AT_REMOVEDIR); err != nil {
		return fmt.Errorf("remove the empty %s: %w", name, err)
	}
	return nil
}
