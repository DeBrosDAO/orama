package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/build"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// DefaultReleaseChannel is the channel --release reads when --channel is not
// given.
const DefaultReleaseChannel = "stable"

// operatorSeenFile is the rollback record of releases this machine has
// accepted, beside known_hosts in ~/.orama. A repository that serves an older
// snapshot than the newest this machine has installed from is refused.
const operatorSeenFile = "release-seen.json"

// releaseBudget bounds fetching and verifying a release: the metadata and one
// archive of a few hundred megabytes.
const releaseBudget = 40 * time.Minute

// checkReleaseFlags refuses a combination of release flags that cannot be
// carried out, before any machine is touched.
func checkReleaseFlags(opts Options) error {
	if opts.Release == "" {
		if opts.ReleaseRepo != "" || opts.ReleaseRoot != "" || opts.Channel != "" {
			return clierr.Usage("--release-repo, --release-root and --channel apply to --release <version>")
		}
		return nil
	}
	if opts.Archive != "" {
		return clierr.Usage("--release and --archive are alternatives; pass one")
	}
	if opts.ReleaseRepo == "" || opts.ReleaseRoot == "" {
		return clierr.Usage("--release needs --release-repo <url> and --release-root <root.json>: the root is the " +
			"release signers' key set you trust, and it is never fetched from the repository")
	}
	if _, err := releaseverify.ParseRepositoryURL(opts.ReleaseRepo); err != nil {
		return clierr.Usage("--release-repo: %v", err)
	}
	return nil
}

// prepareRelease fetches release opts.Release for arch from the release
// repository, verifies it against the root the operator named, and returns the
// path of the archive the cluster installs: the same files, with the manifest
// carrying that root and signed by the operator's RootWallet. cleanup removes
// it.
//
// Nothing the repository says is believed until it verifies: the metadata
// against the root, then the archive against the length and hashes the
// metadata names. The rollback record is ~/.orama/release-seen.json.
func prepareRelease(ctx context.Context, opts Options, arch string) (archive string, cleanup func(), err error) {
	channel := opts.Channel
	if channel == "" {
		channel = DefaultReleaseChannel
	}
	root, err := os.ReadFile(opts.ReleaseRoot)
	if err != nil {
		return "", nil, clierr.Usage("--release-root: %v", err)
	}
	digest, err := releaseverify.ValidateRoot(root, time.Now())
	if err != nil {
		return "", nil, clierr.Usage("--release-root %s: %v", opts.ReleaseRoot, err)
	}
	fmt.Printf("  Release root %s (%s)\n", digest, opts.ReleaseRoot)

	work, err := os.MkdirTemp("", "orama-release-*")
	if err != nil {
		return "", nil, fmt.Errorf("create a working directory: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(work) }
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	seen, err := operatorSeenPath()
	if err != nil {
		return "", nil, err
	}
	// The root that was validated, signed into the manifest and checked
	// against is these bytes, read once.
	rootPath := filepath.Join(work, "root.json")
	if err := os.WriteFile(rootPath, root, 0o600); err != nil {
		return "", nil, fmt.Errorf("keep the release root for the checks: %w", err)
	}
	opts.ReleaseRoot = rootPath
	ctx, cancel := context.WithTimeout(ctx, releaseBudget)
	defer cancel()
	fetched, err := fetchVerifiedRelease(ctx, work, opts, channel, arch, seen)
	if err != nil {
		return "", nil, err
	}
	// The repository may have rotated its root while the metadata was fetched; the
	// manifest carries the root the release verified under.
	if root, err = os.ReadFile(rootPath); err != nil {
		return "", nil, fmt.Errorf("read the release root after the update: %w", err)
	}
	endorsed := filepath.Join(work, "endorsed.tar.gz")
	fmt.Printf("  Release %s verified; signing it with your RootWallet as the build your cluster runs...\n", opts.Release)
	if err := build.EndorseRelease(fetched, endorsed, root); err != nil {
		return "", nil, err
	}
	return endorsed, cleanup, nil
}

// fetchVerifiedRelease downloads release metadata and the archive into work
// and returns the archive's path once it has passed releaseverify.CheckFile.
func fetchVerifiedRelease(ctx context.Context, work string, opts Options, channel, arch, seen string) (path string, err error) {
	repo := releaseverify.Repository{BaseURL: opts.ReleaseRepo}
	metaDir := filepath.Join(work, "metadata")
	if err := os.Mkdir(metaDir, 0o700); err != nil {
		return "", err
	}
	update := releaseverify.RootUpdate{RootPath: opts.ReleaseRoot, SeenPath: seen, Now: time.Now()}
	if err := repo.Sync(ctx, metaDir, []string{channel}, update); err != nil {
		return "", fmt.Errorf("fetch the release metadata: %w", err)
	}
	check := releaseverify.FileCheck{
		RootPath: opts.ReleaseRoot, SeenPath: seen, MetadataDir: metaDir, Roles: []string{channel},
		Target: releaseverify.ArchiveTarget(channel, opts.Release, arch), Now: time.Now(),
	}
	target, err := releaseverify.Lookup(check)
	if err != nil {
		return "", fmt.Errorf("release %s for linux/%s on the %s channel: %w", opts.Release, arch, channel, err)
	}
	path = filepath.Join(work, "release.tar.gz")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	fmt.Printf("  Downloading %s (%d bytes)...\n", target.Path, target.Length)
	if err := repo.FetchTarget(ctx, target, f); err != nil {
		return "", err
	}
	check.File = f
	if _, err := releaseverify.CheckFile(check); err != nil {
		return "", fmt.Errorf("refusing release %s: %w", opts.Release, err)
	}
	return path, nil
}

// operatorSeenPath is ~/.orama/release-seen.json.
func operatorSeenPath() (string, error) {
	hosts, err := durableKnownHostsPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(hosts), operatorSeenFile), nil
}

// releaseForNode fetches the release for the architecture node reports.
func releaseForNode(node inspector.Node, opts Options) (string, func(), error) {
	arch, err := nodeArchitecture(node)
	if err != nil {
		return "", nil, err
	}
	return prepareRelease(context.Background(), opts, arch)
}
