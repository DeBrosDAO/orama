package autoupdate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

func newSource(t *testing.T, rel *release) Source {
	t.Helper()
	return Source{
		RootPath: rel.rootOut, SeenPath: filepath.Join(t.TempDir(), "release-seen.json"),
		WorkDir: t.TempDir(), Arch: testArch, Now: time.Now,
	}
}

// A repository that rotated its root keys serves the metadata under the new
// keys and the root chain that leads to them; the node follows the chain and
// adopts the newest root before it reads the channel.
func TestSourceNewest_followsAPublishedRootRotation(t *testing.T) {
	rel := newRelease(t)
	src := newSource(t, rel)
	first, err := os.ReadFile(rel.rootOut)
	if err != nil {
		t.Fatal(err)
	}
	next, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	second, err := releaserepo.NextRoot(first, rel.keys, next, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel.dir, "2.root.json"), second, 0o644); err != nil {
		t.Fatal(err)
	}
	rel.keys = next
	rel.publish(t, 6, time.Time{}, testVersion, rel.archive)

	got, ok, err := src.Newest(t.Context(), rel.url, "stable")
	if err != nil || !ok {
		t.Fatalf("Newest = ok %v, err %v", ok, err)
	}
	defer got.Remove()
	adopted, err := os.ReadFile(rel.rootOut)
	if err != nil {
		t.Fatal(err)
	}
	if string(adopted) != string(second) {
		t.Fatal("the node did not adopt the rotated root")
	}
}

func TestSourceNewest_withoutTheRotationTheNewKeysAreRefused(t *testing.T) {
	rel := newRelease(t)
	src := newSource(t, rel)
	next, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	rel.keys = next
	rel.publish(t, 6, time.Time{}, testVersion, rel.archive)

	if _, ok, err := src.Newest(t.Context(), rel.url, "stable"); err == nil || ok {
		t.Fatalf("metadata signed by keys the adopted root does not list was accepted (ok %v, err %v)", ok, err)
	}
	if _, err := releaseverify.ReadRoot(rel.rootOut); err != nil {
		t.Fatal(err)
	}
}
