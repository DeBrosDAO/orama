package agent

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	bip39 "github.com/cosmos/go-bip39"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

const (
	// passwordBytes is the entropy of the wallet password.
	passwordBytes = 32
	// mnemonicEntropyBits gives a 12-word BIP-39 phrase.
	mnemonicEntropyBits = 128
	// secretFileMode is the mode of every secret file.
	secretFileMode = 0o600
	// rootwalletDirName is where RootWallet keeps a wallet, under a home.
	rootwalletDirName = ".rootwallet"
)

// lookupRealHome is the account's home from the user database, not $HOME
// (which the harness overrides). A variable so tests can point it elsewhere.
var lookupRealHome = func() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to look up the current user's home: %w", err)
	}
	return u.HomeDir, nil
}

// geteuid is the effective uid; a variable so tests can play root.
var geteuid = os.Geteuid

// newPassword is a random wallet password.
func newPassword() (string, error) {
	buf := make([]byte, passwordBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate the wallet password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// newMnemonic is a random 12-word BIP-39 phrase.
func newMnemonic() (string, error) {
	entropy, err := bip39.NewEntropy(mnemonicEntropyBits)
	if err != nil {
		return "", fmt.Errorf("failed to generate mnemonic entropy: %w", err)
	}
	m, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", fmt.Errorf("failed to encode the mnemonic: %w", err)
	}
	return m, nil
}

// writeSecret creates path (which must not exist, and is never followed as a
// symlink) with mode 0600 and content.
func writeSecret(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, secretFileMode)
	if err != nil {
		return fmt.Errorf("failed to create secret file %s: %w", path, err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return fmt.Errorf("failed to write secret file %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close secret file %s: %w", path, err)
	}
	return nil
}

// shredFile overwrites a regular file with zeros, syncs and removes it. A
// missing file is already gone; a symlink is removed without following it.
func shredFile(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", path, err)
	}
	if fi.Mode().IsRegular() {
		if err := zeroFile(path, fi.Size()); err != nil {
			return err
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to remove %s: %w", path, err)
	}
	return nil
}

func zeroFile(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("failed to open %s for shredding: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(make([]byte, size)); err != nil {
		return fmt.Errorf("failed to overwrite %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("failed to sync %s: %w", path, err)
	}
	return nil
}

// shredTree shreds every regular file under dir, then removes dir. A missing
// dir is already gone.
func shredTree(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			return shredFile(path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to shred %s: %w", dir, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("failed to remove %s: %w", dir, err)
	}
	return nil
}

// resolve follows symlinks in p as far as it exists.
func resolve(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// within reports whether p is dir or inside it.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// checkIsolation refuses running as root, and a dir that is inside the real
// ~/.rootwallet or holds it: the throwaway wallet must never touch the
// operator's.
func checkIsolation(dir string) error {
	if geteuid() == 0 {
		return errors.New("refusing to run the test RootWallet agent as root (rw-agent-headless refuses it too)")
	}
	home, err := lookupRealHome()
	if err != nil {
		return err
	}
	real := resolve(filepath.Join(home, rootwalletDirName))
	d := resolve(dir)
	// By name after resolving links, and by file identity (os.SameFile up
	// each ancestor), so a hard link or a case-folded path cannot slip by.
	inside, err := secrets.InsideByIdentity(d, real)
	if err != nil {
		return fmt.Errorf("cannot check agent dir %s against the real RootWallet directory: %w", dir, err)
	}
	holds, err := secrets.InsideByIdentity(real, d)
	if err != nil {
		return fmt.Errorf("cannot check agent dir %s against the real RootWallet directory: %w", dir, err)
	}
	if inside || holds || within(d, real) || within(real, d) {
		return fmt.Errorf("refusing agent dir %s: the real RootWallet directory %s is inside it or holds it", dir, real)
	}
	return nil
}
