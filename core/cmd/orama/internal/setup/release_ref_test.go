package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/releasefetch"
)

func releaseNetwork() *netregistry.Network {
	return &netregistry.Network{Root: []byte("embedded-root"), Manifest: &netregistry.Manifest{
		Name: "stagenet", Channel: "nightly", ReleaseRepo: "https://releases.example", ReleaseRootSHA256: testRootSHA, MinVersion: "0.3.0",
	}}
}

// ---- Resolve

func TestReleaseFetcher_resolveNamesTheArchiveAndDownloadsNothing(t *testing.T) {
	home := t.TempDir()
	var params releasefetch.Params
	f := releaseFetcher{
		resolve: func(_ context.Context, p releasefetch.Params) (*releasefetch.Resolution, error) {
			params = p
			return &releasefetch.Resolution{
				Version: "0.3.1", Target: "nightly/orama-0.3.1-linux-amd64.tar.gz", URL: testReleaseURL, Length: 312 << 20,
				SHA256: testArchiveSHA, Root: []byte("rotated-root"),
			}, nil
		},
		fetch: func(context.Context, releasefetch.Params) (*releasefetch.Release, error) {
			t.Error("Resolve downloaded the archive")
			return nil, errors.New("unexpected")
		},
		now:  func() time.Time { return time.Unix(1_800_000_000, 0) },
		home: func() (string, error) { return home, nil },
	}
	ref, err := f.Resolve(context.Background(), releaseNetwork(), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Version != "0.3.1" || ref.Arch != "amd64" || ref.URL != testReleaseURL || ref.SHA256 != testArchiveSHA || ref.Length != 312<<20 || string(ref.Root) != "rotated-root" {
		t.Errorf("%+v", ref)
	}
	if params.RepoURL != "https://releases.example" || params.Channel != "nightly" || params.Arch != "amd64" || params.MinVersion != "0.3.0" || params.RootSHA256 != testRootSHA {
		t.Errorf("params %+v: the manifest's pins must reach the resolution", params)
	}
	if string(params.Root) != "embedded-root" {
		t.Errorf("the resolution starts from the root built into the CLI, got %q", params.Root)
	}
	if params.SeenPath != filepath.Join(home, "release-seen.json") || params.AdoptedRoot != filepath.Join(home, "release-root-adopted.json") {
		t.Errorf("rollback records %q %q", params.SeenPath, params.AdoptedRoot)
	}
	if _, err := os.Stat(params.WorkDir); err != nil {
		t.Fatalf("the metadata has no working directory: %v", err)
	}
	if err := ref.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(params.WorkDir); !os.IsNotExist(err) {
		t.Error("Remove leaves the working directory")
	}
}

func TestReleaseFetcher_aFailedResolveCleansUp(t *testing.T) {
	var work string
	f := releaseFetcher{
		resolve: func(_ context.Context, p releasefetch.Params) (*releasefetch.Resolution, error) {
			work = p.WorkDir
			return nil, errors.New("the nightly channel lists no release for linux/amd64")
		},
		now:  time.Now,
		home: func() (string, error) { return t.TempDir(), nil },
	}
	if _, err := f.Resolve(context.Background(), releaseNetwork(), "amd64"); err == nil || !strings.Contains(err.Error(), "no release") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Error("the working directory is removed")
	}
}

func TestReleaseFetcher_resolveHomeFailureIsReturned(t *testing.T) {
	f := releaseFetcher{home: func() (string, error) { return "", errors.New("no home") }}
	if _, err := f.Resolve(context.Background(), releaseNetwork(), "amd64"); err == nil || !strings.Contains(err.Error(), "no home") {
		t.Fatalf("got %v", err)
	}
}

// ---- Endorse

// sealedManifest is what the wallet's signature turns a release manifest into.
const sealedManifest = `{"version":"0.3.1","arch":"amd64","checksums":{"orama":"` + testCLISHA + `"},"release_root":"cm9vdA=="}`

func testEndorser(sealed string, sealErr error, rootSeen *[]byte) releaseFetcher {
	return releaseFetcher{seal: func(_, root []byte) ([]byte, string, error) {
		if rootSeen != nil {
			*rootSeen = root
		}
		return []byte(sealed), "0xsig", sealErr
	}}
}

func TestReleaseFetcher_endorseSignsTheManifestUnderTheRootTheReleaseVerifiedUnder(t *testing.T) {
	var root []byte
	f := testEndorser(sealedManifest, nil, &root)
	manifest := []byte(`{"version":"0.3.1","arch":"amd64"}`)
	ref := &ReleaseRef{Version: "0.3.1", Arch: "amd64", Root: []byte("rotated-root"), ManifestSHA256: archivetrust.ManifestDigest(manifest)}
	got, err := f.Endorse(context.Background(), ref, manifest)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(sealedManifest))
	if string(got.Manifest) != sealedManifest || got.Signature != "0xsig" || got.ManifestSHA256 != hex.EncodeToString(sum[:]) || got.CLISHA256 != testCLISHA {
		t.Errorf("%+v", got)
	}
	if string(root) != "rotated-root" {
		t.Errorf("the wallet signs the manifest with the root the release verified under, got %q", root)
	}
}

func TestReleaseFetcher_endorseSignsNothingForAnotherReleaseThanTheOneResolved(t *testing.T) {
	for name, manifest := range map[string]string{
		"another version":      `{"version":"0.3.0","arch":"amd64"}`,
		"another architecture": `{"version":"0.3.1","arch":"arm64"}`,
		"no JSON":              `not a manifest`,
		"nothing":              ``,
	} {
		// The digest is the manifest's own, so that it is the release that is judged.
		ref := &ReleaseRef{Version: "0.3.1", Arch: "amd64", Root: []byte("root"), ManifestSHA256: archivetrust.ManifestDigest([]byte(manifest))}
		signed := false
		f := releaseFetcher{seal: func(_, _ []byte) ([]byte, string, error) { signed = true; return nil, "", nil }}
		if _, err := f.Endorse(context.Background(), ref, []byte(manifest)); err == nil {
			t.Errorf("%s was endorsed", name)
		}
		if signed {
			t.Errorf("%s reached the wallet", name)
		}
	}
}

func TestReleaseFetcher_endorseFailuresAreTheOperatorsToFix(t *testing.T) {
	manifest := []byte(`{"version":"0.3.1","arch":"amd64"}`)
	ref := &ReleaseRef{Version: "0.3.1", Arch: "amd64", Root: []byte("root"), ManifestSHA256: archivetrust.ManifestDigest(manifest)}
	if _, err := testEndorser("", errors.New("the RootWallet agent is locked"), nil).Endorse(context.Background(), ref, manifest); err == nil ||
		!strings.Contains(err.Error(), "RootWallet") || !strings.Contains(err.Error(), "locked") {
		t.Errorf("a locked wallet: %v", err)
	}
	noCLI := `{"version":"0.3.1","arch":"amd64","checksums":{}}`
	if _, err := testEndorser(noCLI, nil, nil).Endorse(context.Background(), ref, manifest); err == nil || !strings.Contains(err.Error(), "bin/orama") {
		t.Errorf("a manifest that lists no CLI: %v", err)
	}
}

// The manifest the wallet signs is the one the signed metadata names, whatever a
// machine reports: a manifest with another digest never reaches the wallet.
func TestReleaseFetcher_endorseSignsNothingThatIsNotTheManifestTheSignedMetadataNames(t *testing.T) {
	named := []byte(`{"version":"0.3.1","arch":"amd64","checksums":{"orama":"` + testCLISHA + `"}}`)
	ref := &ReleaseRef{Version: "0.3.1", Arch: "amd64", Root: []byte("root"), ManifestSHA256: archivetrust.ManifestDigest(named)}
	for name, manifest := range map[string][]byte{
		"a manifest with other checksums": []byte(`{"version":"0.3.1","arch":"amd64","checksums":{"orama":"` + strings.Repeat("0", 64) + `"}}`),
		"the same manifest with a space":  append(append([]byte(nil), named...), ' '),
		"nothing":                         nil,
	} {
		signed := false
		f := releaseFetcher{seal: func(_, _ []byte) ([]byte, string, error) { signed = true; return []byte(sealedManifest), "0xsig", nil }}
		_, err := f.Endorse(context.Background(), ref, manifest)
		if err == nil || !strings.Contains(err.Error(), "signed metadata names") {
			t.Errorf("%s: err = %v", name, err)
		}
		if signed {
			t.Errorf("%s reached the wallet", name)
		}
	}
	f := releaseFetcher{seal: func(_, _ []byte) ([]byte, string, error) { return []byte(sealedManifest), "0xsig", nil }}
	if _, err := f.Endorse(context.Background(), ref, named); err != nil {
		t.Errorf("the named manifest was refused: %v", err)
	}
}

func TestReleaseFetcher_resolveCarriesTheManifestDigest(t *testing.T) {
	f := releaseFetcher{
		resolve: func(context.Context, releasefetch.Params) (*releasefetch.Resolution, error) {
			return &releasefetch.Resolution{Version: "0.3.1", URL: testReleaseURL, SHA256: testArchiveSHA, Length: 1, ManifestSHA256: strings.Repeat("ab", 32)}, nil
		},
		now:  time.Now,
		home: func() (string, error) { return t.TempDir(), nil },
	}
	ref, err := f.Resolve(context.Background(), releaseNetwork(), "amd64")
	if err != nil || ref.ManifestSHA256 != strings.Repeat("ab", 32) {
		t.Fatalf("%+v, %v", ref, err)
	}
}
