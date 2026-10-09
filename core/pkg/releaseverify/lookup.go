package releaseverify

import (
	"fmt"
)

// Load verifies the metadata in c.MetadataDir against the adopted root and the
// rollback record, as CheckFile does, and returns every target it names. It
// reads no file and writes nothing.
func Load(c FileCheck) (*Verified, error) {
	meta, err := readMetadata(c.RootPath, c.MetadataDir, c.Roles)
	if err != nil {
		return nil, err
	}
	seen, err := readSeen(c.SeenPath)
	if err != nil {
		return nil, err
	}
	return Verify(meta, seen, c.Now)
}

// Lookup is Load for one target, c.Target. A caller that must download the
// target to check it learns its length and hashes here first, then calls
// CheckFile on what it downloaded, which is what raises the rollback record.
func Lookup(c FileCheck) (Target, error) {
	verified, err := Load(c)
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
// of channel, ordered by compare (positive: a is newer than b). A target whose
// version compare cannot order is not a candidate, so one oddly named entry in
// a signed channel does not stop the channel's updates. ok is false when the
// channel lists no candidate.
func (v *Verified) Newest(channel, arch string, compare func(a, b string) (int, error)) (Target, ArchiveRef, bool, error) {
	var best Target
	var bestRef ArchiveRef
	found := false
	for _, t := range v.Targets {
		ref, err := ParseArchiveTarget(t.Path)
		if err != nil || ref.Channel != channel || ref.Arch != arch || t.Role != channel {
			continue
		}
		if _, err := compare(ref.Version, ref.Version); err != nil {
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
