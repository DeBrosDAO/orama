package releaseverify

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func numericCompare(a, b string) (int, error) {
	x, err := strconv.Atoi(strings.ReplaceAll(a, ".", ""))
	if err != nil {
		return 0, err
	}
	y, err := strconv.Atoi(strings.ReplaceAll(b, ".", ""))
	if err != nil {
		return 0, err
	}
	return x - y, nil
}

func TestVerified_newestPicksTheHighestVersionOfTheChannelAndArch(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, map[string][]byte{
		ArchiveTarget("stable", "0.3.1", "amd64"):  []byte("a"),
		ArchiveTarget("stable", "0.3.10", "amd64"): []byte("b"),
		ArchiveTarget("stable", "0.3.9", "amd64"):  []byte("c"),
		ArchiveTarget("stable", "9.9.9", "arm64"):  []byte("d"),
		ArchiveTarget("nightly", "9.0.0", "amd64"): []byte("e"),
	})
	v, err := Verify(r.metaFor(files), Seen{}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	target, ref, ok, err := v.Newest("stable", "amd64", numericCompare)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ref.Version != "0.3.10" || ref.Channel != "stable" {
		t.Fatalf("newest = %+v (%s)", ref, target.Path)
	}
	if _, _, ok, _ := v.Newest("beta", "amd64", numericCompare); ok {
		t.Error("a channel with no targets has a newest")
	}
}

// A channel is a path prefix, so "dev/my-branch" and "dev" are different
// channels and neither sees the other's archives.
func TestVerified_aChannelOnlySeesTheTargetsUnderItsOwnPrefix(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, map[string][]byte{
		ArchiveTarget("dev/my-branch", "0.3.2", "amd64"): []byte("a"),
		ArchiveTarget("dev/other", "0.3.3", "amd64"):     []byte("b"),
		ArchiveTarget("main", "0.3.0", "amd64"):          []byte("c"),
	})
	v, err := Verify(r.metaFor(files), Seen{}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	_, ref, ok, err := v.Newest("dev/my-branch", "amd64", numericCompare)
	if err != nil || !ok || ref.Version != "0.3.2" {
		t.Fatalf("newest = %+v, ok=%v, err=%v", ref, ok, err)
	}
	if _, _, ok, _ := v.Newest("dev", "amd64", numericCompare); ok {
		t.Error("the dev channel saw an archive of dev/my-branch")
	}
}

// The custom field is part of what the publisher signed. One that disagrees
// with the name is not a candidate.
func TestVerified_newestIgnoresATargetWhoseCustomFieldDisagreesWithItsName(t *testing.T) {
	good := ArchiveTarget("nightly", "1.0.0", "amd64")
	bad := ArchiveTarget("nightly", "2.0.0", "amd64")
	v := &Verified{Targets: map[string]Target{
		good: {Path: good, Custom: []byte(`{"version":"1.0.0","arch":"amd64","channel":"nightly"}`)},
		bad:  {Path: bad, Custom: []byte(`{"version":"1.0.0","arch":"amd64","channel":"nightly"}`)},
	}}
	_, ref, ok, err := v.Newest("nightly", "amd64", numericCompare)
	if err != nil || !ok || ref.Version != "1.0.0" {
		t.Fatalf("newest = %+v, ok=%v, err=%v: the mislabelled 2.0.0 should be skipped", ref, ok, err)
	}
	v.Targets[good] = Target{Path: good, Custom: []byte(`not json`)}
	if _, _, ok, _ := v.Newest("nightly", "amd64", numericCompare); ok {
		t.Error("a target with an unreadable custom field was a candidate")
	}
}

func TestVerified_newestIgnoresAVersionItCannotOrder(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, map[string][]byte{ArchiveTarget("stable", "1.0.0", "amd64"): []byte("a"), ArchiveTarget("stable", "1.x", "amd64"): []byte("b")})
	v, err := Verify(r.metaFor(files), Seen{}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	_, ref, ok, err := v.Newest("stable", "amd64", numericCompare)
	if err != nil || !ok || ref.Version != "1.0.0" {
		t.Fatalf("newest = %+v, ok=%v, err=%v: the unorderable 1.x should be skipped", ref, ok, err)
	}
}

func TestLookup_findsATargetWithoutRaisingTheRollbackRecord(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, defaultTargets())
	dir := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rootPath := filepath.Join(t.TempDir(), "root.json")
	if err := os.WriteFile(rootPath, r.root, 0o644); err != nil {
		t.Fatal(err)
	}
	seenPath := filepath.Join(t.TempDir(), "seen.json")
	check := FileCheck{RootPath: rootPath, SeenPath: seenPath, MetadataDir: dir, Target: stableTarget, Now: testNow}
	target, err := Lookup(check)
	if err != nil || target.Path != stableTarget || target.Length != int64(len("stable archive")) {
		t.Fatalf("target = %+v, err = %v", target, err)
	}
	if _, err := os.Stat(seenPath); err == nil {
		t.Fatal("a lookup raised the rollback record")
	}
	check.Target = "stable/orama-9-linux-amd64.tar.gz"
	if _, err := Lookup(check); err == nil {
		t.Fatal("a target the metadata does not name was found")
	}
	if err := writeSeen(seenPath, 9); err != nil {
		t.Fatal(err)
	}
	check.Target = stableTarget
	if _, err := Lookup(check); err == nil {
		t.Fatal("a rolled-back snapshot was looked up")
	}
}
