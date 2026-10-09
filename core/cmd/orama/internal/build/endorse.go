package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// EndorseRelease turns a release archive that has passed the TUF checks into
// the archive a cluster installs: the same files, with the manifest carrying
// the release root and signed by the operator's RootWallet. The cluster then
// verifies it as it verifies any archive its operator built (the wallet
// signature in its trust anchor), and adopts the release root from it.
//
// The archive must be an unsigned release: it verifies against its own
// manifest, carries no signature, no signer list and no release root. The
// operator's signature is what says "this is the release I want my cluster to
// run"; the TUF checks that came first say it is the Orama release.
func EndorseRelease(src, dst string, root []byte) error {
	return endorseRelease(src, dst, root, newAgentSigner())
}

func endorseRelease(src, dst string, root []byte, agent archiveSigner) (err error) {
	tree, err := os.MkdirTemp("", "orama-endorse-*")
	if err != nil {
		return fmt.Errorf("create a working directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(tree)) }()

	if err := archivetrust.Extract(src, tree); err != nil {
		return fmt.Errorf("unpack the release archive %s: %w", src, err)
	}
	manifest, err := releaseManifest(tree)
	if err != nil {
		return err
	}
	built, err := time.Parse(dateLayout, manifest.Date)
	if err != nil {
		return fmt.Errorf("the release's build date %q: %w", manifest.Date, err)
	}
	signer, err := signerAddress(agent)
	if err != nil {
		return err
	}
	manifest.ReleaseRoot = archivetrust.EncodeReleaseRoot(root)
	manifestJSON, signature, err := sealManifest(manifest, agent, signer)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tree, archivetrust.ManifestName), manifestJSON, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tree, archivetrust.SignatureName), []byte(signature), 0o644); err != nil {
		return err
	}
	_, err = writeArchiveFile(dst, tree, built, true)
	return err
}

// releaseManifest verifies the unsigned release unpacked in tree against its
// own manifest and returns that manifest.
func releaseManifest(tree string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(tree, archivetrust.ManifestName))
	if err != nil {
		return nil, fmt.Errorf("read the release manifest: %w", err)
	}
	declared, err := archivetrust.ParseManifest(data)
	if err != nil {
		return nil, err
	}
	v, _, err := archivetrust.VerifyUnsignedTree(tree, declared.Arch)
	if err != nil {
		return nil, fmt.Errorf("the release archive is not intact: %w", err)
	}
	return v.Manifest, nil
}
