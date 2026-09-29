// Package releasetest builds TUF repositories for tests. Every key is
// generated when a test runs; nothing here is a production root, and no
// root this package makes is ever written outside a test's temp directory.
package releasetest

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// Metadata file names, as releaseverify reads them from a directory.
const (
	RootFile      = metadata.ROOT + ".json"
	TimestampFile = metadata.TIMESTAMP + ".json"
	SnapshotFile  = metadata.SNAPSHOT + ".json"
	TargetsFile   = metadata.TARGETS + ".json"
)

// validity is how long a generated root and its roles stay unexpired
// after the reference time.
const validity = 7 * 24 * time.Hour

// Repo is a generated root with one key per top-level role.
type Repo struct {
	root       []byte
	keys       map[string]ed25519.PrivateKey
	validUntil time.Time
}

// NewRepo generates a root valid for a week after now.
func NewRepo(t testing.TB, now time.Time) *Repo {
	t.Helper()
	r := &Repo{keys: map[string]ed25519.PrivateKey{}, validUntil: now.Add(validity)}
	root := metadata.Root(r.validUntil)
	root.Signed.ConsistentSnapshot = false
	for _, role := range []string{metadata.ROOT, metadata.TIMESTAMP, metadata.SNAPSHOT, metadata.TARGETS} {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		pub, err := metadata.KeyFromPublicKey(key.Public())
		if err != nil {
			t.Fatal(err)
		}
		if err := root.Signed.AddKey(pub, role); err != nil {
			t.Fatal(err)
		}
		r.keys[role] = key
	}
	sign(t, root, r.keys[metadata.ROOT])
	r.root = toBytes(t, root)
	return r
}

// Root is the signed root.json.
func (r *Repo) Root() []byte { return append([]byte(nil), r.root...) }

// WriteRoot writes root.json to path, as a node's adopted root.
func (r *Repo) WriteRoot(t testing.TB, path string) {
	t.Helper()
	if err := os.WriteFile(path, r.root, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Publish writes timestamp, snapshot and targets metadata naming targets
// into dir, at version. A zero timestampExpires is the root's own expiry;
// one before the reference time is a frozen repository.
func (r *Repo) Publish(t testing.TB, dir string, version int64, timestampExpires time.Time, targets map[string][]byte) {
	t.Helper()
	if timestampExpires.IsZero() {
		timestampExpires = r.validUntil
	}
	tgt := metadata.Targets(r.validUntil)
	tgt.Signed.Version = version
	for name, content := range targets {
		info, err := metadata.TargetFile().FromBytes(name, content, "sha256")
		if err != nil {
			t.Fatal(err)
		}
		tgt.Signed.Targets[name] = info
	}
	sign(t, tgt, r.keys[metadata.TARGETS])
	targetsBytes := toBytes(t, tgt)

	snap := metadata.Snapshot(r.validUntil)
	snap.Signed.Version = version
	snap.Signed.Meta = map[string]*metadata.MetaFiles{TargetsFile: hashed(version, targetsBytes)}
	sign(t, snap, r.keys[metadata.SNAPSHOT])
	snapshotBytes := toBytes(t, snap)

	ts := metadata.Timestamp(timestampExpires)
	ts.Signed.Version = version
	ts.Signed.Meta = map[string]*metadata.MetaFiles{SnapshotFile: hashed(version, snapshotBytes)}
	sign(t, ts, r.keys[metadata.TIMESTAMP])

	for name, data := range map[string][]byte{
		TimestampFile: toBytes(t, ts),
		SnapshotFile:  snapshotBytes,
		TargetsFile:   targetsBytes,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func hashed(version int64, data []byte) *metadata.MetaFiles {
	sum := sha256.Sum256(data)
	meta := metadata.MetaFile(version)
	meta.Length = int64(len(data))
	meta.Hashes = metadata.Hashes{"sha256": sum[:]}
	return meta
}

func sign[T metadata.Roles](t testing.TB, meta *metadata.Metadata[T], key ed25519.PrivateKey) {
	t.Helper()
	signer, err := signature.LoadSigner(key, crypto.Hash(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := meta.Sign(signer); err != nil {
		t.Fatal(err)
	}
}

func toBytes[T metadata.Roles](t testing.TB, meta *metadata.Metadata[T]) []byte {
	t.Helper()
	data, err := meta.ToBytes(false)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
