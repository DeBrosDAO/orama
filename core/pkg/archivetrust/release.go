package archivetrust

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// A cluster may adopt the TUF release root of the Orama releases
// (docs/SECURITY.md, "Release trust"). Two things follow from it here:
//
//   - A manifest signed by a trusted wallet may carry a release root, which
//     the node adopts the way it takes a signer rotation.
//   - A tree with no manifest.sig verifies if `orama maint node stage-archive
//     --release-only` staged it, after the archive passed the release root's
//     TUF checks. The staging recorded that in releaseverify.StagedPath,
//     keyed by the manifest and the root, which no archive can write.

// The adopted release root and the staged-release record. A test replaces them.
var (
	ReleaseRootPath   = releaseverify.RootPath
	ReleaseStagedPath = releaseverify.StagedPath
)

// ErrNotStaged is a tree with no signature that was not staged through the
// release root.
var ErrNotStaged = errors.New("the archive is unsigned and was not staged through the release root")

// releaseSignerPrefix names the "signer" of a release-root verified tree.
const releaseSignerPrefix = "release-root:"

// releaseSignerDigits is how much of the root's digest names it.
const releaseSignerDigits = 12

// releaseRootLine is the line of the signing message that shows a signer the
// release root the build would adopt; "" when the manifest carries none. The
// manifest's own hash covers the root either way.
func releaseRootLine(m *Manifest) string {
	if m.ReleaseRoot == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(m.ReleaseRoot)
	if err != nil {
		return "release root: not base64\n"
	}
	return "release root sha256: " + releaseverify.RootDigest(raw) + "\n"
}

// releaseRootBytes decodes and validates the root a manifest carries; nil
// when it carries none.
func (m *Manifest) releaseRootBytes() ([]byte, error) {
	if m.ReleaseRoot == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(m.ReleaseRoot)
	if err != nil {
		return nil, fmt.Errorf("the signed manifest's release_root is not base64: %w", err)
	}
	if _, err := releaseverify.ValidateRoot(raw, now()); err != nil {
		return nil, fmt.Errorf("the signed manifest's release_root: %w", err)
	}
	return raw, nil
}

// EncodeReleaseRoot is the form a manifest carries root.json in.
func EncodeReleaseRoot(root []byte) string {
	return base64.StdEncoding.EncodeToString(root)
}

// VerifyReleaseTree verifies the unsigned archive extracted in dir because it
// was staged through the release root: the staged-release record holds an
// endorsement for exactly this manifest under the root adopted now, the
// manifest is for arch, and every file matches it. The archive cannot rotate
// signers or replace the root: a release the root signs decides what code
// runs, not who may sign builds.
func VerifyReleaseTree(dir, arch string) (*Verified, error) {
	manifestJSON, err := rootfs.At(dir).ReadFile(filepath.Join(dir, ManifestName), manifestLimit)
	if err != nil {
		return nil, fmt.Errorf("read the archive manifest: %w", err)
	}
	root, err := releaseverify.ReadRoot(ReleaseRootPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotStaged, err)
	}
	sum := sha256.Sum256(manifestJSON)
	rootDigest := releaseverify.RootDigest(root)
	endorsed, err := releaseverify.FindStaged(ReleaseStagedPath, hex.EncodeToString(sum[:]), rootDigest)
	if err != nil {
		return nil, err
	}
	if endorsed == nil {
		return nil, ErrNotStaged
	}
	return verifyUnsignedTree(dir, manifestJSON, arch, releaseSignerPrefix+rootDigest[:releaseSignerDigits])
}

// verifyUnsignedTree checks the contents of a tree whose manifest has been
// accepted by other means.
func verifyUnsignedTree(dir string, manifestJSON []byte, arch, signer string) (*Verified, error) {
	manifest, err := ParseManifest(manifestJSON)
	if err != nil {
		return nil, err
	}
	if manifest.Signers != nil || manifest.ReleaseRoot != "" {
		return nil, fmt.Errorf("a release the release root signed carries a signer list or a release root; only a build signed by a trusted wallet may change who is trusted")
	}
	if manifest.Arch != arch {
		return nil, fmt.Errorf("the archive is built for linux/%s and this node is linux/%s", manifest.Arch, arch)
	}
	if err := verifyContents(dir, manifest.Checksums); err != nil {
		return nil, err
	}
	return &Verified{Manifest: manifest, Signer: signer}, nil
}

// VerifyUnsignedTree checks the contents of the unsigned archive in dir for
// arch and returns its manifest bytes' digest-bearing Verified, for a caller
// that has just verified the archive file against the release root: stage
// calls it on the tree it extracted, before it records the endorsement.
func VerifyUnsignedTree(dir, arch string) (*Verified, []byte, error) {
	manifestJSON, err := rootfs.At(dir).ReadFile(filepath.Join(dir, ManifestName), manifestLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("read the archive manifest: %w", err)
	}
	if _, err := lstat(filepath.Join(dir, SignatureName)); err == nil {
		return nil, nil, fmt.Errorf("the archive carries a %s; a release staged through the release root is unsigned", SignatureName)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("stat %s in the archive: %w", SignatureName, err)
	}
	v, err := verifyUnsignedTree(dir, manifestJSON, arch, releaseSignerPrefix)
	return v, manifestJSON, err
}

// ManifestDigest is the SHA-256 of manifest bytes in hex, the key of a staged
// release's endorsement.
func ManifestDigest(manifestJSON []byte) string {
	sum := sha256.Sum256(manifestJSON)
	return hex.EncodeToString(sum[:])
}

// ParseManifest reads manifest.json.
func ParseManifest(manifestJSON []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return nil, fmt.Errorf("parse the archive manifest: %w", err)
	}
	return &m, nil
}
