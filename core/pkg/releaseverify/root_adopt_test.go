package releaseverify_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

// rootChain is three consecutive versions of one release root, each signed by its predecessor.
type rootChain struct {
	v1, v2, v3 []byte
	other      []byte // an unrelated version 1
	now        time.Time
}

func newRootChain(t *testing.T) rootChain {
	t.Helper()
	keys := make([]releaserepo.Keys, 4)
	for i := range keys {
		k, err := releaserepo.GenerateKeys()
		if err != nil {
			t.Fatal(err)
		}
		keys[i] = k
	}
	now := time.Now()
	until := now.Add(48 * time.Hour)
	v1, err := releaserepo.NewRoot(keys[0], until)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := releaserepo.NextRoot(v1, keys[0], keys[1], until)
	if err != nil {
		t.Fatal(err)
	}
	v3, err := releaserepo.NextRoot(v2, keys[1], keys[2], until)
	if err != nil {
		t.Fatal(err)
	}
	other, err := releaserepo.NewRoot(keys[3], until)
	if err != nil {
		t.Fatal(err)
	}
	return rootChain{v1: v1, v2: v2, v3: v3, other: other, now: now}
}

func adoptedAt(t *testing.T, root []byte, now time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release-root.json")
	if _, err := releaseverify.AdoptRoot(path, root, now, releaseverify.AdoptSuccessor); err != nil {
		t.Fatal(err)
	}
	return path
}

// A manifest signed by a trusted wallet carries a root, and AdoptRoot used to take it whatever its
// version. A root older than the one adopted brings back the keys the rotation retired.
func TestAdoptRoot_refusesARootOlderThanTheAdoptedOne(t *testing.T) {
	c := newRootChain(t)
	path := adoptedAt(t, c.v2, c.now)
	for _, policy := range []releaseverify.AdoptPolicy{releaseverify.AdoptSuccessor, releaseverify.AdoptNewer} {
		changed, err := releaseverify.AdoptRoot(path, c.v1, c.now, policy)
		if err == nil || changed || !errors.Is(err, releaseverify.ErrRootRotation) {
			t.Fatalf("policy %d: root version 1 replaced the adopted version 2 (changed=%v err=%v)", policy, changed, err)
		}
	}
	got, _ := releaseverify.ReadRoot(path)
	if string(got) != string(c.v2) {
		t.Fatal("the adopted root changed after a refused adoption")
	}
}

func TestAdoptRoot_successorTakesOnlyTheNextVersionOrAFirstRoot(t *testing.T) {
	c := newRootChain(t)
	path := adoptedAt(t, c.v1, c.now)
	if _, err := releaseverify.AdoptRoot(path, c.v3, c.now, releaseverify.AdoptSuccessor); !errors.Is(err, releaseverify.ErrRootRotation) {
		t.Fatalf("a root two versions ahead was adopted as a successor: %v", err)
	}
	if _, err := releaseverify.AdoptRoot(path, c.other, c.now, releaseverify.AdoptSuccessor); !errors.Is(err, releaseverify.ErrRootRotation) {
		t.Fatalf("an unrelated root of the same version was adopted: %v", err)
	}
	changed, err := releaseverify.AdoptRoot(path, c.v2, c.now, releaseverify.AdoptSuccessor)
	if err != nil || !changed {
		t.Fatalf("the next version: changed=%v err=%v", changed, err)
	}
	changed, err = releaseverify.AdoptRoot(path, c.v2, c.now, releaseverify.AdoptSuccessor)
	if err != nil || changed {
		t.Fatalf("the adopted root again: changed=%v err=%v", changed, err)
	}
}

func TestAdoptRoot_newerTakesAJumpButNotASideStep(t *testing.T) {
	c := newRootChain(t)
	path := adoptedAt(t, c.v1, c.now)
	if _, err := releaseverify.AdoptRoot(path, c.other, c.now, releaseverify.AdoptNewer); !errors.Is(err, releaseverify.ErrRootRotation) {
		t.Fatalf("a different root of the same version was adopted as newer: %v", err)
	}
	changed, err := releaseverify.AdoptRoot(path, c.v3, c.now, releaseverify.AdoptNewer)
	if err != nil || !changed {
		t.Fatalf("a jump to version 3: changed=%v err=%v", changed, err)
	}
}

func TestAdoptRoot_replaceTakesAnyWellFormedRoot(t *testing.T) {
	c := newRootChain(t)
	path := adoptedAt(t, c.v3, c.now)
	changed, err := releaseverify.AdoptRoot(path, c.v1, c.now, releaseverify.AdoptReplace)
	if err != nil || !changed {
		t.Fatalf("an explicit replace: changed=%v err=%v", changed, err)
	}
	if _, err := releaseverify.AdoptRoot(path, []byte("garbage"), c.now, releaseverify.AdoptReplace); err == nil {
		t.Fatal("garbage was adopted by a replace")
	}
}
