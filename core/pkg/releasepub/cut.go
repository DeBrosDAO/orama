package releasepub

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// CutParams is one release.
type CutParams struct {
	Repo     Repo
	Channel  Channel
	Archives []string
	// Retention is how many versions of the channel stay listed, this one
	// included.
	Retention int
	// Replace lets a path that is already listed change its bytes (a dev build
	// that reuses a version).
	Replace bool
	// AllowClusterOnly lets an amd64 archive without the global layer be
	// released. A node cannot `orama global install` from it.
	AllowClusterOnly bool
	// DryRun plans the release and stops: nothing is signed or written, and
	// Agent may be nil.
	DryRun   bool
	Now      time.Time
	Agent    Agent
	Progress io.Writer
}

// CutPlan is what a release lists and signs.
type CutPlan struct {
	Channel string
	Version string
	Tag     string
	// Added, Replaced and Dropped are target paths.
	Added, Replaced, Dropped         []string
	TargetsVersion, SnapshotVersion  int64
	TimestampVersion                 int64
	TargetsExpires, TimestampExpires time.Time
	assets                           []Asset
	targets                          *metadata.Metadata[metadata.TargetsType]
	snapshot                         *metadata.Metadata[metadata.SnapshotType]
	timestamp                        *metadata.Metadata[metadata.TimestampType]
	root                             *metadata.Metadata[metadata.RootType]
	rootBytes                        []byte
}

// Cut lists the archives under the channel's prefix, signs the targets, the
// snapshot that names them and the timestamp that names the snapshot (three
// approvals), checks that a client would accept the result, and writes it to
// the repository directory together with the record of what to upload.
func Cut(ctx context.Context, p CutParams) (*CutPlan, error) {
	plan, err := planCut(p)
	if err != nil || p.DryRun {
		return plan, err
	}
	pub, err := p.Agent.ReleaseKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the wallet's release key: %w", err)
	}
	if err := checkAgentKey(plan.root, pub, metadata.TARGETS, metadata.SNAPSHOT, metadata.TIMESTAMP); err != nil {
		return nil, err
	}
	files, err := plan.sign(ctx, p, pub)
	if err != nil {
		return nil, err
	}
	if err := p.Repo.write(files); err != nil {
		return nil, err
	}
	return plan, writePending(p.Repo, plan)
}

// planCut works out the release without signing anything.
func planCut(p CutParams) (*CutPlan, error) {
	rootBytes, root, err := p.Repo.ReadRoot()
	if err != nil {
		return nil, err
	}
	if _, err := releaseverify.ValidateRoot(rootBytes, p.Now); err != nil {
		return nil, fmt.Errorf("the repository's root: %w; renew it (orama maint release renew-root)", err)
	}
	if pending, err := p.Repo.ReadPending(); err != nil {
		return nil, err
	} else if pending != nil {
		return nil, fmt.Errorf("%s holds a cut of %s %s that was never published: publish it (orama maint release publish) before cutting again, "+
			"or delete %s if you mean to throw it away", p.Repo.Dir, pending.Channel, pending.Version, PendingFile)
	}
	if p.Replace && !strings.HasPrefix(p.Channel.Name, devPrefix) {
		return nil, fmt.Errorf("--replace changes the bytes of a published archive and is for dev/<branch> channels only; %s is immutable", p.Channel.Name)
	}
	version, assets, entries, err := readArchives(p.Channel, p.Archives, p.AllowClusterOnly)
	if err != nil {
		return nil, err
	}
	old, err := loadOptional(p.Repo, TargetsFile, metadata.Targets())
	if err != nil {
		return nil, err
	}
	plan := &CutPlan{Channel: p.Channel.Name, Version: version, Tag: p.Channel.Tag(version), assets: assets, root: root, rootBytes: rootBytes}
	if err := plan.buildTargets(p, old, entries); err != nil {
		return nil, err
	}
	return plan, plan.buildSnapshotAndTimestamp(p)
}

// readArchives checks the archive files' names against the channel and each
// other and describes them as targets. They are one version, one per
// architecture.
func readArchives(channel Channel, paths []string, clusterOnly bool) (version string, assets []Asset, entries map[string]*metadata.TargetFiles, err error) {
	if len(paths) == 0 {
		return "", nil, nil, fmt.Errorf("no archive to release: pass --archive orama-<version>-linux-<arch>.tar.gz")
	}
	entries = map[string]*metadata.TargetFiles{}
	for _, path := range paths {
		ref, err := releaseverify.ParseArchiveTarget(channel.Name + "/" + filepath.Base(path))
		if err != nil {
			return "", nil, nil, err
		}
		if version != "" && ref.Version != version {
			return "", nil, nil, fmt.Errorf("archives of versions %s and %s in one release; cut one version at a time", version, ref.Version)
		}
		version = ref.Version
		if err := checkReleasable(ref); err != nil {
			return "", nil, nil, err
		}
		if err := checkArchive(path, ref, clusterOnly); err != nil {
			return "", nil, nil, err
		}
		target := releaseverify.ArchiveTarget(channel.Name, ref.Version, ref.Arch)
		if entries[target] != nil {
			return "", nil, nil, fmt.Errorf("two archives for linux/%s", ref.Arch)
		}
		info, err := describe(path, ref)
		if err != nil {
			return "", nil, nil, err
		}
		entries[target] = info
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", nil, nil, fmt.Errorf("resolve %s: %w", path, err)
		}
		assets = append(assets, Asset{Name: filepath.Base(path), Path: abs, Target: target, SHA256: fmt.Sprintf("%x", info.Hashes["sha256"])})
	}
	return version, assets, entries, nil
}

// checkReleasable refuses a version clients cannot order, or whose name the
// release host cannot redirect.
func checkReleasable(ref releaseverify.ArchiveRef) error {
	if _, err := autoupdate.Compare(ref.Version, ref.Version); err != nil {
		return fmt.Errorf("version %q is not dotted numeric (1.2.3), so a client could not order it and would never install it: %w", ref.Version, err)
	}
	if strings.ContainsAny(ref.Version, "+") {
		return fmt.Errorf("version %q has a '+', which the release host cannot map to a GitHub release tag", ref.Version)
	}
	return nil
}

// describe is the targets entry of an archive: length, sha256, and the custom
// field the person approving the signature reads. The custom field names the
// SHA-256 of the archive's manifest.json too: a machine that downloads the
// archive reports the manifest for the operator to sign, and setup holds the
// manifest to this digest before the wallet is asked.
func describe(path string, ref releaseverify.ArchiveRef) (*metadata.TargetFiles, error) {
	info, err := metadata.TargetFile().FromFile(path, "sha256")
	if err != nil {
		return nil, fmt.Errorf("describe %s: %w", path, err)
	}
	manifestJSON, _, err := archivetrust.ReadArchiveManifest(path)
	if err != nil {
		return nil, err
	}
	custom, err := json.Marshal(releaseverify.ArchiveCustom{
		Version: ref.Version, Arch: ref.Arch, Channel: ref.Channel, ManifestSHA256: archivetrust.ManifestDigest(manifestJSON),
	})
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(custom)
	info.Custom = &raw
	return info, nil
}

// buildTargets makes the unsigned targets metadata: the current listing with
// the new archives merged in and the channel pruned to its retention.
func (c *CutPlan) buildTargets(p CutParams, old *metadata.Metadata[metadata.TargetsType], entries map[string]*metadata.TargetFiles) error {
	expires := capAt(p.Now.UTC().Truncate(time.Second).Add(RolesValidity), c.root.Signed.Expires)
	c.targets, c.TargetsVersion = metadata.Targets(expires), 1
	current := map[string]*metadata.TargetFiles{}
	if old != nil {
		c.TargetsVersion = old.Signed.Version + 1
		current = old.Signed.Targets
	}
	merged, added, replaced, dropped, err := merge(current, entries, c.Channel, p.Retention, p.Replace)
	if err != nil {
		return err
	}
	if len(added)+len(replaced) == 0 {
		return fmt.Errorf("%s version %s is already listed with these bytes; there is nothing to release", c.Channel, c.Version)
	}
	if len(merged) > MaxTargets {
		return fmt.Errorf("the repository would list %d targets, over the %d the RootWallet agent signs; lower --retention", len(merged), MaxTargets)
	}
	c.targets.Signed.Targets, c.targets.Signed.Version = merged, c.TargetsVersion
	c.Added, c.Replaced, c.Dropped, c.TargetsExpires = added, replaced, dropped, expires
	return nil
}

// buildSnapshotAndTimestamp makes the unsigned snapshot and timestamp, one
// version above the current ones. Their hashes are filled in as each file is
// signed.
func (c *CutPlan) buildSnapshotAndTimestamp(p CutParams) error {
	snapOld, err := loadOptional(p.Repo, SnapshotFile, metadata.Snapshot())
	if err != nil {
		return err
	}
	tsOld, err := loadOptional(p.Repo, TimestampFile, metadata.Timestamp())
	if err != nil {
		return err
	}
	c.SnapshotVersion, c.TimestampVersion = 1, 1
	if snapOld != nil {
		c.SnapshotVersion = snapOld.Signed.Version + 1
	}
	if tsOld != nil {
		c.TimestampVersion = tsOld.Signed.Version + 1
	}
	c.snapshot = metadata.Snapshot(c.TargetsExpires)
	c.snapshot.Signed.Version = c.SnapshotVersion
	c.TimestampExpires = capAt(p.Now.UTC().Truncate(time.Second).Add(p.Channel.TimestampValidity()), c.root.Signed.Expires)
	c.timestamp = metadata.Timestamp(c.TimestampExpires)
	c.timestamp.Signed.Version = c.TimestampVersion
	return nil
}

// capAt is t, but never after limit.
func capAt(t, limit time.Time) time.Time {
	if t.After(limit) {
		return limit
	}
	return t
}

// sign makes the three approvals in order and returns the files to write, the
// timestamp last. The result is held to what a client does with it before it
// is returned.
func (c *CutPlan) sign(ctx context.Context, p CutParams, pub ed25519.PublicKey) ([]namedFile, error) {
	paths := slices.Concat(c.Added, c.Replaced)
	steps := []signStep{
		{1, 3, fmt.Sprintf("targets.json version %d: lists %s on the %s channel (%d targets in all)", c.TargetsVersion, summarize(paths), c.Channel, len(c.targets.Signed.Targets))},
		{2, 3, fmt.Sprintf("snapshot.json version %d: names targets.json version %d", c.SnapshotVersion, c.TargetsVersion)},
		{3, 3, fmt.Sprintf("timestamp.json version %d: names snapshot.json version %d, valid until %s", c.TimestampVersion, c.SnapshotVersion, c.TimestampExpires.Format(time.RFC3339))},
	}
	targets, err := signAndEncode(ctx, p, pub, c.targets, steps[0])
	if err != nil {
		return nil, err
	}
	c.snapshot.Signed.Meta = map[string]*metadata.MetaFiles{TargetsFile: metaFor(c.TargetsVersion, targets)}
	snapshot, err := signAndEncode(ctx, p, pub, c.snapshot, steps[1])
	if err != nil {
		return nil, err
	}
	c.timestamp.Signed.Meta = map[string]*metadata.MetaFiles{SnapshotFile: metaFor(c.SnapshotVersion, snapshot)}
	timestamp, err := signAndEncode(ctx, p, pub, c.timestamp, steps[2])
	if err != nil {
		return nil, err
	}
	if _, err := releaseverify.Verify(releaseverify.Metadata{Root: c.rootBytes, Timestamp: timestamp, Snapshot: snapshot, Targets: targets}, releaseverify.Seen{}, p.Now); err != nil {
		return nil, fmt.Errorf("the metadata just signed would not verify on a client; nothing was written: %w", err)
	}
	return []namedFile{{TargetsFile, targets}, {SnapshotFile, snapshot}, {TimestampFile, timestamp}}, nil
}

func signAndEncode[T metadata.Roles](ctx context.Context, p CutParams, pub ed25519.PublicKey, meta *metadata.Metadata[T], step signStep) ([]byte, error) {
	if err := signMetadata(ctx, p.Agent, pub, meta, step, p.Progress); err != nil {
		return nil, err
	}
	data, err := meta.ToBytes(false)
	if err != nil {
		return nil, fmt.Errorf("encode the signed metadata: %w", err)
	}
	return data, nil
}
