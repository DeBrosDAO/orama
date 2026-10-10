package releasepub

import (
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// merge applies archives (path -> new entry) to the targets already listed,
// then drops the channel's oldest versions beyond retention. It reports what
// was added, replaced and dropped, in path order.
//
// The version order is the one a client reads the channel with
// (autoupdate.Compare), so retention keeps what a client would pick. A version
// older than the channel's newest is refused: a client takes the newest, so an
// older release added now would be dead weight and a rollback in the record.
// A path already listed with other bytes is refused unless replace is set.
func merge(current, archives map[string]*metadata.TargetFiles, channel string, retention int, replace bool) (out map[string]*metadata.TargetFiles, added, replaced, dropped []string, err error) {
	out = make(map[string]*metadata.TargetFiles, len(current)+len(archives))
	for path, info := range current {
		out[path] = info
	}
	newest, err := newestVersion(current, channel)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for path, info := range archives {
		if err := checkVersionOrder(path, newest); err != nil {
			return nil, nil, nil, nil, err
		}
		switch old, listed := out[path]; {
		case !listed:
			added = append(added, path)
		case sameBytes(old, info):
			continue
		case !replace:
			return nil, nil, nil, nil, fmt.Errorf("%s is already listed with other bytes; a release is immutable (cut a new version), or pass --replace for a dev build", path)
		default:
			replaced = append(replaced, path)
		}
		out[path] = info
	}
	dropped, err = retain(out, channel, retention)
	slices.Sort(added)
	slices.Sort(replaced)
	return out, added, replaced, dropped, err
}

// channelVersions groups the channel's targets by version.
func channelVersions(targets map[string]*metadata.TargetFiles, channel string) map[string][]string {
	byVersion := map[string][]string{}
	for path := range targets {
		ref, err := releaseverify.ParseArchiveTarget(path)
		if err != nil || ref.Channel != channel {
			continue
		}
		byVersion[ref.Version] = append(byVersion[ref.Version], path)
	}
	return byVersion
}

// newestVersion is the highest version listed for the channel, "" for none.
func newestVersion(targets map[string]*metadata.TargetFiles, channel string) (string, error) {
	versions, err := sortedVersions(channelVersions(targets, channel))
	if err != nil || len(versions) == 0 {
		return "", err
	}
	return versions[0], nil
}

// checkVersionOrder refuses the archive at path when its version is older than
// newest.
func checkVersionOrder(path, newest string) error {
	if newest == "" {
		return nil
	}
	ref, err := releaseverify.ParseArchiveTarget(path)
	if err != nil {
		return err
	}
	cmp, err := autoupdate.Compare(ref.Version, newest)
	if err != nil {
		return fmt.Errorf("order %s against the channel's newest %s: %w", ref.Version, newest, err)
	}
	if cmp < 0 {
		return fmt.Errorf("version %s is older than %s, which %s already lists; a client takes the newest, so cut a newer version", ref.Version, newest, ref.Channel)
	}
	return nil
}

// sortedVersions orders the versions newest first. A version the clients cannot
// order is an error: it would never be picked and never pruned.
func sortedVersions(byVersion map[string][]string) ([]string, error) {
	versions := make([]string, 0, len(byVersion))
	for v := range byVersion {
		if _, err := autoupdate.Compare(v, v); err != nil {
			return nil, fmt.Errorf("the channel lists version %q, which clients cannot order: %w", v, err)
		}
		versions = append(versions, v)
	}
	slices.SortFunc(versions, func(a, b string) int {
		cmp, _ := autoupdate.Compare(b, a)
		return cmp
	})
	return versions, nil
}

// retain removes the channel's versions beyond the newest `keep` from targets
// and returns the removed paths.
func retain(targets map[string]*metadata.TargetFiles, channel string, keep int) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("retention %d would drop the release just cut", keep)
	}
	byVersion := channelVersions(targets, channel)
	versions, err := sortedVersions(byVersion)
	if err != nil || len(versions) <= keep {
		return nil, err
	}
	var dropped []string
	for _, v := range versions[keep:] {
		for _, path := range byVersion[v] {
			delete(targets, path)
			dropped = append(dropped, path)
		}
	}
	slices.Sort(dropped)
	return dropped, nil
}

// sameBytes reports whether two entries name the same file.
func sameBytes(a, b *metadata.TargetFiles) bool {
	if a.Length != b.Length {
		return false
	}
	x, y := a.Hashes["sha256"], b.Hashes["sha256"]
	return len(x) > 0 && hex.EncodeToString(x) == hex.EncodeToString(y)
}
