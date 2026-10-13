package sshx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// knownHostsMode is the mode of a known_hosts file this package writes.
const knownHostsMode = 0o600

// lockSuffix names the sidecar file whose flock serialises every change to
// a known_hosts file: the provisioner, the broker child and feature
// processes pin and unpin hosts of the same run concurrently.
const lockSuffix = ".lock"

// AppendKnownHost pins key for host in the known_hosts file at path,
// creating it with mode 0600. A symlink at path is refused, never followed.
func AppendKnownHost(path, host string, key ssh.PublicKey) error {
	line := knownhosts.Line([]string{knownhosts.Normalize(address(host))}, key)
	return editKnownHosts(path, func(lines []string) []string { return append(lines, line) })
}

// RemoveKnownHost drops every pinned key of host from the known_hosts file at
// path. A missing file has nothing to drop.
func RemoveKnownHost(path, host string) error {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	prefix := knownhosts.Normalize(address(host)) + " "
	return editKnownHosts(path, func(lines []string) []string {
		var kept []string
		for _, l := range lines {
			if !strings.HasPrefix(l, prefix) {
				kept = append(kept, l)
			}
		}
		return kept
	})
}

// editKnownHosts rewrites the file at path with edit applied to its
// non-empty lines, under the sidecar lock, through a temporary file renamed
// over it: a reader sees the old file or the new one, never a torn one, and
// two writers never lose each other's change.
func editKnownHosts(path string, edit func([]string) []string) error {
	unlock, err := lockKnownHosts(path)
	if err != nil {
		return err
	}
	defer unlock()
	lines, err := readKnownHosts(path)
	if err != nil {
		return err
	}
	out := strings.Join(edit(lines), "\n")
	if out != "" {
		out += "\n"
	}
	return replaceFile(path, []byte(out))
}

// lockKnownHosts takes the exclusive flock of path's sidecar lock file.
func lockKnownHosts(path string) (func(), error) {
	f, err := os.OpenFile(path+lockSuffix, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, knownHostsMode)
	if err != nil {
		return nil, fmt.Errorf("failed to open the known_hosts lock %s%s: %w", path, lockSuffix, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, errors.Join(fmt.Errorf("failed to lock known_hosts %s: %w", path, err), f.Close())
	}
	// Closing the file releases the lock.
	return func() { _ = f.Close() }, nil
}

// readKnownHosts returns the non-empty lines of path; a missing file has
// none, a symlink is refused.
func readKnownHosts(path string) ([]string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to inspect known_hosts %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("known_hosts %s is not a regular file (%s); refusing to follow or replace it", path, info.Mode())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read known_hosts %s: %w", path, err)
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// replaceFile writes data to a temporary file beside path (0600) and
// renames it over path.
func replaceFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("failed to create a temporary known_hosts beside %s: %w", path, err)
	}
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Chmod(knownHostsMode), tmp.Sync(), tmp.Close()); err != nil {
		return errors.Join(fmt.Errorf("failed to write known_hosts %s: %w", tmp.Name(), err), os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(fmt.Errorf("failed to replace known_hosts %s: %w", path, err), os.Remove(tmp.Name()))
	}
	return nil
}
