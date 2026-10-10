package releasepub

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// RefreshTimestamp re-signs only the timestamp (one approval): the next
// version, naming the snapshot already in the directory, valid for the
// channel's timestamp validity from now. It is what keeps a quiet repository
// believed by clients, which refuse a timestamp that has expired. It returns
// the new version.
func RefreshTimestamp(ctx context.Context, agent Agent, repo Repo, channel Channel, now time.Time, progress io.Writer) (int64, error) {
	rootBytes, root, err := repo.ReadRoot()
	if err != nil {
		return 0, err
	}
	if _, err := releaseverify.ValidateRoot(rootBytes, now); err != nil {
		return 0, fmt.Errorf("the repository's root: %w; renew it (orama maint release renew-root)", err)
	}
	pub, err := agent.ReleaseKey(ctx)
	if err != nil {
		return 0, fmt.Errorf("read the wallet's release key: %w", err)
	}
	if err := checkAgentKey(root, pub, metadata.TIMESTAMP); err != nil {
		return 0, err
	}
	snapshotBytes, err := os.ReadFile(repo.path(SnapshotFile))
	if err != nil {
		return 0, fmt.Errorf("read %s (cut a release first): %w", repo.path(SnapshotFile), err)
	}
	snapshot, err := metadata.Snapshot().FromBytes(snapshotBytes)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", repo.path(SnapshotFile), err)
	}
	old, err := loadOptional(repo, TimestampFile, metadata.Timestamp())
	if err != nil {
		return 0, err
	}
	version := int64(1)
	if old != nil {
		version = old.Signed.Version + 1
	}
	expires := capAt(now.UTC().Truncate(time.Second).Add(channel.TimestampValidity()), root.Signed.Expires)
	ts := metadata.Timestamp(expires)
	ts.Signed.Version = version
	ts.Signed.Meta = map[string]*metadata.MetaFiles{SnapshotFile: metaFor(snapshot.Signed.Version, snapshotBytes)}
	step := signStep{1, 1, fmt.Sprintf("timestamp.json version %d: names snapshot.json version %d, valid until %s", version, snapshot.Signed.Version, expires.Format(time.RFC3339))}
	data, err := signAndEncode(ctx, CutParams{Agent: agent, Progress: progress}, pub, ts, step)
	if err != nil {
		return 0, err
	}
	targets, err := os.ReadFile(repo.path(TargetsFile))
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", repo.path(TargetsFile), err)
	}
	meta := releaseverify.Metadata{Root: rootBytes, Timestamp: data, Snapshot: snapshotBytes, Targets: targets}
	if _, err := releaseverify.Verify(meta, releaseverify.Seen{}, now); err != nil {
		return 0, fmt.Errorf("the refreshed metadata would not verify on a client; nothing was written: %w", err)
	}
	return version, repo.write([]namedFile{{TimestampFile, data}})
}
