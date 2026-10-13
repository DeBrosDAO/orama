package releaserepo

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// keyFileSuffix names a role's key file in a keys directory.
const keyFileSuffix = ".key"

// keyFilePerm: only the owner reads a private key.
const keyFilePerm = 0o600

// Save writes each key to dir as <role>.key, the hex of its 32-byte seed.
// It refuses to replace a key file: losing a root key to a second init is
// not recoverable.
func (k Keys) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the key directory %s: %w", dir, err)
	}
	for role, key := range k {
		path := filepath.Join(dir, role+keyFileSuffix)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, keyFilePerm)
		if err != nil {
			return fmt.Errorf("write the %s key: %w", role, err)
		}
		_, werr := f.WriteString(hex.EncodeToString(key.Seed()) + "\n")
		if err := f.Close(); werr == nil {
			werr = err
		}
		if werr != nil {
			return fmt.Errorf("write the %s key to %s: %w", role, path, werr)
		}
	}
	return nil
}

// LoadKeys reads every <role>.key in dir.
func LoadKeys(dir string) (Keys, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*"+keyFileSuffix))
	if err != nil {
		return nil, fmt.Errorf("list the key directory %s: %w", dir, err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s holds no %s files", dir, keyFileSuffix)
	}
	keys := Keys{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		seed, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("%s is not the hex of a %d-byte seed", path, ed25519.SeedSize)
		}
		keys[strings.TrimSuffix(filepath.Base(path), keyFileSuffix)] = ed25519.NewKeyFromSeed(seed)
	}
	return keys, nil
}
