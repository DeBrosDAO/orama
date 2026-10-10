// Package releaserepo builds a TUF repository for releases from private keys
// held in memory: a root, and the timestamp, snapshot and targets metadata
// that name release archives under delegated channel roles.
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
	"fmt"
	"sort"
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

// Keys are the private keys of a repository by role: the four top-level roles
// and one per delegated role.
type Keys map[string]ed25519.PrivateKey

// GenerateKeys makes a key for each top-level role and for each of delegated.
func GenerateKeys(delegated ...string) (Keys, error) {
	keys := Keys{}
	for _, role := range append(append([]string{}, topRoles...), delegated...) {
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
	// Version is the version of the timestamp, snapshot and top-level targets
	// metadata; a delegated role has Version too unless RoleVersions says
	// otherwise.
	Version      int64
	RoleVersions map[string]int64
	// RootValidUntil is when the root expires; the other roles default to it.
	RootValidUntil time.Time
	// TimestampExpires is when the timestamp expires. Zero is RootValidUntil;
	// a time before the client's clock is a frozen repository.
	TimestampExpires time.Time
	// Delegated lists the delegated roles, each trusted for "<role>/*". Every
	// role in it needs a key in Keys.
	Delegated []string
	// Targets are the top-level targets by path, and ChannelTargets those of
	// each delegated role, both mapping a path to the file's bytes.
	Targets        map[string][]byte
	ChannelTargets map[string]map[string][]byte
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

// Build returns the timestamp, snapshot, top-level targets and delegated
// metadata files for spec, signed with keys.
func Build(keys Keys, spec Spec) (map[string][]byte, error) {
	if spec.RootValidUntil.IsZero() {
		return nil, fmt.Errorf("a repository spec needs RootValidUntil")
	}
	files := map[string][]byte{}
	top, err := topTargets(keys, spec)
	if err != nil {
		return nil, err
	}
	files[TargetsFile] = top
	for _, role := range spec.Delegated {
		raw, err := roleTargets(keys, spec, role)
		if err != nil {
			return nil, err
		}
		files[role+".json"] = raw
	}
	snapshot, err := snapshotFile(keys, spec, files)
	if err != nil {
		return nil, err
	}
	files[SnapshotFile] = snapshot
	timestamp, err := timestampFile(keys, spec, snapshot)
	if err != nil {
		return nil, err
	}
	files[TimestampFile] = timestamp
	return files, nil
}

func topTargets(keys Keys, spec Spec) ([]byte, error) {
	tgt := metadata.Targets(spec.RootValidUntil)
	tgt.Signed.Version = spec.Version
	if err := addTargets(tgt, spec.Targets); err != nil {
		return nil, err
	}
	if len(spec.Delegated) > 0 {
		delegations, err := delegations(keys, spec.Delegated)
		if err != nil {
			return nil, err
		}
		tgt.Signed.Delegations = delegations
	}
	if err := sign(tgt, keys[metadata.TARGETS]); err != nil {
		return nil, err
	}
	return toBytes(tgt)
}

// delegations trusts each role for the paths under its own name.
func delegations(keys Keys, roles []string) (*metadata.Delegations, error) {
	d := &metadata.Delegations{Keys: map[string]*metadata.Key{}}
	for _, role := range roles {
		pub, err := publicKey(keys, role)
		if err != nil {
			return nil, err
		}
		id, err := pub.ID()
		if err != nil {
			return nil, fmt.Errorf("the %s key's id: %w", role, err)
		}
		d.Keys[id] = pub
		d.Roles = append(d.Roles, metadata.DelegatedRole{
			Name: role, KeyIDs: []string{id}, Threshold: 1, Terminating: true, Paths: []string{role + "/*"},
		})
	}
	return d, nil
}

func roleTargets(keys Keys, spec Spec, role string) ([]byte, error) {
	tgt := metadata.Targets(spec.RootValidUntil)
	tgt.Signed.Version = spec.Version
	if v, ok := spec.RoleVersions[role]; ok {
		tgt.Signed.Version = v
	}
	if err := addTargets(tgt, spec.ChannelTargets[role]); err != nil {
		return nil, err
	}
	if err := sign(tgt, keys[role]); err != nil {
		return nil, err
	}
	return toBytes(tgt)
}

func addTargets(tgt *metadata.Metadata[metadata.TargetsType], targets map[string][]byte) error {
	for name, content := range targets {
		info, err := metadata.TargetFile().FromBytes(name, content, "sha256")
		if err != nil {
			return fmt.Errorf("describe target %s: %w", name, err)
		}
		tgt.Signed.Targets[name] = info
	}
	return nil
}

// snapshotFile lists every targets file in files with its version and hash.
func snapshotFile(keys Keys, spec Spec, files map[string][]byte) ([]byte, error) {
	snap := metadata.Snapshot(spec.RootValidUntil)
	snap.Signed.Version = spec.Version
	snap.Signed.Meta = map[string]*metadata.MetaFiles{}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		version := spec.Version
		if v, ok := spec.RoleVersions[name[:len(name)-len(".json")]]; ok {
			version = v
		}
		snap.Signed.Meta[name] = hashed(version, files[name])
	}
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
