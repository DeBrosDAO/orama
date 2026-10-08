package releaseverify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/trustedmetadata"
)

// rootFilePerm: a node's services read the release root, only root writes it.
const (
	rootFilePerm = 0o644
	// rootDirPerm is /etc/orama when adopting a root creates it.
	rootDirPerm = 0o755
)

// ValidateRoot checks that data is a TUF root a client can use at now: it
// parses, it is signed by its own root keys at the root threshold, it is not
// expired and it names the timestamp, snapshot and targets roles. It returns
// the SHA-256 of data in hex, the name a root is known by.
//
// A root that passes this is only well-formed. Whether to trust it is the
// caller's decision, made out of band; nothing here learns it from a network.
func ValidateRoot(data []byte, now time.Time) (string, error) {
	trusted, err := trustedmetadata.New(data)
	if err != nil {
		return "", fmt.Errorf("not a usable TUF root: %w", err)
	}
	root := trusted.Root.Signed
	if root.IsExpired(now.UTC()) {
		return "", fmt.Errorf("the TUF root expired at %s", root.Expires.UTC().Format(time.RFC3339))
	}
	for _, role := range []string{metadata.TIMESTAMP, metadata.SNAPSHOT, metadata.TARGETS} {
		r, ok := root.Roles[role]
		if !ok || len(r.KeyIDs) == 0 || r.Threshold < 1 {
			return "", fmt.Errorf("the TUF root does not name keys for the %s role", role)
		}
	}
	return RootDigest(data), nil
}

// RootDigest is the SHA-256 of a root's bytes in hex.
func RootDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// AdoptRoot makes data the release root at path, after ValidateRoot, and
// reports whether that changed the file. Adopting the root already there
// changes nothing.
func AdoptRoot(path string, data []byte, now time.Time) (bool, error) {
	if _, err := ValidateRoot(data, now); err != nil {
		return false, err
	}
	current, err := readLimited(path)
	switch {
	case err == nil && bytes.Equal(current, data):
		return false, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return false, fmt.Errorf("read the adopted release root: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), rootDirPerm); err != nil {
		return false, fmt.Errorf("create the directory of the release root: %w", err)
	}
	if err := writeFileAtomic(path, data, rootFilePerm); err != nil {
		return false, fmt.Errorf("adopt the release root: %w", err)
	}
	return true, nil
}

// ReadRoot returns the adopted release root at path, or an error wrapping
// ErrNoRoot when there is none.
func ReadRoot(path string) ([]byte, error) {
	data, err := readLimited(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s does not exist", ErrNoRoot, path)
	}
	if err != nil {
		return nil, fmt.Errorf("read the release root: %w", err)
	}
	return data, nil
}
