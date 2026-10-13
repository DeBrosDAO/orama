package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/legacylayout"
	"golang.org/x/sys/unix"
)

// maxSigningKeySize bounds a signing key read here; a PEM key is a few KB.
const maxSigningKeySize = 1 << 16

// RemoveCopiedSigningKeys deletes the index gateway's old signing keys from
// secrets/ once orama-node has copied them out.
//
// orama-node moves the pre-0.200 layout itself (pkg/legacylayout). The keys in
// secrets/ are the one thing it may be unable to rename, because install writes
// that directory as root; it then copies them and records each copy's digest.
// This deletes an original only when its contents match that record and the
// copy exists — so nothing is lost if the node has not copied it yet, and a
// key that changed since is left for the node to report. It touches two fixed
// names in one directory opened without following symlinks, and walks,
// renames and chowns nothing.
//
// The marker and the copy are the orama user's, so they are evidence, not
// proof: a compromised orama user could forge both and have root delete the
// two originals. That is all it could do — the names are fixed and a digest
// must match a file it can already read — and those keys are the old layout's,
// read by nothing once the node has moved.
func (ps *ProductionSetup) RemoveCopiedSigningKeys() error {
	return removeCopiedSigningKeys(ps.oramaDir, ps.logf)
}

func removeCopiedSigningKeys(oramaDir string, logf func(string, ...interface{})) error {
	copied, err := legacylayout.ReadCopiedKeys(legacylayout.CopiedKeysMarker(oramaDir))
	if err != nil {
		return fmt.Errorf("read which signing keys orama-node copied out of secrets/: %w", err)
	}
	if len(copied) == 0 {
		logf("  ✓ no copied signing keys to remove from secrets/")
		return nil
	}
	secretsDir := legacylayout.SecretsDir(oramaDir)
	dirFD, err := unix.Open(secretsDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s as a directory without following symlinks: %w", secretsDir, err)
	}
	defer unix.Close(dirFD)

	stateDir := legacylayout.IndexGatewayStateDir(oramaDir)
	for _, name := range legacylayout.SigningKeyNames {
		digest, ok := copied[name]
		if !ok {
			continue
		}
		copyPath := filepath.Join(stateDir, name)
		if _, err := os.Lstat(copyPath); errors.Is(err, os.ErrNotExist) {
			logf("  %s/%s stays: orama-node has not copied it to %s yet", secretsDir, name, stateDir)
			continue
		} else if err != nil {
			return fmt.Errorf("check for the copy %s before removing %s/%s: %w", copyPath, secretsDir, name, err)
		}
		removed, err := removeIfDigest(dirFD, secretsDir, name, digest)
		if err != nil {
			return err
		}
		if removed {
			logf("  ✓ removed %s/%s (orama-node copied it to %s)", secretsDir, name, stateDir)
		} else {
			logf("  %s/%s stays: it is gone or no longer what orama-node copied", secretsDir, name)
		}
	}
	return nil
}

// removeIfDigest unlinks name in the directory dirFD when its contents have
// the given digest. It opens the file itself with O_NOFOLLOW.
func removeIfDigest(dirFD int, dir, name, digest string) (bool, error) {
	path := filepath.Join(dir, name)
	// O_NONBLOCK: a FIFO planted under the name must not hang the upgrade;
	// the type is checked before anything is read.
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open %s without following symlinks: %w", path, err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSigningKeySize+1))
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxSigningKeySize || legacylayout.KeyDigest(data) != digest {
		return false, nil
	}
	if err := unix.Unlinkat(dirFD, name, 0); err != nil {
		return false, fmt.Errorf("remove %s: %w", path, err)
	}
	return true, nil
}
