package releaseverify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// rootChain is a root history: versions[i] is root version i+1, signed by the
// keys of the version before it and by its own.
type rootChain struct {
	keys     []releaserepo.Keys
	versions [][]byte
}

func newRootChain(t *testing.T, length int, rotateAll bool) *rootChain {
	t.Helper()
	first, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	until := testNow.Add(24 * time.Hour)
	root, err := releaserepo.NewRoot(first, until)
	if err != nil {
		t.Fatal(err)
	}
	c := &rootChain{keys: []releaserepo.Keys{first}, versions: [][]byte{root}}
	for len(c.versions) < length {
		next := c.keys[len(c.keys)-1]
		if rotateAll {
			if next, err = releaserepo.GenerateKeys(); err != nil {
				t.Fatal(err)
			}
		}
		root, err := releaserepo.NextRoot(c.versions[len(c.versions)-1], c.keys[len(c.keys)-1], next, until)
		if err != nil {
			t.Fatal(err)
		}
		c.keys, c.versions = append(c.keys, next), append(c.versions, root)
	}
	return c
}

// served is the N.root.json files of versions from..to (1-based, inclusive).
func (c *rootChain) served(from, to int) map[string][]byte {
	files := map[string][]byte{}
	for v := from; v <= to; v++ {
		files[fmt.Sprintf("%d.root.json", v)] = c.versions[v-1]
	}
	return files
}

// adopted writes version v as the node's adopted root and returns the paths.
func (c *rootChain) adopted(t *testing.T, v int) RootUpdate {
	t.Helper()
	dir := t.TempDir()
	u := RootUpdate{RootPath: filepath.Join(dir, "release-root.json"), SeenPath: filepath.Join(dir, "release-seen.json"), Now: testNow}
	if err := os.WriteFile(u.RootPath, c.versions[v-1], 0o644); err != nil {
		t.Fatal(err)
	}
	return u
}

func adoptedVersion(t *testing.T, u RootUpdate, c *rootChain) int {
	t.Helper()
	got, err := os.ReadFile(u.RootPath)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range c.versions {
		if string(v) == string(got) {
			return i + 1
		}
	}
	t.Fatal("the adopted root is none of the chain's")
	return 0
}

func TestUpdateRoot_adoptsTheNewestVersionOfAChain(t *testing.T) {
	c := newRootChain(t, 4, true)
	u := c.adopted(t, 1)
	repo := Repository{BaseURL: serveRepo(t, c.served(2, 4), nil)}

	n, err := repo.UpdateRoot(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || adoptedVersion(t, u, c) != 4 {
		t.Fatalf("applied %d versions, adopted version %d; want 3 and 4", n, adoptedVersion(t, u, c))
	}
}

func TestUpdateRoot_noNewerRootChangesNothing(t *testing.T) {
	c := newRootChain(t, 2, true)
	u := c.adopted(t, 2)
	repo := Repository{BaseURL: serveRepo(t, c.served(1, 2), nil)}

	n, err := repo.UpdateRoot(context.Background(), u)
	if err != nil || n != 0 {
		t.Fatalf("applied %d, err %v; want a quiet no-op", n, err)
	}
	if adoptedVersion(t, u, c) != 2 {
		t.Fatal("the adopted root changed")
	}
}

func TestUpdateRoot_aVersionJumpIsRefused(t *testing.T) {
	c := newRootChain(t, 3, true)
	u := c.adopted(t, 1)
	// 2.root.json holds version 3: a client at 1 must not skip 2.
	repo := Repository{BaseURL: serveRepo(t, map[string][]byte{"2.root.json": c.versions[2]}, nil)}

	_, err := repo.UpdateRoot(context.Background(), u)
	if !errors.Is(err, ErrRootRotation) {
		t.Fatalf("err = %v, want ErrRootRotation", err)
	}
	if adoptedVersion(t, u, c) != 1 {
		t.Fatal("a refused rotation changed the adopted root")
	}
}

func TestUpdateRoot_aRollbackIsRefused(t *testing.T) {
	c := newRootChain(t, 3, true)
	u := c.adopted(t, 2)
	// The repository answers the request for version 3 with version 2 again.
	repo := Repository{BaseURL: serveRepo(t, map[string][]byte{"3.root.json": c.versions[1]}, nil)}

	if _, err := repo.UpdateRoot(context.Background(), u); !errors.Is(err, ErrRootRotation) {
		t.Fatalf("err = %v, want ErrRootRotation", err)
	}
}

// A root signed only by its own new keys is what an attacker who runs the
// repository can make; the adopted root's keys did not sign it.
func TestUpdateRoot_aRootNotSignedByThePreviousKeysIsRefused(t *testing.T) {
	c := newRootChain(t, 1, true)
	attacker, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	forged, err := releaserepo.NextRoot(c.versions[0], attacker, attacker, testNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	u := c.adopted(t, 1)
	repo := Repository{BaseURL: serveRepo(t, map[string][]byte{"2.root.json": forged}, nil)}

	if _, err := repo.UpdateRoot(context.Background(), u); !errors.Is(err, ErrRootRotation) {
		t.Fatalf("err = %v, want ErrRootRotation", err)
	}
	if adoptedVersion(t, u, c) != 1 {
		t.Fatal("a forged root was adopted")
	}
}

// A root the previous keys signed but that its own keys did not is a handover
// to keys nobody holds.
func TestUpdateRoot_aRootNotSignedByItsOwnKeysIsRefused(t *testing.T) {
	c := newRootChain(t, 1, true)
	next, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	both, err := releaserepo.NextRoot(c.versions[0], c.keys[0], next, testNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := metadata.Root().FromBytes(both)
	if err != nil {
		t.Fatal(err)
	}
	// NextRoot signs with the old root key first, then the new one: keep the first.
	parsed.Signatures = parsed.Signatures[:1]
	oldOnly, err := parsed.ToBytes(false)
	if err != nil {
		t.Fatal(err)
	}
	u := c.adopted(t, 1)
	repo := Repository{BaseURL: serveRepo(t, map[string][]byte{"2.root.json": oldOnly}, nil)}

	if _, err := repo.UpdateRoot(context.Background(), u); !errors.Is(err, ErrRootRotation) {
		t.Fatalf("err = %v, want ErrRootRotation", err)
	}
	if adoptedVersion(t, u, c) != 1 {
		t.Fatal("a root its own keys did not sign was adopted")
	}
}

func TestUpdateRoot_moreRotationsThanTheBoundAreRefused(t *testing.T) {
	c := newRootChain(t, maxRootRotations+2, false)
	u := c.adopted(t, 1)
	repo := Repository{BaseURL: serveRepo(t, c.served(2, len(c.versions)), nil)}

	_, err := repo.UpdateRoot(context.Background(), u)
	if !errors.Is(err, ErrRootRotation) || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("err = %v, want the bound to be reported", err)
	}
	if adoptedVersion(t, u, c) != 1 {
		t.Fatal("a chain over the bound was partly adopted")
	}
}

func TestUpdateRoot_exactlyTheBoundIsAccepted(t *testing.T) {
	c := newRootChain(t, maxRootRotations+1, false)
	u := c.adopted(t, 1)
	repo := Repository{BaseURL: serveRepo(t, c.served(2, len(c.versions)), nil)}

	n, err := repo.UpdateRoot(context.Background(), u)
	if err != nil || n != maxRootRotations {
		t.Fatalf("applied %d, err %v", n, err)
	}
}

func TestUpdateRoot_anExpiredNewestRootIsRefusedAndTheOldOneKept(t *testing.T) {
	c := newRootChain(t, 2, true)
	u := c.adopted(t, 1)
	u.Now = testNow.Add(48 * time.Hour)
	repo := Repository{BaseURL: serveRepo(t, c.served(2, 2), nil)}

	if _, err := repo.UpdateRoot(context.Background(), u); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v, want the expired root named", err)
	}
	if adoptedVersion(t, u, c) != 1 {
		t.Fatal("an expired root was adopted")
	}
}

func TestUpdateRoot_withoutAnAdoptedRootThereIsNothingToRotateFrom(t *testing.T) {
	c := newRootChain(t, 2, true)
	u := c.adopted(t, 1)
	if err := os.Remove(u.RootPath); err != nil {
		t.Fatal(err)
	}
	repo := Repository{BaseURL: serveRepo(t, c.served(2, 2), nil)}

	if _, err := repo.UpdateRoot(context.Background(), u); !errors.Is(err, ErrNoRoot) {
		t.Fatalf("err = %v, want ErrNoRoot", err)
	}
}

func TestUpdateRoot_aRepositoryErrorOtherThanNotFoundStopsTheUpdate(t *testing.T) {
	c := newRootChain(t, 1, true)
	u := c.adopted(t, 1)
	// Nothing listens on port 1: the failure is a refused connection, which is not "there is no newer root".
	repo := Repository{BaseURL: "http://127.0.0.1:1"}
	AllowLocalRepositories(t)

	if _, err := repo.UpdateRoot(context.Background(), u); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want a connection failure that is not a 404", err)
	}
}

func TestUpdateRoot_aZeroClockIsRefused(t *testing.T) {
	c := newRootChain(t, 1, true)
	u := c.adopted(t, 1)
	u.Now = time.Time{}
	if _, err := (Repository{BaseURL: "https://releases.example.org"}).UpdateRoot(context.Background(), u); err == nil {
		t.Fatal("a zero reference time was accepted")
	}
}

func TestUpdateRoot_rotatingTheSnapshotKeyClearsTheRollbackRecord(t *testing.T) {
	c := newRootChain(t, 2, true)
	u := c.adopted(t, 1)
	if err := writeSeen(u.SeenPath, 900); err != nil {
		t.Fatal(err)
	}
	repo := Repository{BaseURL: serveRepo(t, c.served(2, 2), nil)}

	if _, err := repo.UpdateRoot(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if seen, err := readSeen(u.SeenPath); err != nil || seen.SnapshotVersion != 0 {
		t.Fatalf("seen = %+v, %v; want the record cleared by the key rotation", seen, err)
	}
}

func TestUpdateRoot_aRotationThatKeepsTheSnapshotKeyKeepsTheRollbackRecord(t *testing.T) {
	c := newRootChain(t, 2, false)
	u := c.adopted(t, 1)
	if err := writeSeen(u.SeenPath, 900); err != nil {
		t.Fatal(err)
	}
	repo := Repository{BaseURL: serveRepo(t, c.served(2, 2), nil)}

	if _, err := repo.UpdateRoot(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if seen, err := readSeen(u.SeenPath); err != nil || seen.SnapshotVersion != 900 {
		t.Fatalf("seen = %+v, %v; want the record kept", seen, err)
	}
}

// After Sync the metadata is judged by the rotated root: metadata signed by
// the new keys verifies, which it would not under the first root.
func TestSync_readsTheChannelThroughTheRotatedRoot(t *testing.T) {
	first, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	until := testNow.Add(24 * time.Hour)
	root1, err := releaserepo.NewRoot(first, until)
	if err != nil {
		t.Fatal(err)
	}
	second, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	root2, err := releaserepo.NextRoot(root1, first, second, until)
	if err != nil {
		t.Fatal(err)
	}
	files, err := releaserepo.Build(second, releaserepo.Spec{
		Version: 5, RootValidUntil: until, Targets: map[string][]byte{nightlyTarget: []byte("nightly archive")},
	})
	if err != nil {
		t.Fatal(err)
	}
	files["2.root.json"] = root2
	u := RootUpdate{RootPath: filepath.Join(t.TempDir(), "release-root.json"), SeenPath: filepath.Join(t.TempDir(), "seen.json"), Now: testNow}
	if err := os.WriteFile(u.RootPath, root1, 0o644); err != nil {
		t.Fatal(err)
	}
	repo := Repository{BaseURL: serveRepo(t, files, nil)}
	dir := t.TempDir()

	if err := repo.Sync(context.Background(), dir, u); err != nil {
		t.Fatal(err)
	}
	v, err := Load(FileCheck{RootPath: u.RootPath, SeenPath: u.SeenPath, MetadataDir: dir, Now: testNow})
	if err != nil {
		t.Fatalf("the metadata does not verify under the rotated root: %v", err)
	}
	if _, ok := v.Targets[nightlyTarget]; !ok {
		t.Fatalf("targets = %v", v.Targets)
	}
}

// A chain that moves the snapshot key and moves it back still moved it: a
// record the middle key raised must not survive.
func TestUpdateRoot_aKeyRotatedAwayAndBackStillClearsTheRollbackRecord(t *testing.T) {
	c := newRootChain(t, 1, true)
	until := testNow.Add(24 * time.Hour)
	other, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	second, err := releaserepo.NextRoot(c.versions[0], c.keys[0], other, until)
	if err != nil {
		t.Fatal(err)
	}
	third, err := releaserepo.NextRoot(second, other, c.keys[0], until)
	if err != nil {
		t.Fatal(err)
	}
	c.keys, c.versions = append(c.keys, other, c.keys[0]), append(c.versions, second, third)
	u := c.adopted(t, 1)
	if err := writeSeen(u.SeenPath, 900); err != nil {
		t.Fatal(err)
	}
	repo := Repository{BaseURL: serveRepo(t, c.served(2, 3), nil)}

	if _, err := repo.UpdateRoot(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if seen, _ := readSeen(u.SeenPath); seen.SnapshotVersion != 0 {
		t.Fatalf("seen = %+v, want cleared", seen)
	}
}

// A caller that rebuilds RootPath from a pinned root on every run keeps the
// newest root in Adopted; the same rotation then clears the record once, not
// on every run.
func TestUpdateRoot_aCallerThatStartsFromAPinnedRootClearsTheRecordOncePerRotation(t *testing.T) {
	c := newRootChain(t, 2, true)
	repo := Repository{BaseURL: serveRepo(t, c.served(2, 2), nil)}
	adopted := filepath.Join(t.TempDir(), "adopted-root.json")
	run := func() RootUpdate {
		u := c.adopted(t, 1)
		u.Adopted = adopted
		return u
	}

	first := run()
	if err := writeSeen(first.SeenPath, 900); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateRoot(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if seen, _ := readSeen(first.SeenPath); seen.SnapshotVersion != 0 {
		t.Fatalf("first run: seen = %+v, want cleared", seen)
	}
	if got, err := os.ReadFile(adopted); err != nil || string(got) != string(c.versions[1]) {
		t.Fatalf("the newest root was not kept: %v", err)
	}

	again := run()
	if err := writeSeen(again.SeenPath, 900); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateRoot(context.Background(), again); err != nil {
		t.Fatal(err)
	}
	if seen, _ := readSeen(again.SeenPath); seen.SnapshotVersion != 900 {
		t.Fatalf("second run: seen = %+v, want the record kept", seen)
	}
	if got, _ := os.ReadFile(again.RootPath); string(got) != string(c.versions[1]) {
		t.Fatal("RootPath was not brought to the newest root")
	}
}

// Once a machine has adopted a root, a repository that stops serving it (or
// serves an older chain) does not take the machine back to retired keys.
func TestUpdateRoot_aRepositoryOfferingAnOlderRootThanTheAdoptedOneIsRefused(t *testing.T) {
	c := newRootChain(t, 2, true)
	adopted := filepath.Join(t.TempDir(), "adopted-root.json")
	if err := os.WriteFile(adopted, c.versions[1], 0o644); err != nil {
		t.Fatal(err)
	}
	u := c.adopted(t, 1)
	u.Adopted = adopted
	// The repository no longer serves 2.root.json.
	repo := Repository{BaseURL: serveRepo(t, nil, nil)}
	if _, err := repo.UpdateRoot(context.Background(), u); !errors.Is(err, ErrRootRotation) || !strings.Contains(err.Error(), "retired keys") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateRoot_anAdoptedRootOfAnotherChainIsNotTrusted(t *testing.T) {
	c := newRootChain(t, 2, true)
	foreign := newRootChain(t, 2, true)
	adopted := filepath.Join(t.TempDir(), "adopted-root.json")
	if err := os.WriteFile(adopted, foreign.versions[1], 0o644); err != nil {
		t.Fatal(err)
	}
	u := c.adopted(t, 1)
	u.Adopted = adopted
	repo := Repository{BaseURL: serveRepo(t, c.served(2, 2), nil)}
	if _, err := repo.UpdateRoot(context.Background(), u); !errors.Is(err, ErrRootRotation) || !strings.Contains(err.Error(), "another chain") {
		t.Fatalf("err = %v", err)
	}
}

func TestRotateRoot_adoptsTheNextVersionSignedByTheAdoptedKeys(t *testing.T) {
	c := newRootChain(t, 3, true)
	u := c.adopted(t, 1)

	changed, err := RotateRoot(u, c.versions[1])

	if err != nil || !changed || adoptedVersion(t, u, c) != 2 {
		t.Fatalf("changed %v, err %v, adopted version %d; want version 2", changed, err, adoptedVersion(t, u, c))
	}
	if changed, err = RotateRoot(u, c.versions[1]); err != nil || changed {
		t.Errorf("the adopted root again: changed %v, err %v; want nothing to do", changed, err)
	}
}

func TestRotateRoot_refusesWhatAClientFollowingTheRepositoryWouldRefuse(t *testing.T) {
	c := newRootChain(t, 3, true)
	other := newRootChain(t, 2, true)
	for name, tc := range map[string]struct {
		adopted int
		next    []byte
	}{
		"a jump of two versions":    {1, c.versions[2]},
		"a root of another chain":   {1, other.versions[1]},
		"an older root":             {2, c.versions[0]},
		"not a root":                {2, []byte(`{"signed":{}}`)},
		"an empty file":             {2, nil},
		"the same version, another": {1, other.versions[0]},
	} {
		u := c.adopted(t, tc.adopted)
		if changed, err := RotateRoot(u, tc.next); err == nil || changed {
			t.Errorf("%s: changed %v, err %v; want a refusal", name, changed, err)
		}
		if adoptedVersion(t, u, c) != tc.adopted {
			t.Errorf("%s: the adopted root was changed by a refused rotation", name)
		}
	}
}

func TestRotateRoot_anExpiredNextRootIsRefused(t *testing.T) {
	c := newRootChain(t, 2, true)
	u := c.adopted(t, 1)
	u.Now = testNow.Add(48 * time.Hour)

	if changed, err := RotateRoot(u, c.versions[1]); err == nil || changed {
		t.Fatalf("changed %v, err %v; want an expired root refused", changed, err)
	}
	if adoptedVersion(t, u, c) != 1 {
		t.Error("the adopted root was replaced by an expired one")
	}
}

func TestRotateRoot_rotatingTheSnapshotKeyClearsTheRollbackRecordAndKeepingItKeepsIt(t *testing.T) {
	for _, rotateAll := range []bool{true, false} {
		c := newRootChain(t, 2, rotateAll)
		u := c.adopted(t, 1)
		if err := writeSeen(u.SeenPath, 900); err != nil {
			t.Fatal(err)
		}
		if _, err := RotateRoot(u, c.versions[1]); err != nil {
			t.Fatal(err)
		}
		seen, err := readSeen(u.SeenPath)
		want := int64(900)
		if rotateAll {
			want = 0
		}
		if err != nil || int64(seen.SnapshotVersion) != want {
			t.Errorf("rotateAll=%v: seen %+v, %v; want the snapshot version %d", rotateAll, seen, err, want)
		}
	}
}

func TestRotateRoot_withoutAnAdoptedRootThereIsNothingToRotateFrom(t *testing.T) {
	c := newRootChain(t, 2, true)
	dir := t.TempDir()
	u := RootUpdate{RootPath: filepath.Join(dir, "none.json"), SeenPath: filepath.Join(dir, "seen.json"), Now: testNow}
	if _, err := RotateRoot(u, c.versions[1]); !errors.Is(err, ErrNoRoot) {
		t.Fatalf("err = %v, want ErrNoRoot", err)
	}
}
