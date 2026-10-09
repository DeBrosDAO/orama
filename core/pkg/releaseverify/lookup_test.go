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
	files := r.files(t, 4, map[string]map[string][]byte{
		"stable": {
			ArchiveTarget("stable", "0.3.1", "amd64"):  []byte("a"),
			ArchiveTarget("stable", "0.3.10", "amd64"): []byte("b"),
			ArchiveTarget("stable", "0.3.9", "amd64"):  []byte("c"),
			ArchiveTarget("stable", "9.9.9", "arm64"):  []byte("d"),
		},
		"nightly": {ArchiveTarget("nightly", "9.0.0", "amd64"): []byte("e")},
	})
	v, err := Verify(r.metaFor(files, "stable", "nightly"), Seen{}, delegationNow)
	if err != nil {
		t.Fatal(err)
	}
	target, ref, ok, err := v.Newest("stable", "amd64", numericCompare)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ref.Version != "0.3.10" || target.Role != "stable" {
		t.Fatalf("newest = %+v (%s)", ref, target.Path)
	}
	if _, _, ok, _ := v.Newest("beta", "amd64", numericCompare); ok {
		t.Error("a channel with no targets has a newest")
	}
}

func TestVerified_newestIgnoresAVersionItCannotOrder(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, map[string]map[string][]byte{
		"stable": {ArchiveTarget("stable", "1.0.0", "amd64"): []byte("a"), ArchiveTarget("stable", "1.x", "amd64"): []byte("b")},
	})
	v, err := Verify(r.metaFor(files, "stable"), Seen{}, delegationNow)
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
	files := r.files(t, 4, defaultChannels())
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
	check := FileCheck{RootPath: rootPath, SeenPath: seenPath, MetadataDir: dir, Roles: []string{"stable"}, Target: stableTarget, Now: delegationNow}
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
