// Package releaserepo builds a TUF repository for releases from private keys
// held in memory: a root, and the timestamp, snapshot and targets metadata
// that name release archives under their channel's path prefix.
//
// It is the generator the stagenet test root and the package tests use. The
// keys it makes are software keys that sit on disk; a production root is
// signed by release signers through their RootWallets (pkg/releasesign), not
// by this package.
package releaserepo

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// Metadata file names, as the client reads them.
const (
	RootFile      = metadata.ROOT + ".json"
	TimestampFile = metadata.TIMESTAMP + ".json"
	SnapshotFile  = metadata.SNAPSHOT + ".json"
	TargetsFile   = metadata.TARGETS + ".json"
)

// topRoles are the four roles every root names.
var topRoles = []string{metadata.ROOT, metadata.TIMESTAMP, metadata.SNAPSHOT, metadata.TARGETS}

// Keys are the private keys of a repository by role: the four top-level roles.
type Keys map[string]ed25519.PrivateKey

// GenerateKeys makes a key for each top-level role.
func GenerateKeys() (Keys, error) {
	keys := Keys{}
	for _, role := range topRoles {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate the %s key: %w", role, err)
		}
		keys[role] = key
	}
	return keys, nil
}

// Spec is one version of the repository's metadata.
type Spec struct {
	// Version is the version of the timestamp, snapshot and targets metadata.
	Version int64
	// RootValidUntil is when the root expires; the other roles default to it.
	RootValidUntil time.Time
	// TimestampExpires is when the timestamp expires. Zero is RootValidUntil;
	// a time before the client's clock is a frozen repository.
	TimestampExpires time.Time
	// Targets are the files the targets metadata names, by path (a release
	// archive's path begins with its channel), mapping a path to the file's
	// bytes.
	Targets map[string][]byte
	// Custom, by path, is the custom field a target carries (a release archive's
	// is releaseverify.ArchiveCustom); a target not listed has none.
	Custom map[string]json.RawMessage
}

// NewRoot returns the signed root.json naming the four top-level keys.
func NewRoot(keys Keys, validUntil time.Time) ([]byte, error) {
	root := metadata.Root(validUntil)
	root.Signed.ConsistentSnapshot = false
	for _, role := range topRoles {
		pub, err := publicKey(keys, role)
		if err != nil {
			return nil, err
		}
		if err := root.Signed.AddKey(pub, role); err != nil {
			return nil, fmt.Errorf("add the %s key to the root: %w", role, err)
		}
	}
	if err := sign(root, keys[metadata.ROOT]); err != nil {
		return nil, err
	}
	return toBytes(root)
}

// NextRoot returns the root that follows prev: the next version, naming next's
// four top-level keys, signed by prevKeys' root key, which is what a client
// holding prev checks, and by next's root key, which is what the new root says
// it is signed by. When both are one key it signs once.
func NextRoot(prev []byte, prevKeys, next Keys, validUntil time.Time) ([]byte, error) {
	old, err := metadata.Root().FromBytes(prev)
	if err != nil {
		return nil, fmt.Errorf("read the root to follow: %w", err)
	}
	root := metadata.Root(validUntil)
	root.Signed.Version = old.Signed.Version + 1
	root.Signed.ConsistentSnapshot = false
	for _, role := range topRoles {
		pub, err := publicKey(next, role)
		if err != nil {
			return nil, err
		}
		if err := root.Signed.AddKey(pub, role); err != nil {
			return nil, fmt.Errorf("add the %s key to the root: %w", role, err)
		}
	}
	signers := []ed25519.PrivateKey{prevKeys[metadata.ROOT]}
	if newRootKey := next[metadata.ROOT]; !newRootKey.Equal(signers[0]) {
		signers = append(signers, newRootKey)
	}
	for _, key := range signers {
		if err := sign(root, key); err != nil {
			return nil, err
		}
	}
	return toBytes(root)
}

// Build returns the timestamp, snapshot and targets metadata files for spec,
// signed with keys.
func Build(keys Keys, spec Spec) (map[string][]byte, error) {
	if spec.RootValidUntil.IsZero() {
		return nil, fmt.Errorf("a repository spec needs RootValidUntil")
	}
	tgt := metadata.Targets(spec.RootValidUntil)
	tgt.Signed.Version = spec.Version
	if err := addTargets(tgt, spec.Targets, spec.Custom); err != nil {
		return nil, err
	}
	if err := sign(tgt, keys[metadata.TARGETS]); err != nil {
		return nil, err
	}
	targets, err := toBytes(tgt)
	if err != nil {
		return nil, err
	}
	snapshot, err := snapshotFile(keys, spec, targets)
	if err != nil {
		return nil, err
	}
	timestamp, err := timestampFile(keys, spec, snapshot)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{TargetsFile: targets, SnapshotFile: snapshot, TimestampFile: timestamp}, nil
}

func addTargets(tgt *metadata.Metadata[metadata.TargetsType], targets map[string][]byte, custom map[string]json.RawMessage) error {
	for name, content := range targets {
		info, err := metadata.TargetFile().FromBytes(name, content, "sha256")
		if err != nil {
			return fmt.Errorf("describe target %s: %w", name, err)
		}
		if c, ok := custom[name]; ok {
			info.Custom = &c
		}
		tgt.Signed.Targets[name] = info
	}
	return nil
}

// snapshotFile lists the targets file with its version and hash.
func snapshotFile(keys Keys, spec Spec, targets []byte) ([]byte, error) {
	snap := metadata.Snapshot(spec.RootValidUntil)
	snap.Signed.Version = spec.Version
	snap.Signed.Meta = map[string]*metadata.MetaFiles{TargetsFile: hashed(spec.Version, targets)}
	if err := sign(snap, keys[metadata.SNAPSHOT]); err != nil {
		return nil, err
	}
	return toBytes(snap)
}

func timestampFile(keys Keys, spec Spec, snapshot []byte) ([]byte, error) {
	expires := spec.TimestampExpires
	if expires.IsZero() {
		expires = spec.RootValidUntil
	}
	ts := metadata.Timestamp(expires)
	ts.Signed.Version = spec.Version
	ts.Signed.Meta = map[string]*metadata.MetaFiles{SnapshotFile: hashed(spec.Version, snapshot)}
	if err := sign(ts, keys[metadata.TIMESTAMP]); err != nil {
		return nil, err
	}
	return toBytes(ts)
}

func hashed(version int64, data []byte) *metadata.MetaFiles {
	sum := sha256.Sum256(data)
	meta := metadata.MetaFile(version)
	meta.Length = int64(len(data))
	meta.Hashes = metadata.Hashes{"sha256": sum[:]}
	return meta
}

func publicKey(keys Keys, role string) (*metadata.Key, error) {
	key, ok := keys[role]
	if !ok {
		return nil, fmt.Errorf("there is no %s key", role)
	}
	pub, err := metadata.KeyFromPublicKey(key.Public())
	if err != nil {
		return nil, fmt.Errorf("the %s public key: %w", role, err)
	}
	return pub, nil
}

func sign[T metadata.Roles](meta *metadata.Metadata[T], key ed25519.PrivateKey) error {
	if key == nil {
		return fmt.Errorf("there is no key to sign the metadata with")
	}
	signer, err := signature.LoadSigner(key, crypto.Hash(0))
	if err != nil {
		return fmt.Errorf("load the signing key: %w", err)
	}
	if _, err := meta.Sign(signer); err != nil {
		return fmt.Errorf("sign the metadata: %w", err)
	}
	return nil
}

func toBytes[T metadata.Roles](meta *metadata.Metadata[T]) ([]byte, error) {
	data, err := meta.ToBytes(false)
	if err != nil {
		return nil, fmt.Errorf("encode the metadata: %w", err)
	}
	return data, nil
}
