package releaseverify

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

const (
	fuzzTarget  = "nightly/orama-1.2.3-linux-amd64.tar.gz"
	fuzzVersion = 5
)

var (
	fuzzNow       = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	fuzzRootEnds  = fuzzNow.Add(7 * 24 * time.Hour)
	fuzzFreezesAt = fuzzNow.Add(48 * time.Hour)
)

// fuzzRepo builds the same repository every run (the keys come from fixed seeds and ed25519 signs
// deterministically), so the corpus the fuzzer keeps stays a corpus of valid metadata.
func fuzzRepo(tb testing.TB) (Metadata, []byte) {
	tb.Helper()
	keys := releaserepo.Keys{}
	for i, role := range []string{metadata.ROOT, metadata.TIMESTAMP, metadata.SNAPSHOT, metadata.TARGETS} {
		keys[role] = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{byte(i + 1)}, ed25519.SeedSize))
	}
	root, err := releaserepo.NewRoot(keys, fuzzRootEnds)
	if err != nil {
		tb.Fatal(err)
	}
	archive := []byte("a release archive")
	custom, _ := json.Marshal(ArchiveCustom{Version: "1.2.3", Arch: "amd64", Channel: "nightly"})
	files, err := releaserepo.Build(keys, releaserepo.Spec{
		Version: fuzzVersion, RootValidUntil: fuzzRootEnds, TimestampExpires: fuzzFreezesAt,
		Targets: map[string][]byte{fuzzTarget: archive}, Custom: map[string]json.RawMessage{fuzzTarget: custom},
	})
	if err != nil {
		tb.Fatal(err)
	}
	return Metadata{Root: root, Timestamp: files[TimestampFile], Snapshot: files[SnapshotFile], Targets: files[TargetsFile]}, archive
}

// FuzzReleaseVerify: the TUF metadata and archive a node is handed are attacker-controlled bytes. For
// any mutation of them, Verify never panics, and when it accepts, it has accepted exactly the
// release the signers published and nothing else: the one target with its signed length, hashes
// and custom field, at the signed snapshot version; the clock is before the timestamp's expiry (a
// freeze is refused); the rollback record is not above the snapshot (a replay is refused); and
// VerifyArchive accepts only the published archive bytes.
func FuzzReleaseVerify(f *testing.F) {
	meta, archive := fuzzRepo(f)
	f.Add(meta.Root, meta.Timestamp, meta.Snapshot, meta.Targets, archive, int64(0), int32(0))
	f.Add(meta.Root, meta.Timestamp, meta.Snapshot, meta.Targets, archive, int64(fuzzVersion), int32(47))
	f.Add(meta.Root, meta.Timestamp, meta.Snapshot, meta.Targets, archive, int64(fuzzVersion+1), int32(0))
	f.Add(meta.Root, meta.Timestamp, meta.Snapshot, meta.Targets, archive, int64(0), int32(49))
	f.Add(meta.Root, meta.Timestamp, meta.Snapshot, meta.Targets, []byte("other bytes"), int64(0), int32(0))
	f.Add([]byte("{}"), []byte("{}"), []byte("{}"), []byte("{}"), []byte{}, int64(-1), int32(0))
	f.Fuzz(func(t *testing.T, root, timestamp, snapshot, targets, content []byte, seen int64, hours int32) {
		now := fuzzNow.Add(time.Duration(hours%400) * time.Hour)
		in := Metadata{Root: root, Timestamp: timestamp, Snapshot: snapshot, Targets: targets}
		verified, err := Verify(in, Seen{SnapshotVersion: seen}, now)
		if err != nil {
			if _, err := VerifyArchive(in, Seen{SnapshotVersion: seen}, now, fuzzTarget, content); err == nil {
				t.Fatal("VerifyArchive accepted metadata that Verify refuses")
			}
			return
		}
		if len(verified.Targets) != 1 || verified.SnapshotVersion != fuzzVersion {
			t.Fatalf("accepted %d targets at snapshot %d, the signers published one at %d", len(verified.Targets), verified.SnapshotVersion, fuzzVersion)
		}
		target, ok := verified.Targets[fuzzTarget]
		if !ok || target.Length != int64(len(archive)) || target.Match(archive) != nil {
			t.Fatalf("accepted a target other than the published one: %+v", verified.Targets)
		}
		if !now.Before(fuzzFreezesAt) {
			t.Fatalf("accepted metadata at %s, after the timestamp expired at %s", now, fuzzFreezesAt)
		}
		if seen > fuzzVersion {
			t.Fatalf("accepted snapshot %d with %d already seen", fuzzVersion, seen)
		}
		if seen < 0 {
			t.Fatal("accepted a negative rollback record")
		}
		_, archiveErr := VerifyArchive(in, Seen{SnapshotVersion: seen}, now, fuzzTarget, content)
		if (archiveErr == nil) != bytes.Equal(content, archive) {
			t.Fatalf("VerifyArchive error %v for %d bytes of content (published archive is %d bytes)", archiveErr, len(content), len(archive))
		}
		if errors.Is(archiveErr, ErrRollback) {
			t.Fatal("rollback reported for an accepted snapshot")
		}
	})
}

// FuzzReleaseArchiveName: a target name is read as a release archive only in the one form the
// builder writes, and reading it back gives the name again, so two names never share a channel,
// version and architecture.
func FuzzReleaseArchiveName(f *testing.F) {
	for _, s := range []string{fuzzTarget, "main/orama-0.3.0-linux-arm64.tar.gz", "dev/my-branch/orama-0.3.1-linux-amd64.tar.gz", "../x/orama-1-linux-amd64.tar.gz", "nightly/orama--linux-amd64.tar.gz", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		ref, err := ParseArchiveTarget(name)
		if err != nil {
			return
		}
		if ValidChannel(ref.Channel) != nil {
			t.Fatalf("parsed channel %q is not a valid channel", ref.Channel)
		}
		if ArchiveTarget(ref.Channel, ref.Version, ref.Arch) != name {
			t.Fatalf("name %q does not round trip through %+v", name, ref)
		}
		if strings.ContainsAny(name, "\x00\\ ") || strings.Count(name, "/") > 2 || strings.HasPrefix(name, "/") {
			t.Fatalf("name %q carries a path trick", name)
		}
	})
}
