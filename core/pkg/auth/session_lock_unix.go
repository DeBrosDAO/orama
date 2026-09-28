//go:build unix

package auth

import (
	"fmt"
	"os"
	"syscall"
)

// credentialLockSuffix names the lock file beside the credential file. It is a
// file of its own rather than the credential file because Save rewrites that
// one, and a lock should not depend on how its data is written.
const credentialLockSuffix = ".lock"

// credentialLockPerm matches the credential file: the lock says nothing, but
// nobody else has any business holding it.
const credentialLockPerm = 0o600

// lockCredentialFile takes the exclusive lock every process shares around
// reading, renewing and writing back a stored session, and waits for a holder
// to finish. sessionMu cannot do this: it is per process, and `orama monitor`
// in one terminal and any other command in another are two processes renewing
// the same refresh token. A holder keeps it for one refresh at most, which
// sessionHTTPTimeout bounds; the kernel drops it if the holder dies.
func lockCredentialFile() (func(), error) {
	credPath, err := GetCredentialsPath()
	if err != nil {
		return nil, err
	}
	path := credPath + credentialLockSuffix
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, credentialLockPerm)
	if err != nil {
		return nil, fmt.Errorf("open the credential lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	// Closing the descriptor releases the lock whether or not Close reports an
	// error, so an error here cannot leave the lock held; it is still said.
	return func() {
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: closing the credential lock %s: %v\n", path, err)
		}
	}, nil
}
