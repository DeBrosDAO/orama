//go:build unix

package rootfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCreateExclusive_createsOnceWithMode(t *testing.T) {
	r, anchor, _ := newTree(t)
	path := filepath.Join(anchor, "configs", "marker")
	if err := r.CreateExclusive(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v (%v), want 0600", info, err)
	}
	err = r.CreateExclusive(path, []byte("y"), 0o600)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second create: %v, want fs.ErrExist", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "x" {
		t.Fatalf("the second create changed the file: %q", got)
	}
}

func TestCreateExclusive_refusesASymlinkLeaf(t *testing.T) {
	r, anchor, _ := newTree(t)
	target := filepath.Join(t.TempDir(), "target")
	link := filepath.Join(anchor, "configs", "marker")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateExclusive(link, nil, 0o600); err == nil {
		t.Fatal("created through a symlink")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("the symlink's target was created")
	}
}

func TestCreateExclusive_exactlyOneOfManyRacersWins(t *testing.T) {
	r, anchor, _ := newTree(t)
	path := filepath.Join(anchor, "configs", "marker")
	const racers = 16
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- r.CreateExclusive(path, nil, 0o600)
		}()
	}
	wg.Wait()
	close(errs)
	won := 0
	for err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, fs.ErrExist):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d creates succeeded, want 1", won)
	}
}
