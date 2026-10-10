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
	manifestJSON, signature, err := sealEndorsement(manifest, root, agent)
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

// EndorseManifest is EndorseRelease for a release this machine does not hold:
// the machine that does (it downloaded the archive and checked it against the
// signed release targets) reports the archive's manifest.json, and the
// operator's RootWallet signs it carrying root, exactly as EndorseRelease signs
// the manifest of an archive it unpacked. It returns the manifest.json and the
// manifest.sig that, put in the release's tree in place of its own manifest,
// make the archive the one EndorseRelease would have written. Whether the
// release's files match the manifest is for the machine that holds them: the
// signature is checked against them when it stages the archive, and an archive
// whose files differ from this manifest is refused there.
func EndorseManifest(manifest, root []byte) (manifestJSON []byte, signature string, err error) {
	return endorseManifest(manifest, root, newAgentSigner())
}

func endorseManifest(manifest, root []byte, agent archiveSigner) ([]byte, string, error) {
	declared, err := archivetrust.ParseManifest(manifest)
	if err != nil {
		return nil, "", err
	}
	return sealEndorsement(declared, root, agent)
}

// sealEndorsement carries root in the unsigned release's manifest and has the
// operator's wallet sign it.
func sealEndorsement(manifest *Manifest, root []byte, agent archiveSigner) ([]byte, string, error) {
	if manifest.Signers != nil || manifest.ReleaseRoot != "" {
		return nil, "", errors.New("a release carries no signer list and no release root; this manifest does, so it is not one the release root signed")
	}
	signer, err := signerAddress(agent)
	if err != nil {
		return nil, "", err
	}
	manifest.ReleaseRoot = archivetrust.EncodeReleaseRoot(root)
	return sealManifest(manifest, agent, signer)
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
