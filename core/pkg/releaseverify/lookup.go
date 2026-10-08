package releaseverify

import (
	"fmt"
)

// Lookup verifies the metadata in c.MetadataDir against the adopted root and
// the rollback record, as CheckFile does, and returns the target named
// c.Target. It reads no file and writes nothing: a caller that must download
// the target to check it learns its length and hashes here first, then calls
// CheckFile on what it downloaded, which is what raises the rollback record.
func Lookup(c FileCheck) (Target, error) {
	meta, err := readMetadata(c.RootPath, c.MetadataDir, c.Roles)
	if err != nil {
		return Target{}, err
	}
	seen, err := readSeen(c.SeenPath)
	if err != nil {
		return Target{}, err
	}
	verified, err := Verify(meta, seen, c.Now)
	if err != nil {
		return Target{}, err
	}
	target, ok := verified.Targets[c.Target]
	if !ok {
		return Target{}, fmt.Errorf("targets metadata does not name %q", c.Target)
	}
	return target, nil
}

// Newest is the highest-versioned archive for arch among the verified targets
// of channel, ordered by compare (positive: a is newer than b). ok is false
// when the channel lists none.
func (v *Verified) Newest(channel, arch string, compare func(a, b string) (int, error)) (Target, ArchiveRef, bool, error) {
	var best Target
	var bestRef ArchiveRef
	found := false
	for _, t := range v.Targets {
		ref, err := ParseArchiveTarget(t.Path)
		if err != nil || ref.Channel != channel || ref.Arch != arch || t.Role != channel {
			continue
		}
		if !found {
			best, bestRef, found = t, ref, true
			continue
		}
		cmp, err := compare(ref.Version, bestRef.Version)
		if err != nil {
			return Target{}, ArchiveRef{}, false, fmt.Errorf("order %s and %s: %w", t.Path, best.Path, err)
		}
		if cmp > 0 {
			best, bestRef = t, ref
		}
	}
	return best, bestRef, found, nil
}
