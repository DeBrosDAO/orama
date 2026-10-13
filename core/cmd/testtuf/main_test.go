package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/releasefetch"
	"github.com/DeBrosOfficial/network/pkg/releasepub/pubtest"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

func serve(t *testing.T, dir string) releaseverify.Repository {
	t.Helper()
	releaseverify.AllowLocalRepositories(t)
	srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Join(dir, repoSubdir))))
	t.Cleanup(srv.Close)
	return releaseverify.Repository{BaseURL: srv.URL}
}

func verifyTarget(t *testing.T, repo releaseverify.Repository, dir, name string) (releaseverify.Target, error) {
	t.Helper()
	meta := t.TempDir()
	if err := repo.FetchMetadata(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	return releaseverify.Lookup(releaseverify.FileCheck{
		RootPath: filepath.Join(dir, repoSubdir, "root.json"), SeenPath: filepath.Join(t.TempDir(), "seen.json"),
		MetadataDir: meta, Target: name, Now: time.Now(),
	})
}

func TestTesttuf_aPublishedArchiveIsOneAClientVerifies(t *testing.T) {
	dir := t.TempDir()
	if err := run([]string{"init", "-dir", dir}); err != nil {
		t.Fatal(err)
	}
	archive := pubtest.Archive(t, "0.3.1", "amd64", "archive bytes")
	if err := run([]string{"publish", "-dir", dir, "-channel", "stable", "-archive", archive}); err != nil {
		t.Fatal(err)
	}
	repo := serve(t, dir)
	name := releaseverify.ArchiveTarget("stable", "0.3.1", "amd64")
	target, err := verifyTarget(t, repo, dir, name)
	if err != nil {
		t.Fatalf("a published archive does not verify: %v", err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Match(data); err != nil {
		t.Fatal(err)
	}
	// The metadata names the digest of the archive's manifest.
	manifest, _, err := archivetrust.ReadArchiveManifest(archive)
	if err != nil {
		t.Fatal(err)
	}
	var custom releaseverify.ArchiveCustom
	if err := json.Unmarshal(target.Custom, &custom); err != nil || custom.ManifestSHA256 != archivetrust.ManifestDigest(manifest) {
		t.Fatalf("custom = %s (%v), want manifest_sha256 %s", target.Custom, err, archivetrust.ManifestDigest(manifest))
	}
	if _, err := verifyTarget(t, repo, dir, releaseverify.ArchiveTarget("nightly", "0.3.1", "amd64")); err == nil {
		t.Fatal("an archive that was not published on a channel is listed there")
	}
}

// What setup reads back from a repository this tool made is the digest of the
// archive's manifest: publish, then Resolve, gives the archive's own.
func TestTesttuf_resolveGivesTheDigestOfThePublishedManifest(t *testing.T) {
	dir := t.TempDir()
	if err := run([]string{"init", "-dir", dir}); err != nil {
		t.Fatal(err)
	}
	archive := pubtest.Archive(t, "0.3.1", "amd64", "archive bytes")
	if err := run([]string{"publish", "-dir", dir, "-channel", "stable", "-archive", archive}); err != nil {
		t.Fatal(err)
	}
	repo := serve(t, dir)
	root, err := os.ReadFile(filepath.Join(dir, repoSubdir, "root.json"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	res, err := releasefetch.Resolve(t.Context(), releasefetch.Params{
		RepoURL: repo.BaseURL, Channel: "stable", Arch: "amd64", Root: root, RootSHA256: releaseverify.RootDigest(root),
		WorkDir: filepath.Join(home, "work"), SeenPath: filepath.Join(home, "seen.json"), AdoptedRoot: filepath.Join(home, "adopted.json"),
		Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("a client cannot resolve the release this tool published: %v", err)
	}
	manifest, _, err := archivetrust.ReadArchiveManifest(archive)
	if err != nil {
		t.Fatal(err)
	}
	if res.ManifestSHA256 != archivetrust.ManifestDigest(manifest) {
		t.Errorf("Resolve gives manifest digest %s, the archive's is %s", res.ManifestSHA256, archivetrust.ManifestDigest(manifest))
	}
}

func TestTesttuf_everyPublishAndRefreshRaisesTheVersion(t *testing.T) {
	dir := t.TempDir()
	if err := run([]string{"init", "-dir", dir}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, repoSubdir)
	_, _, err := nextVersion(repo)
	if err != nil {
		t.Fatal(err)
	}
	v1, _, _ := nextVersion(repo)
	if err := run([]string{"refresh", "-dir", dir}); err != nil {
		t.Fatal(err)
	}
	v2, _, _ := nextVersion(repo)
	if v2 != v1+1 {
		t.Fatalf("versions %d then %d", v1, v2)
	}
}

func TestTesttuf_refusals(t *testing.T) {
	dir := t.TempDir()
	if err := run([]string{"init", "-dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"init", "-dir", dir}); err == nil {
		t.Error("a second init replaced the keys")
	}
	archive := filepath.Join(t.TempDir(), "not-a-release.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"no dir":          {"publish"},
		"unknown":         {"explode", "-dir", dir},
		"bad channel":     {"publish", "-dir", dir, "-channel", "beta", "-archive", archive},
		"bad archive":     {"publish", "-dir", dir, "-channel", "stable", "-archive", archive},
		"missing archive": {"publish", "-dir", dir, "-channel", "stable"},
		"uninitialised":   {"refresh", "-dir", t.TempDir()},
	} {
		if err := run(args); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
