// Package releaseverify checks a release archive against TUF metadata the
// caller already holds.
//
// The caller supplies the root: as bytes to Verify, or as the file a node
// adopted (RootPath) to CheckFile. This package ships no root and does not
// fetch metadata, so it cannot point a cluster at a public repository.
// It does not read or write /etc/orama/archive-signers. A node that has
// not adopted a release root has nothing to check against, CheckFile
// refuses with ErrNoRoot, and the default remains the operator wallet in
// pkg/archivetrust.
//
// Checks are the go-tuf client workflow for the four top-level roles:
// the root the caller passed, then timestamp, snapshot, and targets.
package releaseverify

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/trustedmetadata"
)

const (
	snapshotMeta = metadata.SNAPSHOT + ".json"
	targetsMeta  = metadata.TARGETS + ".json"
)

// ErrRollback is a snapshot whose version is lower than one this client
// has already accepted.
var ErrRollback = errors.New("snapshot rollback")

// ErrFreeze is an expired timestamp: the repository is not issuing fresh
// metadata, so the client refuses the snapshot it points at.
var ErrFreeze = errors.New("timestamp freeze")

// ErrThreshold is a role signed by fewer keys than the threshold the root
// records for that role.
var ErrThreshold = errors.New("signature threshold")

// ErrTargetHash is archive bytes that do not match the length and hashes
// in the verified targets metadata.
var ErrTargetHash = errors.New("target hash")

// Metadata is one consistent set of top-level TUF metadata. Every field
// is bytes the caller already has; nothing here is downloaded.
type Metadata struct {
	Root      []byte
	Timestamp []byte
	Snapshot  []byte
	Targets   []byte
}

// Seen is the rollback state the caller stores between verifications.
// SnapshotVersion zero means this client has not accepted a snapshot yet.
type Seen struct {
	SnapshotVersion int64
}

// Target is one file named by verified targets metadata.
type Target struct {
	Path   string
	Length int64
	// Hashes maps a TUF hash algorithm (sha256, sha512) to the raw digest.
	Hashes map[string][]byte
}

// Verified is a targets set that passed the TUF checks. SnapshotVersion
// is what the caller passes back as Seen.SnapshotVersion next time.
type Verified struct {
	Targets         map[string]Target
	SnapshotVersion int64
}

// Verify checks meta against the root inside it and against seen, at now.
// now is the clock expiry is judged by; the zero time is refused so a
// caller cannot turn the freeze check off. A snapshot version lower than
// seen.SnapshotVersion is a rollback. An equal version is the snapshot
// already accepted and is not a rollback.
func Verify(meta Metadata, seen Seen, now time.Time) (*Verified, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("reference time is required so an expired timestamp is a freeze")
	}
	if seen.SnapshotVersion < 0 {
		return nil, fmt.Errorf("seen snapshot version %d is negative", seen.SnapshotVersion)
	}

	trusted, err := trustedmetadata.New(meta.Root)
	if err != nil {
		return nil, roleError("root", err)
	}
	// go-tuf starts the reference clock at the wall time. Expiry has to
	// follow the caller, or a test — and a node with a stepped clock —
	// cannot decide what "expired" means.
	trusted.RefTime = now.UTC()

	if _, err := trusted.UpdateTimestamp(meta.Timestamp); err != nil {
		return nil, roleError("timestamp", err)
	}
	if _, err := trusted.UpdateSnapshot(meta.Snapshot, false); err != nil {
		return nil, roleError("snapshot", err)
	}
	// Expiry is decided before this. An old snapshot that has also expired
	// is a freeze. Rollback is the replay that is still inside its expiry
	// and is refused only because a newer snapshot was already accepted.
	version := trusted.Snapshot.Signed.Version
	if version < 1 {
		return nil, fmt.Errorf("snapshot version %d is not a positive version", version)
	}
	if seen.SnapshotVersion > version {
		return nil, fmt.Errorf("%w: snapshot version %d is lower than %d already seen", ErrRollback, version, seen.SnapshotVersion)
	}
	targets, err := trusted.UpdateTargets(meta.Targets)
	if err != nil {
		return nil, roleError("targets", err)
	}

	out := make(map[string]Target, len(targets.Signed.Targets))
	for path, info := range targets.Signed.Targets {
		target, err := targetFrom(path, info)
		if err != nil {
			return nil, err
		}
		out[path] = target
	}
	return &Verified{Targets: out, SnapshotVersion: version}, nil
}

// VerifyArchive checks meta, then checks that archive is the target named
// name. A hash or length mismatch is refused even when the metadata itself
// verifies: the metadata then names some other bytes.
func VerifyArchive(meta Metadata, seen Seen, now time.Time, name string, archive []byte) (*Verified, error) {
	verified, err := Verify(meta, seen, now)
	if err != nil {
		return nil, err
	}
	target, ok := verified.Targets[name]
	if !ok {
		return nil, fmt.Errorf("targets metadata does not name %q", name)
	}
	if err := target.Match(archive); err != nil {
		return nil, err
	}
	return verified, nil
}

// Match reports whether content is the file target names.
func (t Target) Match(content []byte) error {
	if t.Path == "" {
		return fmt.Errorf("%w: target has no path", ErrTargetHash)
	}
	file := &metadata.TargetFiles{
		Length: t.Length,
		Hashes: make(metadata.Hashes, len(t.Hashes)),
	}
	for algo, sum := range t.Hashes {
		file.Hashes[algo] = bytes.Clone(sum)
	}
	if err := file.VerifyLengthHashes(content); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrTargetHash, t.Path, err)
	}
	return nil
}

// targetFrom copies one targets entry. A target with no hash cannot be
// checked later, so it is refused here rather than returned as trusted.
func targetFrom(path string, info *metadata.TargetFiles) (Target, error) {
	if info == nil || len(info.Hashes) == 0 {
		return Target{}, fmt.Errorf("%w: %s has no hashes", ErrTargetHash, path)
	}
	hashes := make(map[string][]byte, len(info.Hashes))
	for algo, sum := range info.Hashes {
		if len(sum) == 0 {
			return Target{}, fmt.Errorf("%w: %s has an empty %s hash", ErrTargetHash, path, algo)
		}
		hashes[algo] = bytes.Clone(sum)
	}
	return Target{Path: path, Length: info.Length, Hashes: hashes}, nil
}

// roleError turns a go-tuf failure into the refusal a caller can branch on.
// A short signature set is a threshold failure for whichever role produced
// it. Only an expired timestamp is a freeze; an expired root or targets
// file is still refused, and reported as that role.
func roleError(role string, err error) error {
	var unsigned *metadata.ErrUnsignedMetadata
	if errors.As(err, &unsigned) {
		return fmt.Errorf("%w: %s: %w", ErrThreshold, role, err)
	}
	var expired *metadata.ErrExpiredMetadata
	if errors.As(err, &expired) && strings.Contains(expired.Msg, "timestamp.json") {
		return fmt.Errorf("%w: %w", ErrFreeze, err)
	}
	return fmt.Errorf("%s: %w", role, err)
}
