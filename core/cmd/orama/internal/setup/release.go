package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/build"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/releasefetch"
)

const (
	// orama home files: the rollback record of releases this machine has
	// accepted and the newest root it has followed the repository to.
	operatorSeenFile  = "release-seen.json"
	adoptedRootFile   = "release-root-adopted.json"
	releaseWorkPrefix = "orama-release-"
	endorsedFile      = "endorsed.tar.gz"
	oramaHomeDir      = ".orama"
	oramaHomeMode     = 0o700
)

// releaseFetcher fetches the newest release of a network's channel, verified
// against the root the network pins (releasefetch), and has the operator's
// RootWallet sign it: a fresh machine has no trust anchor yet, so the build it
// installs is the one its operator endorsed, with the release root in its
// manifest for the machine to adopt (build.EndorseRelease).
type releaseFetcher struct {
	// Test seams.
	fetch   func(context.Context, releasefetch.Params) (*releasefetch.Release, error)
	endorse func(src, dst string, root []byte) error
	now     func() time.Time
	home    func() (string, error)
}

func newReleaseFetcher() releaseFetcher {
	return releaseFetcher{fetch: releasefetch.Fetch, endorse: build.EndorseRelease, now: time.Now, home: oramaHome}
}

func oramaHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory for the release records: %w", err)
	}
	dir := filepath.Join(home, oramaHomeDir)
	if err := os.MkdirAll(dir, oramaHomeMode); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return dir, nil
}

// Fetch downloads, verifies and endorses the release for arch.
func (f releaseFetcher) Fetch(ctx context.Context, n *netregistry.Network, arch string) (*Release, error) {
	home, err := f.home()
	if err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp("", releaseWorkPrefix)
	if err != nil {
		return nil, fmt.Errorf("create a working directory for the release: %w", err)
	}
	rel, err := f.fetch(ctx, releasefetch.Params{
		RepoURL: n.Manifest.ReleaseRepo, Channel: n.Manifest.Channel, Arch: arch, Root: n.Root, RootSHA256: n.Manifest.ReleaseRootSHA256,
		MinVersion: n.Manifest.MinVersion, WorkDir: work, SeenPath: filepath.Join(home, operatorSeenFile),
		AdoptedRoot: filepath.Join(home, adoptedRootFile), Now: f.now(),
	})
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(work))
	}
	remove := func() error { return errors.Join(rel.Remove(), os.RemoveAll(work)) }
	endorsed := filepath.Join(work, endorsedFile)
	if err := f.endorse(rel.ArchivePath, endorsed, rel.Root); err != nil {
		return nil, errors.Join(fmt.Errorf("have your RootWallet sign release %s: %w", rel.Version, err), remove())
	}
	manifest, err := build.ReadArchiveManifest(endorsed)
	if err != nil {
		return nil, errors.Join(err, remove())
	}
	sum := sha256.Sum256(manifest)
	return &Release{Version: rel.Version, Arch: arch, ArchivePath: endorsed, ManifestSHA256: hex.EncodeToString(sum[:]), Remove: remove}, nil
}
