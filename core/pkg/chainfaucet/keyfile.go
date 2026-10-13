package chainfaucet

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// CreateKeyFile creates the faucet key file at path for the account uid:gid, which is the account the
// gateway runs as, and returns the key. It never replaces a key: a file that is there is read
// back (so running `orama maint faucet init` twice prints the same address), and it is an error
// if that file is not a key. The file is created exclusively with mode 0600 and handed to
// uid:gid before it is written, so the secret is never in a file anyone else can open.
func CreateKeyFile(path string, uid, gid int) (key *Key, created bool, err error) {
	existing, err := readExisting(path)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}
	if key, err = NewKey(); err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, false, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, KeyFileMode)
	if err != nil {
		return nil, false, fmt.Errorf("create faucet key file %s: %w", path, err)
	}
	if err := writeKey(f, key, uid, gid); err != nil {
		_ = os.Remove(path)
		return nil, false, fmt.Errorf("write faucet key file %s: %w", path, err)
	}
	return key, true, nil
}

// readExisting reads the key already at path, or returns nil when there is no file. A symlink or
// a file that is not a key is an error: init does not print the account of a key the gateway
// would refuse to load, and does not write over a file it did not make.
func readExisting(path string) (*Key, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("%s is a symlink: a symlink is not trusted with a secret (move it away to make a key there)", path)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	content, err := readBounded(f, path)
	if err != nil {
		return nil, err
	}
	key, err := ParseKey(content)
	if err != nil {
		return nil, fmt.Errorf("%s exists and is not a faucet key: %w (move it away to make a new one)", path, err)
	}
	return key, nil
}

func writeKey(f *os.File, key *Key, uid, gid int) error {
	defer f.Close()
	if err := f.Chown(uid, gid); err != nil {
		return fmt.Errorf("give the file to uid %d: %w", uid, err)
	}
	if _, err := f.Write(key.encode()); err != nil {
		return err
	}
	return f.Sync()
}
