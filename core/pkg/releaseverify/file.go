package releaseverify

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/durablefile"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// Where a node keeps what it has adopted. A node without RootPath has not
// opted in to a release root, and CheckFile refuses rather than falling
// back to anything else.
const (
	// RootPath is the release root.json this node trusts.
	RootPath = "/etc/orama/release-root.json"
	// SeenPath records the highest snapshot version this node has accepted,
	// so a replayed older snapshot is a rollback across runs.
	SeenPath = "/etc/orama/release-seen.json"
)

// Metadata file names inside a metadata directory.
const (
	TimestampFile = metadata.TIMESTAMP + ".json"
	SnapshotFile  = snapshotMeta
	TargetsFile   = targetsMeta
)

const (
	// maxMetadataBytes bounds one metadata file; a targets file for a few
	// dozen archives is kilobytes.
	maxMetadataBytes = 4 << 20
	// seenFilePerm is the rollback record: root writes, anyone reads.
	seenFilePerm = 0o644
)

// ErrNoRoot is a node that has not adopted a release root.
var ErrNoRoot = errors.New("no release root adopted")

// FileCheck names the files one verification reads.
type FileCheck struct {
	// RootPath is the adopted root.json.
	RootPath string
	// SeenPath is the rollback record; it is created on first success.
	SeenPath string
	// MetadataDir holds timestamp.json, snapshot.json and targets.json.
	MetadataDir string
	// Target is the name the targets metadata lists the file under.
	Target string
	// File is an open descriptor of the file that must be that target. It
	// is read from its start; the caller opens it where nobody else can
	// replace it, so the bytes checked are the bytes it then uses.
	File *os.File
	// Now is the clock expiry is judged by.
	Now time.Time
}

// CheckFile verifies the metadata in c.MetadataDir against the adopted root
// and the rollback record, then checks that c.File has the length and
// hashes c.Target has in the verified targets metadata. Only after all of
// that passes is the rollback record raised to the accepted snapshot. The
// record is locked from its read to its write, so two checks cannot both
// read the old version and the lower one land last.
func CheckFile(c FileCheck) (*Verified, error) {
	if c.File == nil {
		return nil, fmt.Errorf("no file to check against target %q", c.Target)
	}
	return acceptTarget(c, func(t Target) error { return t.MatchOpen(c.File) })
}

// Accept is CheckFile for a caller that had the file checked somewhere else:
// the machines of a setup run each download the archive and check it against
// the length and SHA-256 that Lookup returns, and this machine never holds the
// file. It verifies the metadata in c.MetadataDir as CheckFile does, requires
// c.Target to be named in it, and raises the rollback record to the accepted
// snapshot. It is called only once the file has been checked against that
// target; c.File is not used.
func Accept(c FileCheck) (*Verified, error) {
	return acceptTarget(c, func(Target) error { return nil })
}

// acceptTarget verifies the metadata, hands the verified c.Target to match,
// and only when match agrees raises the rollback record.
func acceptTarget(c FileCheck, match func(Target) error) (v *Verified, err error) {
	meta, err := readMetadata(c.RootPath, c.MetadataDir)
	if err != nil {
		return nil, err
	}
	unlock, err := lockSeen(c.SeenPath)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	seen, err := readSeen(c.SeenPath)
	if err != nil {
		return nil, err
	}
	verified, err := Verify(meta, seen, c.Now)
	if err != nil {
		return nil, err
	}
	target, ok := verified.Targets[c.Target]
	if !ok {
		return nil, fmt.Errorf("targets metadata does not name %q", c.Target)
	}
	if err := match(target); err != nil {
		return nil, err
	}
	if verified.SnapshotVersion > seen.SnapshotVersion {
		if err := writeSeen(c.SeenPath, verified.SnapshotVersion); err != nil {
			return nil, err
		}
	}
	return verified, nil
}

// MatchOpen is Match for an open file, read from its start once and never
// past the target's length, so a huge or growing file cannot exhaust
// memory.
func (t Target) MatchOpen(f *os.File) error {
	if t.Path == "" || len(t.Hashes) == 0 {
		return fmt.Errorf("%w: target %q has no hashes", ErrTargetHash, t.Path)
	}
	path := f.Name()
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind %s to check it against target %s: %w", path, t.Path, err)
	}

	hashers := make(map[string]hash.Hash, len(t.Hashes))
	writers := make([]io.Writer, 0, len(t.Hashes))
	for algo := range t.Hashes {
		h, err := newHash(algo)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrTargetHash, t.Path, err)
		}
		hashers[algo] = h
		writers = append(writers, h)
	}
	n, err := io.Copy(io.MultiWriter(writers...), io.LimitReader(f, t.Length+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if n != t.Length {
		return fmt.Errorf("%w: %s: %s is not %d bytes long", ErrTargetHash, t.Path, path, t.Length)
	}
	for algo, h := range hashers {
		if !bytes.Equal(h.Sum(nil), t.Hashes[algo]) {
			return fmt.Errorf("%w: %s: %s %s does not match", ErrTargetHash, t.Path, path, algo)
		}
	}
	return nil
}

// newHash is a hash TUF targets metadata may name. Any other algorithm is
// refused: a target this client cannot check is not a verified target.
func newHash(algo string) (hash.Hash, error) {
	switch algo {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("hash algorithm %q is not supported", algo)
	}
}

func readMetadata(rootPath, dir string) (Metadata, error) {
	root, err := readLimited(rootPath)
	if errors.Is(err, fs.ErrNotExist) {
		return Metadata{}, fmt.Errorf("%w: %s does not exist", ErrNoRoot, rootPath)
	}
	if err != nil {
		return Metadata{}, fmt.Errorf("read the release root: %w", err)
	}
	meta := Metadata{Root: root}
	for name, dst := range map[string]*[]byte{
		TimestampFile: &meta.Timestamp,
		SnapshotFile:  &meta.Snapshot,
		TargetsFile:   &meta.Targets,
	} {
		data, err := readLimited(filepath.Join(dir, name))
		if err != nil {
			return Metadata{}, fmt.Errorf("read release metadata: %w", err)
		}
		*dst = data
	}
	return meta, nil
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMetadataBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxMetadataBytes {
		return nil, fmt.Errorf("%s is over %d bytes", path, maxMetadataBytes)
	}
	return data, nil
}

// seenRecord is the rollback record's on-disk form.
type seenRecord struct {
	SnapshotVersion int64 `json:"snapshot_version"`
}

// readSeen reads the rollback record. A missing record is a node that has
// not accepted a snapshot yet; an unreadable one is an error, never zero,
// because zero would accept any replay.
func readSeen(path string) (Seen, error) {
	data, err := readLimited(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Seen{}, nil
	}
	if err != nil {
		return Seen{}, fmt.Errorf("read the release rollback record: %w", err)
	}
	var rec seenRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return Seen{}, fmt.Errorf("parse the release rollback record %s: %w", path, err)
	}
	return Seen(rec), nil
}

// writeSeen replaces the rollback record atomically.
func writeSeen(path string, version int64) error {
	data, err := json.Marshal(seenRecord{SnapshotVersion: version})
	if err != nil {
		return fmt.Errorf("encode the release rollback record: %w", err)
	}
	return durablefile.Write(path, data, seenFilePerm)
}
