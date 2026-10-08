package releaseverify

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"
)

// StagedPath records the archives this node staged because they verified
// against the release root. It is beside the root in /etc/orama, outside
// /opt/orama and outside anything an archive can write.
const StagedPath = "/etc/orama/release-staged.json"

const (
	// stagedFilePerm: root writes, anyone reads.
	stagedFilePerm = 0o644
	// maxStaged is how many staged archives are remembered: the one in place,
	// the one it replaced for a rollback, and a few more for a node that falls
	// behind a release.
	maxStaged = 8
)

// Endorsement says that an archive was verified against the release root
// when it was staged. It is keyed by the manifest, since the staged tree is
// not the archive file any more, and by the root, so a root that is replaced
// withdraws every endorsement given under the old one.
type Endorsement struct {
	ManifestSHA256  string    `json:"manifest_sha256"`
	RootSHA256      string    `json:"root_sha256"`
	Target          string    `json:"target"`
	SnapshotVersion int64     `json:"snapshot_version"`
	StagedAt        time.Time `json:"staged_at"`
}

type stagedFile struct {
	Entries []Endorsement `json:"entries"`
}

// RecordStaged adds e to the file at path, newest last, under a lock, and
// keeps the last maxStaged. An endorsement for the same manifest and root is
// replaced rather than repeated.
func RecordStaged(path string, e Endorsement) (err error) {
	if e.ManifestSHA256 == "" || e.RootSHA256 == "" || e.Target == "" {
		return fmt.Errorf("an endorsement needs a manifest, a root and a target")
	}
	unlock, err := lockSeen(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	file, err := readStaged(path)
	if err != nil {
		return err
	}
	kept := file.Entries[:0]
	for _, old := range file.Entries {
		if old.ManifestSHA256 != e.ManifestSHA256 || old.RootSHA256 != e.RootSHA256 {
			kept = append(kept, old)
		}
	}
	kept = append(kept, e)
	if len(kept) > maxStaged {
		kept = kept[len(kept)-maxStaged:]
	}
	data, err := json.Marshal(stagedFile{Entries: kept})
	if err != nil {
		return fmt.Errorf("encode the staged-release record: %w", err)
	}
	return writeFileAtomic(path, data, stagedFilePerm)
}

// FindStaged returns the endorsement for a manifest under a root, or nil.
func FindStaged(path, manifestSHA256, rootSHA256 string) (*Endorsement, error) {
	file, err := readStaged(path)
	if err != nil {
		return nil, err
	}
	for i := len(file.Entries) - 1; i >= 0; i-- {
		if e := file.Entries[i]; e.ManifestSHA256 == manifestSHA256 && e.RootSHA256 == rootSHA256 {
			return &e, nil
		}
	}
	return nil, nil
}

// readStaged reads the record. A missing file is an empty record; one that
// cannot be read or parsed is an error, never an empty record, since an empty
// record would drop every endorsement silently.
func readStaged(path string) (stagedFile, error) {
	data, err := readLimited(path)
	if errors.Is(err, fs.ErrNotExist) {
		return stagedFile{}, nil
	}
	if err != nil {
		return stagedFile{}, fmt.Errorf("read the staged-release record: %w", err)
	}
	var file stagedFile
	if err := json.Unmarshal(data, &file); err != nil {
		return stagedFile{}, fmt.Errorf("parse the staged-release record %s: %w", path, err)
	}
	return file, nil
}
