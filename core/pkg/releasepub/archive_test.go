package releasepub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/releasepub/pubtest"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

func TestCut_refusesAnArchiveANodeCouldNotInstallFromARelease(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	ch := mustChannel(t, "nightly")
	cases := map[string]struct {
		path string
		want string
	}{
		"a wallet-signed archive":               {pubtest.ArchiveWith(t, "0.3.1", "amd64", "a", pubtest.Options{Signed: true}), "manifest.sig"},
		"a signer rotation":                     {pubtest.ArchiveWith(t, "0.3.1", "amd64", "a", pubtest.Options{Signers: []string{"0xabc"}}), "signer list"},
		"a release root in the archive":         {pubtest.ArchiveWith(t, "0.3.1", "amd64", "a", pubtest.Options{ReleaseRoot: "e30="}), "release root"},
		"a manifest for another version":        {pubtest.ArchiveWith(t, "0.3.1", "amd64", "a", pubtest.Options{ManifestVersion: "0.3.0"}), "its manifest says 0.3.0"},
		"a manifest for another arch":           {pubtest.ArchiveWith(t, "0.3.1", "arm64", "a", pubtest.Options{ManifestArch: "amd64"}), "its manifest says 0.3.1 linux/amd64"},
		"an amd64 archive with no global layer": {pubtest.ArchiveWith(t, "0.3.1", "amd64", "a", pubtest.Options{ClusterOnly: true}), "lacks the global layer"},
	}
	for name, c := range cases {
		before := agent.approvals()
		_, err := Cut(t.Context(), cutParams(repo, agent, ch, c.path))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one containing %q", name, err, c.want)
		}
		if agent.approvals() != before {
			t.Errorf("%s: a person was asked to approve it", name)
		}
	}
}

func TestCut_clusterOnlyArchivesAreReleasableOnRequestAndArm64NeedsNoGlobalLayer(t *testing.T) {
	agent := newFakeAgent(t)
	repo := newRepo(t, agent)
	ch := mustChannel(t, "nightly")
	arm := pubtest.ArchiveWith(t, "0.3.1", "arm64", "a", pubtest.Options{ClusterOnly: true})
	if _, err := Cut(t.Context(), cutParams(repo, agent, ch, arm)); err != nil {
		t.Fatalf("an arm64 archive: %v", err)
	}
	published(t, repo)
	p := cutParams(repo, agent, ch, pubtest.ArchiveWith(t, "0.3.2", "amd64", "b", pubtest.Options{ClusterOnly: true}))
	p.AllowClusterOnly = true
	if _, err := Cut(t.Context(), p); err != nil {
		t.Fatalf("a cluster-only archive asked for: %v", err)
	}
	if _, err := clientView(t, repo, testNow); err != nil {
		t.Fatal(err)
	}
}

func TestCheckArchive_notAnArchive(t *testing.T) {
	ref := releaseverify.ArchiveRef{Channel: "nightly", Version: "0.3.1", Arch: "amd64"}
	plain := filepath.Join(t.TempDir(), "orama-0.3.1-linux-amd64.tar.gz")
	if err := os.WriteFile(plain, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkArchive(plain, ref, false); err == nil {
		t.Error("a file that is not gzip passed")
	}
	if err := checkArchive(filepath.Join(t.TempDir(), "missing.tar.gz"), ref, false); err == nil {
		t.Error("a missing file passed")
	}
	if err := checkArchive(pubtest.Archive(t, "0.3.1", "amd64", "x"), ref, false); err != nil {
		t.Errorf("a good archive: %v", err)
	}
}
