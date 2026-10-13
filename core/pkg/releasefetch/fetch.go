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
// Resolve does the same without the download, for a caller whose machines each
// download the archive: it verifies the metadata and returns the archive's URL,
// length and SHA-256 from the signed targets, and Accept raises the rollback
// record once those machines have checked the file.
//
// What Fetch returns needs no operator signature to install: the archive is
// unsigned, and a node accepts it because it verified against the release
// root (`orama maint node stage-archive --release-only --release-metadata
// <Release.MetadataDir> --release-target <Release.Target>`, after the root is
// adopted on the node with `orama node trust add-root`). The operator-wallet
// path (a build signed by a wallet in the node's trust anchor) stays for
// maintainers' own builds.
package releasefetch

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	// a repository cannot replay an older snapshot. AdoptedRoot is the newest
	// root this machine has followed the repository to, kept beside it: each
	// fetch starts from the pinned root and walks the rotations again, and this
	// is what tells a rotation this machine has already taken from a new one.
	SeenPath    string
	AdoptedRoot string
	Now         time.Time
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
	ctx, cancel := context.WithTimeout(ctx, fetchBudget)
	defer cancel()
	f, err := newest(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := f.src.Download(ctx, p.RepoURL, f.rel); err != nil {
		return nil, errors.Join(err, f.rel.Remove())
	}
	root, err := releaseverify.ReadRoot(f.rootPath)
	if err != nil {
		return nil, errors.Join(err, f.rel.Remove())
	}
	rel := f.rel
	return &Release{Version: rel.Version, Target: rel.Target.Path, ArchivePath: rel.ArchivePath(), MetadataDir: rel.MetadataDir(), Root: root, rel: rel}, nil
}

// Resolution is the newest release of a channel as the verified metadata names
// it, with the archive not downloaded: for a caller whose machines each download
// the archive from URL and check the file they get against Length and SHA256,
// which are the signed targets' own.
type Resolution struct {
	Version string
	// Target is the name the archive has in the signed targets.
	Target string
	// URL is where the repository serves the archive.
	URL string
	// Length and SHA256 (hex) are the archive's size and digest in the signed
	// targets metadata.
	Length int64
	SHA256 string
	// ManifestSHA256 (hex) is the digest of the archive's manifest.json that the
	// signed targets metadata names (releaseverify.ArchiveCustom). A machine that
	// downloaded the archive reports the manifest, and what the operator's wallet
	// signs must hash to this.
	ManifestSHA256 string
	// Root is the root the metadata verified under: the embedded root, or the
	// newest the repository's rotations led to.
	Root  []byte
	rel   autoupdate.Release
	check releaseverify.FileCheck
}

// Remove deletes the fetched metadata.
func (r *Resolution) Remove() error { return r.rel.Remove() }

// Accept raises this machine's rollback record to the snapshot the release was
// resolved from. It is called once the archive has been checked against
// SHA256 and Length wherever it was downloaded: a snapshot older than one
// accepted this way is refused by the next fetch.
func (r *Resolution) Accept() error {
	if _, err := releaseverify.Accept(r.check); err != nil {
		return fmt.Errorf("record release %s as accepted: %w", r.Version, err)
	}
	return nil
}

// Resolve is Fetch without the download: it verifies the channel's metadata the
// same way, picks the newest release for the architecture and checks it against
// the manifest's minimum version, and returns what the signed targets say about
// the archive. Only the metadata, a few kilobytes, is fetched.
func Resolve(ctx context.Context, p Params) (*Resolution, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchBudget)
	defer cancel()
	f, err := newest(ctx, p)
	if err != nil {
		return nil, err
	}
	res, err := f.resolution(p)
	if err != nil {
		return nil, errors.Join(err, f.rel.Remove())
	}
	return res, nil
}

// found is the newest release of a channel, its metadata verified.
type found struct {
	src      autoupdate.Source
	rel      autoupdate.Release
	rootPath string
}

func (f found) resolution(p Params) (*Resolution, error) {
	target := f.rel.Target
	sum := target.Hashes["sha256"]
	if len(sum) != sha256.Size {
		return nil, fmt.Errorf("the signed targets metadata gives no SHA-256 for %s, which a machine downloading it needs to check it", target.Path)
	}
	manifest, err := manifestDigest(target)
	if err != nil {
		return nil, err
	}
	url, err := (releaseverify.Repository{BaseURL: p.RepoURL}).TargetURL(target)
	if err != nil {
		return nil, err
	}
	root, err := releaseverify.ReadRoot(f.rootPath)
	if err != nil {
		return nil, err
	}
	check := releaseverify.FileCheck{RootPath: f.rootPath, SeenPath: p.SeenPath, MetadataDir: f.rel.MetadataDir(), Target: target.Path, Now: p.Now}
	return &Resolution{
		Version: f.rel.Version, Target: target.Path, URL: url, Length: target.Length, SHA256: hex.EncodeToString(sum),
		ManifestSHA256: manifest, Root: root, rel: f.rel, check: check,
	}, nil
}

// sha256Pattern is a SHA-256 in lower case hex.
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// manifestDigest is the manifest digest the signed metadata names for target. A
// target without one is refused, never taken on the word of the machines that
// download it: the operator's wallet would sign whatever manifest one of them
// reported.
func manifestDigest(target releaseverify.Target) (string, error) {
	var custom releaseverify.ArchiveCustom
	if len(target.Custom) > 0 {
		if err := json.Unmarshal(target.Custom, &custom); err != nil {
			return "", fmt.Errorf("the custom field of %s in the signed targets metadata: %w", target.Path, err)
		}
	}
	if !sha256Pattern.MatchString(custom.ManifestSHA256) {
		return "", fmt.Errorf("the release metadata for %s names no manifest digest, so a machine's report of the manifest cannot be checked before your wallet signs it; "+
			"cut a new release with a current `orama maint release cut` (a published version cannot be changed), or run with --upload-release", target.Path)
	}
	return custom.ManifestSHA256, nil
}

// newest holds the parameters to their pins, brings the root up to the
// repository's newest, and verifies the channel's metadata: the newest release
// for the architecture, not older than the manifest's minimum.
func newest(ctx context.Context, p Params) (found, error) {
	if err := p.check(); err != nil {
		return found{}, err
	}
	if err := os.MkdirAll(p.WorkDir, workDirPerm); err != nil {
		return found{}, fmt.Errorf("create %s: %w", p.WorkDir, err)
	}
	rootPath := filepath.Join(p.WorkDir, rootFileName)
	if err := writeRoot(rootPath, p.Root); err != nil {
		return found{}, err
	}
	src := autoupdate.Source{RootPath: rootPath, SeenPath: p.SeenPath, AdoptedRoot: p.AdoptedRoot, WorkDir: p.WorkDir, Arch: p.Arch, Now: func() time.Time { return p.Now }}
	rel, ok, err := src.Newest(ctx, p.RepoURL, p.Channel)
	if err != nil {
		return found{}, fmt.Errorf("the %s channel of %s: %w", p.Channel, p.RepoURL, err)
	}
	if !ok {
		return found{}, fmt.Errorf("the %s channel of %s lists no release for linux/%s", p.Channel, p.RepoURL, p.Arch)
	}
	if err := p.checkMinVersion(rel.Version); err != nil {
		return found{}, errors.Join(err, rel.Remove())
	}
	return found{src: src, rel: rel, rootPath: rootPath}, nil
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
	if p.WorkDir == "" || p.SeenPath == "" || p.AdoptedRoot == "" || p.Now.IsZero() {
		return errors.New("a fetch needs a work directory, a rollback record, a place to keep the adopted root and a clock")
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

// writeRoot writes the root the fetch starts from. The file is created new:
// a path that already exists (a symlink left in the work directory included) is
// removed first, never written through.
func writeRoot(path string, root []byte) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear the release root of an earlier fetch: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, rootFilePerm)
	if err != nil {
		return fmt.Errorf("keep the release root for the checks: %w", err)
	}
	if _, err := f.Write(root); err != nil {
		return errors.Join(fmt.Errorf("keep the release root for the checks: %w", err), f.Close())
	}
	return f.Close()
}
