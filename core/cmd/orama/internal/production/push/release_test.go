package push

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releasetest"
)

// releaseNow is the clock the generated metadata is judged by.
var releaseNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

const releaseTarget = "orama-2.0.0-linux-amd64.tar.gz"

// releaseNode is a node that adopted a generated test root, and a signed
// archive published under it.
type releaseNode struct {
	target   *testTarget
	base     string
	archive  string
	metadata string
	repo     *releasetest.Repo
	rootPath string
}

func newReleaseNode(t *testing.T) *releaseNode {
	t.Helper()
	key, addr := newSigner(t)
	etc := t.TempDir()
	n := &releaseNode{
		base:     installedNode(t),
		archive:  writeTarball(t, signedEntries(t, key, newBuild)),
		metadata: t.TempDir(),
		repo:     releasetest.NewRepo(t, releaseNow),
		rootPath: filepath.Join(etc, "release-root.json"),
	}
	n.repo.WriteRoot(t, n.rootPath)
	n.publish(t, releaseNow.Add(time.Hour))
	n.target = trusting(n.base, addr)
	n.target.checkRelease = func(archive, dir, target string) error {
		_, err := releaseverify.CheckFile(releaseverify.FileCheck{
			RootPath:    n.rootPath,
			SeenPath:    filepath.Join(etc, "release-seen.json"),
			MetadataDir: dir,
			Target:      target,
			File:        archive,
			Now:         releaseNow,
		})
		return err
	}
	return n
}

func (n *releaseNode) publish(t *testing.T, timestampExpires time.Time) {
	t.Helper()
	body, err := os.ReadFile(n.archive)
	if err != nil {
		t.Fatal(err)
	}
	n.repo.Publish(t, n.metadata, 1, timestampExpires, map[string][]byte{releaseTarget: body})
}

func (n *releaseNode) stage() error {
	return stageArchive(n.target.stageTarget, StageOptions{
		Archive:         n.archive,
		ReleaseMetadata: n.metadata,
		ReleaseTarget:   releaseTarget,
	})
}

func TestStageRelease_goodTargetInstalls(t *testing.T) {
	n := newReleaseNode(t)
	if err := n.stage(); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(n.base, "bin", "orama")); string(b) != "new cli" {
		t.Fatalf("bin/orama = %q", b)
	}
}

func TestStageRelease_tamperedArchiveIsRefused(t *testing.T) {
	n := newReleaseNode(t)
	f, err := os.OpenFile(n.archive, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := n.stage(); !errors.Is(err, releaseverify.ErrTargetHash) {
		t.Fatalf("tampered archive: %v", err)
	}
	assertUntouched(t, n.base)
}

func TestStageRelease_expiredTimestampIsRefused(t *testing.T) {
	n := newReleaseNode(t)
	n.publish(t, releaseNow.Add(-time.Minute))
	if err := n.stage(); !errors.Is(err, releaseverify.ErrFreeze) {
		t.Fatalf("expired timestamp: %v", err)
	}
	assertUntouched(t, n.base)
}

func TestStageRelease_wrongRootIsRefused(t *testing.T) {
	n := newReleaseNode(t)
	releasetest.NewRepo(t, releaseNow).WriteRoot(t, n.rootPath)
	if err := n.stage(); !errors.Is(err, releaseverify.ErrThreshold) {
		t.Fatalf("metadata under another root: %v", err)
	}
	assertUntouched(t, n.base)
}

func TestStageRelease_noAdoptedRootIsRefusedNotSkipped(t *testing.T) {
	n := newReleaseNode(t)
	if err := os.Remove(n.rootPath); err != nil {
		t.Fatal(err)
	}
	if err := n.stage(); !errors.Is(err, releaseverify.ErrNoRoot) {
		t.Fatalf("no adopted root: %v", err)
	}
	assertUntouched(t, n.base)
}

func TestStageRelease_halfTheFlagsIsAnError(t *testing.T) {
	n := newReleaseNode(t)
	for _, opts := range []StageOptions{
		{Archive: n.archive, ReleaseMetadata: n.metadata},
		{Archive: n.archive, ReleaseTarget: releaseTarget},
	} {
		if err := stageArchive(n.target.stageTarget, opts); err == nil {
			t.Fatalf("%+v staged with half the release flags", opts)
		}
		assertUntouched(t, n.base)
	}
}
