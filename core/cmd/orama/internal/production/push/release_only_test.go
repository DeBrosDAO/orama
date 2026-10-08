package push

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

const channelTarget = "stable/orama-2.0.0-linux-amd64.tar.gz"

// releaseOnlyNode is a node whose TUF check passes at snapshot 7 and which
// records the endorsements it is asked to give.
type releaseOnlyNode struct {
	*testTarget
	endorsed []releaseverify.Endorsement
	tufErr   error
}

func newReleaseOnlyNode(t *testing.T) *releaseOnlyNode {
	t.Helper()
	_, addr := newSigner(t)
	n := &releaseOnlyNode{testTarget: trusting(installedNode(t), addr)}
	n.checkRelease = func(*os.File, string, string) (int64, error) { return 7, n.tufErr }
	n.endorse = func(e releaseverify.Endorsement) error {
		n.endorsed = append(n.endorsed, e)
		return nil
	}
	// The staged tree has no signature, so the test target judges it as the
	// real one does after a release-only stage: by its own manifest.
	signed := n.verify
	n.verify = func(dir string) (*archivetrust.Verified, error) {
		if _, err := os.Stat(filepath.Join(dir, archivetrust.SignatureName)); err != nil {
			v, _, err := archivetrust.VerifyUnsignedTree(dir, n.arch)
			return v, err
		}
		return signed(dir)
	}
	return n
}

// unsignedEntries is signedEntries without the signature: what CI publishes.
func unsignedEntries(t *testing.T, files map[string]string) []tarEntry {
	t.Helper()
	key, _ := newSigner(t)
	entries := signedEntries(t, key, files)
	return entries[:len(entries)-1]
}

func (n *releaseOnlyNode) stageUnsigned(t *testing.T, files map[string]string, keep bool) error {
	t.Helper()
	return stageArchive(n.stageTarget, StageOptions{
		Archive:         writeTarball(t, unsignedEntries(t, files)),
		ReleaseMetadata: t.TempDir(),
		ReleaseTarget:   channelTarget,
		ReleaseOnly:     true,
		KeepPrevious:    keep,
	})
}

func TestStageReleaseOnly_anUnsignedReleaseThatPassedTUFIsStagedAndEndorsed(t *testing.T) {
	n := newReleaseOnlyNode(t)
	if err := n.stageUnsigned(t, newBuild, false); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(n.base, "bin", "orama")); string(b) != "new cli" {
		t.Fatalf("bin/orama = %q", b)
	}
	if _, err := os.Stat(filepath.Join(n.base, archivetrust.SignatureName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old build's signature stayed beside the new unsigned manifest: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(n.base, archivetrust.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if len(n.endorsed) != 1 {
		t.Fatalf("%d endorsements recorded, want 1", len(n.endorsed))
	}
	e := n.endorsed[0]
	if e.ManifestSHA256 != archivetrust.ManifestDigest(manifest) || e.Target != channelTarget || e.SnapshotVersion != 7 {
		t.Fatalf("endorsement = %+v", e)
	}
}

func TestStageReleaseOnly_aFailedTUFCheckChangesNothingAndEndorsesNothing(t *testing.T) {
	n := newReleaseOnlyNode(t)
	n.tufErr = releaseverify.ErrRollback
	err := n.stageUnsigned(t, newBuild, false)
	if !errors.Is(err, releaseverify.ErrRollback) {
		t.Fatalf("err = %v", err)
	}
	assertUntouched(t, n.base)
	if len(n.endorsed) != 0 {
		t.Fatal("a refused release was endorsed")
	}
}

func TestStageReleaseOnly_aReleaseThatNamesSignersOrARootIsRefused(t *testing.T) {
	n := newReleaseOnlyNode(t)
	key, _ := newSigner(t)
	entries := signedEntries(t, key, newBuild)
	// Rewrite the manifest to rotate the signers, and drop the signature.
	for i, e := range entries {
		if e.name == archivetrust.ManifestName {
			entries[i].body = strings.Replace(e.body, `"arch": "amd64"`, `"arch": "amd64", "signers": ["0x`+strings.Repeat("a", 40)+`"]`, 1)
		}
	}
	err := stageArchive(n.stageTarget, StageOptions{
		Archive: writeTarball(t, entries[:len(entries)-1]), ReleaseMetadata: t.TempDir(), ReleaseTarget: channelTarget, ReleaseOnly: true,
	})
	if err == nil || !strings.Contains(err.Error(), "only a build signed") {
		t.Fatalf("err = %v", err)
	}
	assertUntouched(t, n.base)
	if len(n.endorsed) != 0 {
		t.Fatal("a rotating release was endorsed")
	}
}

func TestStageReleaseOnly_aSignedArchiveIsNotARelease(t *testing.T) {
	n := newReleaseOnlyNode(t)
	key, _ := newSigner(t)
	err := stageArchive(n.stageTarget, StageOptions{
		Archive: writeTarball(t, signedEntries(t, key, newBuild)), ReleaseMetadata: t.TempDir(), ReleaseTarget: channelTarget, ReleaseOnly: true,
	})
	if err == nil || !strings.Contains(err.Error(), archivetrust.SignatureName) {
		t.Fatalf("err = %v", err)
	}
	assertUntouched(t, n.base)
}

func TestStageReleaseOnly_aNodeWithNoAnchorIsNotInstalled(t *testing.T) {
	n := newReleaseOnlyNode(t)
	n.anchor = nil
	err := n.stageUnsigned(t, newBuild, false)
	if err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatalf("err = %v", err)
	}
	assertUntouched(t, n.base)
}

func TestStage_releaseOnlyNeedsBothReleaseFlagsAndNoTrustSigners(t *testing.T) {
	for _, opts := range []StageOptions{
		{Archive: "x", ReleaseOnly: true},
		{Archive: "x", ReleaseOnly: true, ReleaseMetadata: "dir"},
		{Archive: "x", ReleaseOnly: true, ReleaseMetadata: "dir", ReleaseTarget: "t", TrustSigners: []string{"0x1"}},
	} {
		err := Stage(opts)
		var usage *clierr.Error
		if !errors.As(err, &usage) {
			t.Errorf("%+v: err = %v, want a usage error", opts, err)
		}
	}
}

func TestStageKeepPrevious_andRestorePreviousPutTheReplacedReleaseBack(t *testing.T) {
	n := newReleaseOnlyNode(t)
	key, addr := newSigner(t)
	n.anchor = []string{addr}
	first := writeTarball(t, signedEntries(t, key, map[string]string{"bin/orama": "first cli", "systemd/orama-namespace-x.service": "[Unit]\n"}))
	if err := stageArchive(n.stageTarget, StageOptions{Archive: first}); err != nil {
		t.Fatal(err)
	}
	if err := n.stageUnsigned(t, newBuild, true); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(n.base, PreviousRelease)
	if b, _ := os.ReadFile(filepath.Join(kept, "bin", "orama")); string(b) != "first cli" {
		t.Fatalf("the kept release's bin/orama = %q", b)
	}

	if err := restorePrevious(n.stageTarget); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(n.base, "bin", "orama")); string(b) != "first cli" {
		t.Fatalf("after the restore bin/orama = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(kept, "bin", "orama")); string(b) != "new cli" {
		t.Fatalf("the release that was running was not kept: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(n.base, ".orama", "data", "node.key")); string(b) != "node identity" {
		t.Errorf("the node's data was touched: %q", b)
	}
}

func TestRestorePrevious_refusals(t *testing.T) {
	n := newReleaseOnlyNode(t)
	if err := restorePrevious(n.stageTarget); err == nil || !strings.Contains(err.Error(), "no kept release") {
		t.Fatalf("nothing kept: err = %v", err)
	}
	if err := n.stageUnsigned(t, newBuild, true); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(n.base, PreviousRelease)
	// installedNode's old build has a manifest that is not one: it must not be
	// put back as the running release.
	if err := restorePrevious(n.stageTarget); err == nil {
		t.Fatal("a kept tree that does not verify was restored")
	}
	if b, _ := os.ReadFile(filepath.Join(n.base, "bin", "orama")); string(b) != "new cli" {
		t.Fatalf("a refused restore changed bin/orama to %q", b)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("a refused restore removed the kept release: %v", err)
	}
}
