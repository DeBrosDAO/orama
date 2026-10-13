package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// releaseManifestBytes is the manifest.json of an unsigned release archive: what a
// machine that downloaded the archive reports.
func releaseManifestBytes(t *testing.T, archive string) []byte {
	t.Helper()
	tree := t.TempDir()
	if err := archivetrust.Extract(archive, tree); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tree, archivetrust.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The manifest and signature EndorseManifest returns are the ones EndorseRelease
// writes into the archive it makes from the same release: an archive a machine
// assembles from them verifies exactly as the endorsed archive does.
func TestEndorseManifest_isWhatEndorseReleaseWritesIntoTheArchive(t *testing.T) {
	key, addr := newKey(t)
	agent := startFakeAgent(t, &fakeAgent{key: key, reported: addr})
	root := releaseRoot(t)
	release := unsignedRelease(t, false)

	manifest, signature, err := endorseManifest(releaseManifestBytes(t, release), root, agent)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "endorsed.tar.gz")
	if err := endorseRelease(release, out, root, agent); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	if err := archivetrust.Extract(out, tree); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(tree, archivetrust.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	wantSig, err := os.ReadFile(filepath.Join(tree, archivetrust.SignatureName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(manifest, want) || signature != string(wantSig) {
		t.Fatal("EndorseManifest and EndorseRelease sealed the same release differently")
	}
	recovered, err := archivetrust.RecoverSigner(manifest, signature)
	if err != nil || recovered != strings.ToLower(addr) {
		t.Fatalf("the signature recovers to %q (%v), want the operator %s", recovered, err, addr)
	}
}

func TestEndorseManifest_carriesTheRootTheReleaseVerifiedUnder(t *testing.T) {
	key, addr := newKey(t)
	agent := startFakeAgent(t, &fakeAgent{key: key, reported: addr})
	root := releaseRoot(t)
	manifest, _, err := endorseManifest(releaseManifestBytes(t, unsignedRelease(t, false)), root, agent)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := archivetrust.ParseManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ReleaseRoot != archivetrust.EncodeReleaseRoot(root) || parsed.Version != "0.3.1" {
		t.Fatalf("the sealed manifest is %+v", parsed)
	}
}

func TestEndorseManifest_aManifestThatIsNotAnUnsignedReleaseIsNotSigned(t *testing.T) {
	key, addr := newKey(t)
	fake := &fakeAgent{key: key, reported: addr}
	agent := startFakeAgent(t, fake)
	root := releaseRoot(t)
	sealed, _, err := endorseManifest(releaseManifestBytes(t, unsignedRelease(t, false)), root, agent)
	if err != nil {
		t.Fatal(err)
	}
	signedBefore := len(fake.signed)
	cases := map[string][]byte{
		"a manifest that already carries a release root": sealed,
		"a manifest that rotates the signers":            []byte(`{"version":"0.3.1","arch":"amd64","signers":["0x1111111111111111111111111111111111111111"]}`),
		"text that is not JSON":                          []byte("not a manifest"),
		"nothing":                                        nil,
	}
	for name, manifest := range cases {
		if _, _, err := endorseManifest(manifest, root, agent); err == nil {
			t.Errorf("%s was signed", name)
		}
	}
	if len(fake.signed) != signedBefore {
		t.Fatal("the wallet signed a manifest that is not an unsigned release's")
	}
}

func TestEndorseManifest_aLockedWalletSignsNothing(t *testing.T) {
	manifest := releaseManifestBytes(t, unsignedRelease(t, false))
	if _, _, err := endorseManifest(manifest, releaseRoot(t), unreachableAgent{}); err == nil {
		t.Fatal("endorsed without a wallet")
	}
}
