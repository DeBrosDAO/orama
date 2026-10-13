package releaseverify

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// testNow is the clock every fixture is judged by. Expiry is relative to
// it, not to the wall clock, so a freeze is the timestamp's date and not
// a slow test run.
var testNow = time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)

// stagenetArchive is the target name inside the generated repository.
// It is not a URL and it is not fetched.
const stagenetArchive = "orama-stagenet.tar"

// stagenetRoot is a TUF root generated for this test. It is not a
// production root and it is not written anywhere the cluster would read.
type stagenetRoot struct {
	root         []byte
	snapshotKey  ed25519.PrivateKey
	timestampKey ed25519.PrivateKey
	targetsKeys  [2]ed25519.PrivateKey
	validUntil   time.Time
}

// newStagenetRoot generates a root whose targets role needs both targets
// keys. The threshold lives in the root; the other roles need one key.
func newStagenetRoot(t *testing.T) *stagenetRoot {
	t.Helper()
	s := &stagenetRoot{validUntil: testNow.Add(7 * 24 * time.Hour)}
	s.snapshotKey = testKey(t)
	s.timestampKey = testKey(t)
	s.targetsKeys[0] = testKey(t)
	s.targetsKeys[1] = testKey(t)
	rootKey := testKey(t)

	root := metadata.Root(s.validUntil)
	// No consistent-snapshot prefix: the caller hands us the bytes.
	root.Signed.ConsistentSnapshot = false
	addKey(t, root, rootKey, metadata.ROOT)
	addKey(t, root, s.snapshotKey, metadata.SNAPSHOT)
	addKey(t, root, s.timestampKey, metadata.TIMESTAMP)
	addKey(t, root, s.targetsKeys[0], metadata.TARGETS)
	addKey(t, root, s.targetsKeys[1], metadata.TARGETS)
	root.Signed.Roles[metadata.TARGETS].Threshold = 2
	signMeta(t, root, rootKey)
	s.root = mustBytes(t, root)
	return s
}

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func addKey(t *testing.T, root *metadata.Metadata[metadata.RootType], key ed25519.PrivateKey, role string) {
	t.Helper()
	pub, err := metadata.KeyFromPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Signed.AddKey(pub, role); err != nil {
		t.Fatal(err)
	}
}

func signMeta[T metadata.Roles](t *testing.T, meta *metadata.Metadata[T], keys ...ed25519.PrivateKey) {
	t.Helper()
	meta.ClearSignatures()
	for _, key := range keys {
		signer, err := signature.LoadSigner(key, crypto.Hash(0))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := meta.Sign(signer); err != nil {
			t.Fatal(err)
		}
	}
}

func mustBytes[T metadata.Roles](t *testing.T, meta *metadata.Metadata[T]) []byte {
	t.Helper()
	data, err := meta.ToBytes(false)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// hashedMeta is the snapshot or timestamp entry for a metadata file: its
// version plus the length and sha256 of the exact bytes the client will see.
func hashedMeta(version int64, data []byte) *metadata.MetaFiles {
	sum := sha256.Sum256(data)
	digest := make([]byte, len(sum))
	copy(digest, sum[:])
	meta := metadata.MetaFile(version)
	meta.Length = int64(len(data))
	meta.Hashes = metadata.Hashes{"sha256": digest}
	return meta
}

// release is one published consistent set, plus the archive it names.
type release struct {
	meta    Metadata
	archive []byte
}

// publish signs a consistent repository at snapshot version, with the
// first targetsSigners targets keys. timestampExpires zero uses the root's
// own expiry. archive nil uses a fixed stagenet payload.
func (s *stagenetRoot) publish(t *testing.T, version int64, targetsSigners int, timestampExpires time.Time, archive []byte) release {
	t.Helper()
	if targetsSigners < 1 || targetsSigners > len(s.targetsKeys) {
		t.Fatalf("targets signers %d", targetsSigners)
	}
	if len(archive) == 0 {
		archive = []byte("stagenet archive v1\n")
	}
	if timestampExpires.IsZero() {
		timestampExpires = s.validUntil
	}

	targets := metadata.Targets(s.validUntil)
	targets.Signed.Version = version
	info, err := metadata.TargetFile().FromBytes(stagenetArchive, archive, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	targets.Signed.Targets[stagenetArchive] = info
	signMeta(t, targets, s.targetsKeys[:targetsSigners]...)
	targetsBytes := mustBytes(t, targets)

	snapshot := metadata.Snapshot(s.validUntil)
	snapshot.Signed.Version = version
	snapshot.Signed.Meta = map[string]*metadata.MetaFiles{
		targetsMeta: hashedMeta(targets.Signed.Version, targetsBytes),
	}
	signMeta(t, snapshot, s.snapshotKey)
	snapshotBytes := mustBytes(t, snapshot)

	timestamp := metadata.Timestamp(timestampExpires)
	timestamp.Signed.Version = version
	timestamp.Signed.Meta = map[string]*metadata.MetaFiles{
		snapshotMeta: hashedMeta(snapshot.Signed.Version, snapshotBytes),
	}
	signMeta(t, timestamp, s.timestampKey)

	return release{
		meta: Metadata{
			Root:      s.root,
			Timestamp: mustBytes(t, timestamp),
			Snapshot:  snapshotBytes,
			Targets:   targetsBytes,
		},
		archive: append([]byte(nil), archive...),
	}
}

func TestRefusesTamperedTargetHash(t *testing.T) {
	root := newStagenetRoot(t)
	good := root.publish(t, 1, 2, time.Time{}, nil)
	if _, err := VerifyArchive(good.meta, Seen{}, testNow, stagenetArchive, good.archive); err != nil {
		t.Fatal(err)
	}

	tampered := append([]byte(nil), good.archive...)
	tampered[len(tampered)-1] ^= 0xff
	_, err := VerifyArchive(good.meta, Seen{}, testNow, stagenetArchive, tampered)
	if !errors.Is(err, ErrTargetHash) {
		t.Fatalf("tampered archive: got %v, want %v", err, ErrTargetHash)
	}
}

func TestRefusesBelowThresholdSignatures(t *testing.T) {
	root := newStagenetRoot(t)
	// The root records a targets threshold of 2. One signature is not it.
	good := root.publish(t, 1, 2, time.Time{}, nil)
	if _, err := VerifyArchive(good.meta, Seen{}, testNow, stagenetArchive, good.archive); err != nil {
		t.Fatal(err)
	}

	short := root.publish(t, 1, 1, time.Time{}, nil)
	_, err := VerifyArchive(short.meta, Seen{}, testNow, stagenetArchive, short.archive)
	if !errors.Is(err, ErrThreshold) {
		t.Fatalf("one signature: got %v, want %v", err, ErrThreshold)
	}
}

func TestRefusesExpiredTimestamp(t *testing.T) {
	root := newStagenetRoot(t)
	fresh := root.publish(t, 1, 2, time.Time{}, nil)
	if _, err := VerifyArchive(fresh.meta, Seen{}, testNow, stagenetArchive, fresh.archive); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(fresh.meta, Seen{}, time.Time{}); err == nil {
		t.Fatal("zero time accepted metadata, so expiry was not checked")
	}

	// The snapshot and the targets file are still inside their expiry.
	// The timestamp is not, which is the freeze.
	frozen := root.publish(t, 1, 2, testNow.Add(-time.Minute), nil)
	_, err := VerifyArchive(frozen.meta, Seen{}, testNow, stagenetArchive, frozen.archive)
	if !errors.Is(err, ErrFreeze) {
		t.Fatalf("expired timestamp: got %v, want %v", err, ErrFreeze)
	}
}

func TestRefusesSnapshotRollback(t *testing.T) {
	root := newStagenetRoot(t)
	// Version 1 is a complete, unexpired repository. Seen nothing, it verifies.
	older := root.publish(t, 1, 2, time.Time{}, nil)
	if _, err := VerifyArchive(older.meta, Seen{}, testNow, stagenetArchive, older.archive); err != nil {
		t.Fatal(err)
	}

	newer := root.publish(t, 2, 2, time.Time{}, nil)
	got, err := VerifyArchive(newer.meta, Seen{}, testNow, stagenetArchive, newer.archive)
	if err != nil {
		t.Fatal(err)
	}
	if got.SnapshotVersion != 2 {
		t.Fatalf("snapshot version %d, want 2", got.SnapshotVersion)
	}

	// Same root, still unexpired, lower snapshot version than the one just accepted.
	_, err = VerifyArchive(older.meta, Seen{SnapshotVersion: got.SnapshotVersion}, testNow, stagenetArchive, older.archive)
	if !errors.Is(err, ErrRollback) {
		t.Fatalf("older snapshot: got %v, want %v", err, ErrRollback)
	}

	// The version already seen is not a rollback.
	if _, err := VerifyArchive(newer.meta, Seen{SnapshotVersion: got.SnapshotVersion}, testNow, stagenetArchive, newer.archive); err != nil {
		t.Fatal(err)
	}
}

// TestStagenetRootIsNotCheckedIn fails if a root file is added beside the
// test. The only root is the one newStagenetRoot generates.
func TestStagenetRootIsNotCheckedIn(t *testing.T) {
	matches, err := filepath.Glob("*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("checked-in metadata %v; the test root must be generated", matches)
	}
}
