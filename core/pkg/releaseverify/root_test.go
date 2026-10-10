package releaseverify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

func TestValidateRoot_aWellFormedRootNamesItself(t *testing.T) {
	r := newChannelRepo(t)
	digest, err := ValidateRoot(r.root, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if digest != RootDigest(r.root) || len(digest) != 64 {
		t.Fatalf("digest %q", digest)
	}
}

func TestValidateRoot_refusals(t *testing.T) {
	r := newChannelRepo(t)
	if _, err := ValidateRoot(r.root, testNow.Add(72*time.Hour)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("an expired root: %v", err)
	}
	if _, err := ValidateRoot([]byte(`{"signed":{}}`), testNow); err == nil {
		t.Error("a root that is not one was accepted")
	}
	if _, err := ValidateRoot(nil, testNow); err == nil {
		t.Error("an empty root was accepted")
	}
	other, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := releaserepo.NewRoot(other, testNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	tampered := []byte(strings.Replace(string(foreign), `"version":1`, `"version":2`, 1))
	if _, err := ValidateRoot(tampered, testNow); err == nil {
		t.Error("a root whose content changed after it was signed was accepted")
	}
}

func TestAdoptRoot_writesOnceAndReportsChange(t *testing.T) {
	r := newChannelRepo(t)
	path := filepath.Join(t.TempDir(), "release-root.json")
	changed, err := AdoptRoot(path, r.root, testNow)
	if err != nil || !changed {
		t.Fatalf("first adoption: changed=%v err=%v", changed, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != rootFilePerm {
		t.Fatalf("root file: %v, %v", info, err)
	}
	changed, err = AdoptRoot(path, r.root, testNow)
	if err != nil || changed {
		t.Fatalf("the same root again: changed=%v err=%v", changed, err)
	}
	got, err := ReadRoot(path)
	if err != nil || string(got) != string(r.root) {
		t.Fatalf("ReadRoot = %q, %v", got, err)
	}
}

func TestAdoptRoot_aBadRootLeavesTheAdoptedOneAlone(t *testing.T) {
	r := newChannelRepo(t)
	path := filepath.Join(t.TempDir(), "release-root.json")
	if _, err := AdoptRoot(path, r.root, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := AdoptRoot(path, []byte("garbage"), testNow); err == nil {
		t.Fatal("garbage was adopted")
	}
	if got, _ := ReadRoot(path); string(got) != string(r.root) {
		t.Fatal("the adopted root changed after a refused adoption")
	}
}

func TestReadRoot_noRootIsErrNoRoot(t *testing.T) {
	_, err := ReadRoot(filepath.Join(t.TempDir(), "none.json"))
	if err == nil || !strings.Contains(err.Error(), ErrNoRoot.Error()) {
		t.Fatalf("err = %v", err)
	}
}

func TestStaged_recordFindAndCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-staged.json")
	if e, err := FindStaged(path, "m", "r"); err != nil || e != nil {
		t.Fatalf("an empty record found %v, %v", e, err)
	}
	for i := 0; i < maxStaged+3; i++ {
		err := RecordStaged(path, Endorsement{ManifestSHA256: string(rune('a' + i)), RootSHA256: "r", Target: "stable/x", SnapshotVersion: int64(i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if e, _ := FindStaged(path, "a", "r"); e != nil {
		t.Error("the oldest endorsement survived the cap")
	}
	last := string(rune('a' + maxStaged + 2))
	if e, err := FindStaged(path, last, "r"); err != nil || e == nil || e.SnapshotVersion != int64(maxStaged+2) {
		t.Fatalf("newest = %v, %v", e, err)
	}
	if e, _ := FindStaged(path, last, "another-root"); e != nil {
		t.Error("an endorsement under one root was found under another")
	}
}

func TestStaged_sameManifestIsReplacedNotRepeated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-staged.json")
	for v := int64(1); v <= 3; v++ {
		if err := RecordStaged(path, Endorsement{ManifestSHA256: "m", RootSHA256: "r", Target: "stable/x", SnapshotVersion: v}); err != nil {
			t.Fatal(err)
		}
	}
	file, err := readStaged(path)
	if err != nil || len(file.Entries) != 1 || file.Entries[0].SnapshotVersion != 3 {
		t.Fatalf("entries = %+v, %v", file.Entries, err)
	}
}

func TestStaged_incompleteEndorsementAndCorruptRecordAreErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-staged.json")
	if err := RecordStaged(path, Endorsement{ManifestSHA256: "m"}); err == nil {
		t.Error("an endorsement with no root or target was recorded")
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := FindStaged(path, "m", "r"); err == nil {
		t.Error("a corrupt record read as empty")
	}
	if err := RecordStaged(path, Endorsement{ManifestSHA256: "m", RootSHA256: "r", Target: "t"}); err == nil {
		t.Error("a corrupt record was overwritten")
	}
}

func TestArchiveTarget_roundTripAndRefusals(t *testing.T) {
	name := ArchiveTarget("stable", "0.3.1", "arm64")
	ref, err := ParseArchiveTarget(name)
	if err != nil || ref != (ArchiveRef{Channel: "stable", Version: "0.3.1", Arch: "arm64"}) {
		t.Fatalf("%q -> %+v, %v", name, ref, err)
	}
	dev := ArchiveTarget("dev/my-branch", "0.3.1", "amd64")
	if ref, err := ParseArchiveTarget(dev); err != nil || ref.Channel != "dev/my-branch" {
		t.Fatalf("%q -> %+v, %v", dev, ref, err)
	}
	for _, bad := range []string{
		"orama-0.3.1-linux-amd64.tar.gz", "stable/orama-0.3.1-linux-riscv64.tar.gz", "stable/../orama-1-linux-amd64.tar.gz",
		"stable/orama--linux-amd64.tar.gz", "Stable/orama-1.0-linux-amd64.tar.gz", "stable/x/y/orama-1.0-linux-amd64.tar.gz", "dev/Branch/orama-1.0-linux-amd64.tar.gz",
	} {
		if _, err := ParseArchiveTarget(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestValidChannel(t *testing.T) {
	for _, ok := range []string{"nightly", "main", "dev/my-branch", "dev/a", "a-b", strings.Repeat("a", 32), "dev/" + strings.Repeat("b", 32)} {
		if err := ValidChannel(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "Nightly", "dev/", "/dev", "dev//x", "dev/x/y", "a.b", "dev/feat_x", "../x", "x/..", strings.Repeat("a", 33), "dev/" + strings.Repeat("b", 33), "a b",
	} {
		if err := ValidChannel(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
