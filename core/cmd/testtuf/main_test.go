package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	archive := filepath.Join(t.TempDir(), "orama-0.3.1-linux-amd64.tar.gz")
	if err := os.WriteFile(archive, []byte("archive bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"publish", "-dir", dir, "-channel", "stable", "-archive", archive}); err != nil {
		t.Fatal(err)
	}
	repo := serve(t, dir)
	name := releaseverify.ArchiveTarget("stable", "0.3.1", "amd64")
	target, err := verifyTarget(t, repo, dir, name)
	if err != nil {
		t.Fatalf("a published archive does not verify: %v", err)
	}
	if err := target.Match([]byte("archive bytes")); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyTarget(t, repo, dir, releaseverify.ArchiveTarget("nightly", "0.3.1", "amd64")); err == nil {
		t.Fatal("an archive that was not published on a channel is listed there")
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
