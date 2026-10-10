package globalnode

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

// Files root keeps in the global state root, where no service account can
// write.
const (
	// signFloorFile is the state a migrated key last signed on its source
	// host. The chain does not start below it.
	signFloorFile = "validator-sign-floor.json"
	// recipientKeyFile is the X25519 private key a migration is sealed to.
	recipientKeyFile = "migrate-recipient.key"
	// quarantinePrefix names a key or state copy root keeps in the state root.
	quarantinePrefix = "validator-"
	// copySuffixBytes of randomness make each copy's name unique.
	copySuffixBytes = 8
	// secretMode is every key file root writes.
	secretMode = 0o600
)

// Host is where the validator files are. DefaultHost is the real node.
type Host struct {
	// Root is an anchor only root may write, above StateDir.
	Root      rootfs.Root
	StateDir  string
	KeyPath   string
	StatePath string
	Lookup    func(account string) (uid, gid int, err error)
	Chown     func(r rootfs.Root, path string, uid, gid int) error
	Now       func() time.Time
	// StateOwner is the uid StateDir must belong to: root on a node.
	StateOwner int
}

// DefaultHost is this node's chain home under /var/lib/orama-global.
func DefaultHost() Host {
	return Host{
		Root:       rootfs.At(filepath.Dir(constants.GlobalStateRoot)),
		StateDir:   constants.GlobalStateRoot,
		KeyPath:    constants.ChainValidatorKeyPath,
		StatePath:  constants.ChainValidatorStatePath,
		Lookup:     cosmovisor.LookupAccount,
		Chown:      func(r rootfs.Root, path string, uid, gid int) error { return r.Chown(path, uid, gid) },
		Now:        time.Now,
		StateOwner: 0,
	}
}

func (h Host) floorPath() string     { return filepath.Join(h.StateDir, signFloorFile) }
func (h Host) recipientPath() string { return filepath.Join(h.StateDir, recipientKeyFile) }

func (h Host) read(path string) ([]byte, error) {
	data, err := h.Root.ReadFile(path, rootfs.SmallFileLimit)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// ExportKey seals priv_validator_key.json to recipient. The node holds only
// the recipient's public key, so it cannot open what it wrote.
func (h Host) ExportKey(recipient *[32]byte) ([]byte, error) {
	key, err := h.read(h.KeyPath)
	if err != nil {
		return nil, err
	}
	if _, err := ValidatorKeyPubKey(key); err != nil {
		return nil, err
	}
	return nsbackup.Seal(recipient, key)
}

// PrepareMigration returns the public half of the key a migration to this
// host is sealed to, creating it on the first call. The private half stays
// in the state root, root's, mode 0600, until ImportMigration removes it.
func (h Host) PrepareMigration() (*[32]byte, error) {
	if err := h.checkStateDir(); err != nil {
		return nil, err
	}
	existing, err := h.Root.ReadFile(h.recipientPath(), rootfs.SmallFileLimit)
	if err == nil {
		priv, err := ParseX25519Hex(string(existing))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", h.recipientPath(), err)
		}
		return publicKey(priv)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", h.recipientPath(), err)
	}
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate the migration key: %w", err)
	}
	if err := h.Root.WriteFile(h.recipientPath(), []byte(hex.EncodeToString(priv[:])), secretMode); err != nil {
		return nil, fmt.Errorf("write %s: %w", h.recipientPath(), err)
	}
	return pub, nil
}

func publicKey(priv *[32]byte) (*[32]byte, error) {
	out, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("derive the migration public key: %w", err)
	}
	var pub [32]byte
	copy(pub[:], out)
	return &pub, nil
}

// quarantineKey moves priv_validator_key.json out of the chain home into the
// state root, where the chain account cannot reach it, and returns where it
// went.
func (h Host) quarantineKey(kind string) (string, error) {
	key, err := h.read(h.KeyPath)
	if err != nil {
		return "", err
	}
	dst, err := h.keepCopy(key, kind)
	if err != nil {
		return "", err
	}
	if err := h.Root.Remove(h.KeyPath); err != nil {
		return "", fmt.Errorf("remove %s after copying it to %s: %w", h.KeyPath, dst, err)
	}
	return dst, nil
}

// keepCopy writes data to a new file in the state root, named
// validator-<kind>-<unix time>-<random>.json. The file is created with
// O_EXCL and O_NOFOLLOW, so an existing entry is never replaced. The state
// root is root's and no service account can write it.
func (h Host) keepCopy(data []byte, kind string) (string, error) {
	if err := h.checkStateDir(); err != nil {
		return "", err
	}
	suffix := make([]byte, copySuffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("name a copy in %s: %w", h.StateDir, err)
	}
	name := quarantinePrefix + kind + "-" + strconv.FormatInt(h.Now().Unix(), 10) + "-" + hex.EncodeToString(suffix) + ".json"
	dst := filepath.Join(h.StateDir, name)
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, secretMode)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", fmt.Errorf("write %s: %w", dst, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", fmt.Errorf("sync %s: %w", dst, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close %s: %w", dst, err)
	}
	return dst, nil
}

// CancelMigration removes this host's migration key, if there is one. It
// reports whether a key was removed.
func (h Host) CancelMigration() (bool, error) {
	if err := h.checkStateDir(); err != nil {
		return false, err
	}
	err := h.Root.Remove(h.recipientPath())
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("remove %s: %w", h.recipientPath(), err)
	}
	return true, nil
}
