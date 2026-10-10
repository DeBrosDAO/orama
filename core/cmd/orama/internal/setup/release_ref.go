package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// ReleaseRef is the newest release of a network's channel as its verified
// metadata names it. Nothing of the archive is here: every machine downloads it
// from URL, and the digest and length are what it must have.
type ReleaseRef struct {
	Version string
	Arch    string
	// URL is where the repository serves the archive.
	URL string
	// SHA256 (hex) and Length are the archive's digest and size in the signed
	// targets metadata.
	SHA256 string
	Length int64
	// Root is the release root the metadata verified under; the endorsement
	// carries it so the cluster adopts it.
	Root []byte
	// Accept raises the rollback record to the snapshot the release came from,
	// once the machines have checked the archive. Remove deletes the metadata.
	Accept func() error
	Remove func() error
}

// FetchedRelease is a release a machine downloaded and checked.
type FetchedRelease struct {
	// Dir is the private directory on the machine that holds it.
	Dir string
	// SHA256 is the digest of the archive, computed on the machine.
	SHA256 string
	// Manifest is the manifest.json inside the archive.
	Manifest []byte
}

// Endorsement is the operator's signature on a release: the archive's manifest
// carrying the release root, and the wallet's signature on it. Put in the
// release's tree in place of its own manifest, they make the archive a cluster
// installs.
type Endorsement struct {
	Manifest  []byte
	Signature string
	// ManifestSHA256 and CLISHA256 are the digests of the signed manifest and of
	// the bin/orama it lists: a node that has both runs this build.
	ManifestSHA256 string
	CLISHA256      string
}

// Resolve verifies the channel's metadata against the root the network pins and
// names the newest release for arch: the metadata is a few kilobytes, and the
// archive is not downloaded here. Each machine downloads it and checks it against
// the digest and length that come back.
func (f releaseFetcher) Resolve(ctx context.Context, n *netregistry.Network, arch string) (*ReleaseRef, error) {
	home, err := f.home()
	if err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp("", releaseWorkPrefix)
	if err != nil {
		return nil, fmt.Errorf("create a working directory for the release metadata: %w", err)
	}
	res, err := f.resolve(ctx, f.params(n, arch, home, work))
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(work))
	}
	remove := func() error { return errors.Join(res.Remove(), os.RemoveAll(work)) }
	return &ReleaseRef{
		Version: res.Version, Arch: arch, URL: res.URL, SHA256: res.SHA256, Length: res.Length, Root: res.Root,
		Accept: res.Accept, Remove: remove,
	}, nil
}

// Endorse has the operator's RootWallet sign the manifest that a machine, which
// downloaded the archive and found it to be the signed target, reports. The
// manifest must be the one of the release that was resolved: another version or
// architecture is a machine reporting something else than it was asked to fetch.
func (f releaseFetcher) Endorse(_ context.Context, ref *ReleaseRef, manifest []byte) (*Endorsement, error) {
	declared, err := archivetrust.ParseManifest(manifest)
	if err != nil {
		return nil, fmt.Errorf("the manifest of the downloaded release: %w", err)
	}
	if declared.Version != ref.Version || declared.Arch != ref.Arch {
		return nil, fmt.Errorf("the downloaded archive's manifest is for %s linux/%s, and the signed metadata named %s linux/%s",
			declared.Version, declared.Arch, ref.Version, ref.Arch)
	}
	sealed, signature, err := f.seal(manifest, ref.Root)
	if err != nil {
		return nil, fmt.Errorf("have your RootWallet sign release %s: %w", ref.Version, err)
	}
	cli, err := cliChecksum(sealed)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(sealed)
	return &Endorsement{Manifest: sealed, Signature: signature, ManifestSHA256: hex.EncodeToString(sum[:]), CLISHA256: cli}, nil
}
