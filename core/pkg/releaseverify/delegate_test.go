package releaseverify

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

var delegationNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

const (
	stableTarget  = "stable/orama-0.3.1-linux-amd64.tar.gz"
	nightlyTarget = "nightly/orama-0.4.0-linux-amd64.tar.gz"
)

// channelRepo is a repository with the stable and nightly channels, each
// listing one archive.
type channelRepo struct {
	keys releaserepo.Keys
	root []byte
}

func newChannelRepo(t *testing.T) *channelRepo {
	t.Helper()
	keys, err := releaserepo.GenerateKeys("stable", "nightly")
	if err != nil {
		t.Fatal(err)
	}
	root, err := releaserepo.NewRoot(keys, delegationNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return &channelRepo{keys: keys, root: root}
}

func (r *channelRepo) files(t *testing.T, version int64, channelTargets map[string]map[string][]byte) map[string][]byte {
	t.Helper()
	files, err := releaserepo.Build(r.keys, releaserepo.Spec{
		Version: version, RootValidUntil: delegationNow.Add(24 * time.Hour),
		Delegated: []string{"stable", "nightly"}, ChannelTargets: channelTargets,
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func defaultChannels() map[string]map[string][]byte {
	return map[string]map[string][]byte{
		"stable":  {stableTarget: []byte("stable archive")},
		"nightly": {nightlyTarget: []byte("nightly archive")},
	}
}

// metaFor is the metadata a client holds after fetching the given roles.
func (r *channelRepo) metaFor(files map[string][]byte, roles ...string) Metadata {
	meta := Metadata{
		Root: r.root, Timestamp: files[TimestampFile], Snapshot: files[SnapshotFile], Targets: files[TargetsFile],
		Delegated: map[string][]byte{},
	}
	for _, role := range roles {
		meta.Delegated[role] = files[role+".json"]
	}
	return meta
}

func TestVerify_delegatedChannelTargetsAreTrustedUnderTheirRole(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, defaultChannels())
	v, err := Verify(r.metaFor(files, "stable"), Seen{}, delegationNow)
	if err != nil {
		t.Fatalf("a channel the client fetched was refused: %v", err)
	}
	got, ok := v.Targets[stableTarget]
	if !ok || got.Role != "stable" {
		t.Fatalf("stable archive = %+v (listed: %v), want role stable", got, ok)
	}
	if _, ok := v.Targets[nightlyTarget]; ok {
		t.Fatal("a channel the client did not fetch is trusted")
	}
	if err := got.Match([]byte("stable archive")); err != nil {
		t.Fatalf("the archive does not match its target: %v", err)
	}
}

func TestVerify_aRoleTheTargetsDoNotDelegateIsRefused(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, defaultChannels())
	meta := r.metaFor(files, "stable")
	meta.Delegated["beta"] = files["stable.json"]
	if _, err := Verify(meta, Seen{}, delegationNow); !errors.Is(err, ErrTargetPath) {
		t.Fatalf("err = %v, want ErrTargetPath", err)
	}
}

func TestVerify_aChannelCannotVouchForAnotherChannelsPath(t *testing.T) {
	r := newChannelRepo(t)
	channels := defaultChannels()
	channels["stable"][nightlyTarget] = []byte("smuggled")
	files := r.files(t, 4, channels)
	if _, err := Verify(r.metaFor(files, "stable"), Seen{}, delegationNow); !errors.Is(err, ErrTargetPath) {
		t.Fatalf("err = %v, want ErrTargetPath", err)
	}
}

func TestVerify_aChannelWithItsSignaturesRemovedIsRefused(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, defaultChannels())
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(files["stable.json"], &doc); err != nil {
		t.Fatal(err)
	}
	doc["signatures"] = json.RawMessage("[]")
	files["stable.json"], _ = json.Marshal(doc)
	if _, err := Verify(r.metaFor(files, "stable"), Seen{}, delegationNow); err == nil {
		t.Fatal("a channel with no signatures was accepted")
	}
}

func TestVerify_aChannelSignedByTheOtherChannelsKeyIsRefused(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, defaultChannels())
	files["stable.json"] = files["nightly.json"]
	if _, err := Verify(r.metaFor(files, "stable"), Seen{}, delegationNow); err == nil {
		t.Fatal("nightly's metadata was accepted as stable's")
	}
}

func TestVerify_aChannelChangedAfterTheSnapshotIsRefused(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, defaultChannels())
	other := r.files(t, 4, map[string]map[string][]byte{"stable": {stableTarget: []byte("a different archive")}})
	files["stable.json"] = other["stable.json"]
	if _, err := Verify(r.metaFor(files, "stable"), Seen{}, delegationNow); err == nil {
		t.Fatal("a channel whose bytes the snapshot does not name was accepted")
	}
}

func TestVerify_channelsStillSufferFreezeAndRollback(t *testing.T) {
	r := newChannelRepo(t)
	files := r.files(t, 4, defaultChannels())
	if _, err := Verify(r.metaFor(files, "stable"), Seen{SnapshotVersion: 9}, delegationNow); !errors.Is(err, ErrRollback) {
		t.Fatalf("rollback: err = %v", err)
	}
	if _, err := Verify(r.metaFor(files, "stable"), Seen{}, delegationNow.Add(48*time.Hour)); err == nil {
		t.Fatal("metadata past its expiry was accepted")
	}
}

func TestCheckFile_readsTheChannelsItIsAskedFor(t *testing.T) {
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
	archive := filepath.Join(t.TempDir(), "orama.tar.gz")
	if err := os.WriteFile(archive, []byte("stable archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	check := FileCheck{
		RootPath: rootPath, SeenPath: filepath.Join(t.TempDir(), "seen.json"), MetadataDir: dir,
		Roles: []string{"stable"}, Target: stableTarget, File: f, Now: delegationNow,
	}
	if _, err := CheckFile(check); err != nil {
		t.Fatalf("a channel archive was refused: %v", err)
	}
	check.Target = nightlyTarget
	if _, err := CheckFile(check); err == nil || !strings.Contains(err.Error(), "does not name") {
		t.Fatalf("a target of an unfetched channel: err = %v", err)
	}
}

func TestCheckFile_aRoleNameThatIsAPathIsRefused(t *testing.T) {
	for _, bad := range []string{"../root", "a/b", "", "Stable", "targets", strings.Repeat("a", 33)} {
		if err := validRoleName(bad); err == nil {
			t.Errorf("role name %q was accepted", bad)
		}
	}
	if err := validRoleName("stable"); err != nil {
		t.Errorf("stable: %v", err)
	}
}
