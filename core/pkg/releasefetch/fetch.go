// Package releasefetch gets the newest release of a network's channel onto
// this machine, verified.
//
// A network's manifest names a release repository, a channel, and the SHA-256
// of the TUF root it trusts (release_root_sha256); the root itself ships
// inside the CLI. Fetch takes those values as plain parameters. It checks the
// embedded root against the pinned digest, follows the repository's root
// rotations from it (releaseverify.Repository.UpdateRoot), verifies the
// channel's metadata against the newest root and the rollback record, picks
// the newest release for the architecture, downloads it and checks its length
// and hashes against the signed targets.
//
// What comes back needs no operator signature to install: the archive is
// unsigned, and a node accepts it because it verified against the release
// root (`orama node stage-archive --release-only --release-metadata
// <Release.MetadataDir> --release-target <Release.Target>`, after the root is
// adopted on the node with `orama node trust add-root`). The operator-wallet
// path (a build signed by a wallet in the node's trust anchor) stays for
// maintainers' own builds.
package releasefetch

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

const (
	// workDirPerm and rootFilePerm: what a fetch leaves is the fetching
	// user's alone.
	workDirPerm  = 0o700
	rootFilePerm = 0o600
	// rootFileName is the root the fetch works from and rotates.
	rootFileName = "release-root.json"
	// fetchBudget bounds a whole fetch: the metadata and one archive.
	fetchBudget = 40 * time.Minute
)

// Params is one fetch.
type Params struct {
	// RepoURL is the manifest's release_repo.
	RepoURL string
	// Channel is the manifest's channel: nightly, main or dev/<branch>.
	Channel string
	// Arch is linux/<Arch> of the node the release is for (amd64 or arm64).
	Arch string
	// Root is the root.json embedded in the CLI, and RootSHA256 the digest the
	// manifest pins. The root is used only if it has that digest.
	Root       []byte
	RootSHA256 string
	// MinVersion, when set, is the manifest's min_version: a channel whose
	// newest release is older is refused.
	MinVersion string
	// WorkDir receives the root, the metadata and the archive. Release.Remove
	// deletes what the fetch made in it.
	WorkDir string
	// SeenPath is the rollback record of this machine, kept between fetches so
	// a repository cannot replay an older snapshot.
	SeenPath string
	Now      time.Time
}

// Release is a downloaded, verified release.
type Release struct {
	Version string
	// Target is the name the archive has in the signed targets.
	Target string
	// ArchivePath is the verified archive on this machine.
	ArchivePath string
	// MetadataDir holds the timestamp, snapshot and targets the archive
	// verified against.
	MetadataDir string
	// Root is the root the release verified under: the embedded root, or the
	// newest the repository's rotations led to.
	Root []byte
	rel  autoupdate.Release
}

// Remove deletes the fetched metadata and archive.
func (r *Release) Remove() error { return r.rel.Remove() }

// Fetch downloads and verifies the newest release of the channel for the
// architecture.
func Fetch(ctx context.Context, p Params) (*Release, error) {
	if err := p.check(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p.WorkDir, workDirPerm); err != nil {
		return nil, fmt.Errorf("create %s: %w", p.WorkDir, err)
	}
	rootPath := filepath.Join(p.WorkDir, rootFileName)
	if err := os.WriteFile(rootPath, p.Root, rootFilePerm); err != nil {
		return nil, fmt.Errorf("keep the release root for the checks: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, fetchBudget)
	defer cancel()
	src := autoupdate.Source{RootPath: rootPath, SeenPath: p.SeenPath, WorkDir: p.WorkDir, Arch: p.Arch, Now: func() time.Time { return p.Now }}
	rel, ok, err := src.Newest(ctx, p.RepoURL, p.Channel)
	if err != nil {
		return nil, fmt.Errorf("the %s channel of %s: %w", p.Channel, p.RepoURL, err)
	}
	if !ok {
		return nil, fmt.Errorf("the %s channel of %s lists no release for linux/%s", p.Channel, p.RepoURL, p.Arch)
	}
	if err := p.checkMinVersion(rel.Version); err != nil {
		return nil, errors.Join(err, rel.Remove())
	}
	if err := src.Download(ctx, p.RepoURL, rel); err != nil {
		return nil, errors.Join(err, rel.Remove())
	}
	root, err := releaseverify.ReadRoot(rootPath)
	if err != nil {
		return nil, errors.Join(err, rel.Remove())
	}
	return &Release{Version: rel.Version, Target: rel.Target.Path, ArchivePath: rel.ArchivePath(), MetadataDir: rel.MetadataDir(), Root: root, rel: rel}, nil
}

// check validates the parameters and holds the embedded root to the pin. The
// root is not required to be unexpired: the repository may have rotated it,
// and only the newest root has to be valid (UpdateRoot).
func (p Params) check() error {
	if _, err := releaseverify.ParseRepositoryURL(p.RepoURL); err != nil {
		return err
	}
	if err := releaseverify.ValidChannel(p.Channel); err != nil {
		return err
	}
	if p.Arch != "amd64" && p.Arch != "arm64" {
		return fmt.Errorf("architecture %q is not amd64 or arm64", p.Arch)
	}
	if p.WorkDir == "" || p.SeenPath == "" || p.Now.IsZero() {
		return errors.New("a fetch needs a work directory, a rollback record and a clock")
	}
	pin := strings.ToLower(strings.TrimSpace(p.RootSHA256))
	if _, err := hex.DecodeString(pin); err != nil || len(pin) != 64 {
		return fmt.Errorf("release_root_sha256 %q is not a SHA-256 in hex", p.RootSHA256)
	}
	if len(p.Root) == 0 {
		return errors.New("no release root to start from")
	}
	if subtle.ConstantTimeCompare([]byte(releaseverify.RootDigest(p.Root)), []byte(pin)) != 1 {
		return fmt.Errorf("the release root built into this CLI has sha256 %s, not the %s this network pins; "+
			"use a CLI built for the network, or check the manifest", releaseverify.RootDigest(p.Root), pin)
	}
	return nil
}

func (p Params) checkMinVersion(version string) error {
	if p.MinVersion == "" {
		return nil
	}
	cmp, err := autoupdate.Compare(version, p.MinVersion)
	if err != nil {
		return fmt.Errorf("compare release %s with the network's minimum %s: %w", version, p.MinVersion, err)
	}
	if cmp < 0 {
		return fmt.Errorf("the newest release on the %s channel is %s, older than the %s this network requires", p.Channel, version, p.MinVersion)
	}
	return nil
}
