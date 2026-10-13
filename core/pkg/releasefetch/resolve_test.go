package releasefetch

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

func TestResolve_namesTheArchiveWithoutDownloadingIt(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)

	res, err := Resolve(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(r.archive)
	if res.Version != "0.3.1" || res.Target != "nightly/orama-0.3.1-linux-amd64.tar.gz" {
		t.Fatalf("resolution = %+v", res)
	}
	if res.SHA256 != hex.EncodeToString(sum[:]) || res.Length != int64(len(r.archive)) {
		t.Errorf("the signed digest and size are %s and %d, got %s and %d", hex.EncodeToString(sum[:]), len(r.archive), res.SHA256, res.Length)
	}
	if want := r.url + "/targets/" + res.Target; res.URL != want {
		t.Errorf("URL = %q, want %q", res.URL, want)
	}
	if string(res.Root) != string(r.root) {
		t.Error("the release root changed without a rotation")
	}
	entries, err := os.ReadDir(p.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(p.WorkDir, e.Name(), "release.tar.gz")); err == nil {
				t.Error("Resolve downloaded the archive")
			}
		}
	}
	if err := res.Remove(); err != nil {
		t.Fatal(err)
	}
}

func TestResolve_carriesTheManifestDigestTheSignedMetadataNames(t *testing.T) {
	r := newRepo(t)
	res, err := Resolve(t.Context(), r.params(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.ManifestSHA256 != testManifestDigest {
		t.Errorf("ManifestSHA256 = %q, want the %q the signed metadata names", res.ManifestSHA256, testManifestDigest)
	}
}

func TestResolve_metadataThatNamesNoManifestDigestIsRefused(t *testing.T) {
	for name, digest := range map[string]string{
		"no digest":               "",
		"a digest that is short":  "abcd",
		"an upper case digest":    strings.ToUpper(testManifestDigest),
		"a digest that is no hex": strings.Repeat("z", 64),
	} {
		r := newRepo(t)
		r.manifestDigest = digest
		r.publish(t, 4, "0.3.1", r.archive, r.archive)
		_, err := Resolve(t.Context(), r.params(t))
		if err == nil || !strings.Contains(err.Error(), "names no manifest digest") || !strings.Contains(err.Error(), "--upload-release") {
			t.Errorf("%s: err = %v, want the missing digest named with the way out", name, err)
		}
	}
}

func TestResolve_aTargetWithNoCustomFieldAtAllIsRefused(t *testing.T) {
	r := newRepo(t)
	files, err := releaserepo.Build(r.keys, releaserepo.Spec{
		Version: 5, RootValidUntil: now.Add(24 * time.Hour),
		Targets: map[string][]byte{releaseverify.ArchiveTarget("nightly", "0.3.1", "amd64"): r.archive},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(r.dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Resolve(t.Context(), r.params(t)); err == nil || !strings.Contains(err.Error(), "names no manifest digest") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolve_theRollbackRecordIsRaisedOnlyByAccept(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)
	res, err := Resolve(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.SeenPath); err == nil {
		t.Fatal("resolving a release raised the rollback record before any machine checked the archive")
	}
	if err := res.Accept(); err != nil {
		t.Fatal(err)
	}
	// An older snapshot is now a rollback.
	r.publish(t, 2, "0.3.1", r.archive, r.archive)
	p.WorkDir = filepath.Join(t.TempDir(), "again")
	if _, err := Resolve(t.Context(), p); err == nil {
		t.Fatal("a snapshot older than the accepted one was resolved")
	}
}

func TestResolve_followsARootRotationAndReturnsTheNewRoot(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)
	next, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	second, err := releaserepo.NextRoot(r.root, r.keys, next, now.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "2.root.json"), second, 0o644); err != nil {
		t.Fatal(err)
	}
	r.keys = next
	r.publish(t, 4, "0.3.2", r.archive, r.archive)

	res, err := Resolve(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "0.3.2" || string(res.Root) != string(second) {
		t.Fatalf("version %s, rotated root returned: %v", res.Version, string(res.Root) == string(second))
	}
}

func TestResolve_refusesWhatFetchRefuses(t *testing.T) {
	r := newRepo(t)
	cases := map[string]func(p *Params){
		"a root other than the pinned one": func(p *Params) { p.RootSHA256 = strings.Repeat("ab", 32) },
		"a channel with no release":        func(p *Params) { p.Channel = "main" },
		"an architecture with no release":  func(p *Params) { p.Arch = "arm64" },
		"a release older than the minimum": func(p *Params) { p.MinVersion = "0.4.0" },
		"no rollback record":               func(p *Params) { p.SeenPath = "" },
	}
	for name, change := range cases {
		p := r.params(t)
		change(&p)
		if _, err := Resolve(t.Context(), p); err == nil {
			t.Errorf("%s was resolved", name)
		}
	}
}

func TestResolve_aTargetWithoutASHA256IsRefused(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)
	found, err := newest(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	delete(found.rel.Target.Hashes, "sha256")
	if _, err := found.resolution(p); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err = %v, want the missing digest named", err)
	}
}

func TestAccept_aTargetTheMetadataDoesNotNameIsRefused(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)
	res, err := Resolve(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	res.check.Target = "nightly/orama-9.9.9-linux-amd64.tar.gz"
	if _, err := releaseverify.Accept(res.check); err == nil || !strings.Contains(err.Error(), "does not name") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(p.SeenPath); err == nil {
		t.Error("a refused acceptance raised the rollback record")
	}
}
