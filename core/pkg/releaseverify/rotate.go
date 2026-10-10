package releaseverify

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/trustedmetadata"
)

// A release repository publishes every version of its root as
// <version>.root.json beside the metadata (1.root.json, 2.root.json, ...). A
// client that holds version N asks for N+1, then N+2, until the repository
// answers 404, and accepts each only when it is signed by the root keys of the
// version before it and by its own (TUF 5.3.2). That is how a root key is
// rotated, or an expiring root replaced, without anyone handing a new root to
// every machine.
const (
	// maxRootRotations bounds how many root versions one update applies. A
	// repository that keeps answering with newer roots cannot keep a client
	// downloading (TUF's endless-data attack); a real root changes about once
	// a year, so this is generous.
	maxRootRotations = 32
	rootVersionFile  = "%d.root.json"
)

// ErrRootRotation is a root file that is not the next version of the adopted
// root: it is not signed by the adopted root's keys at their threshold, not
// signed by its own, or its version is not the adopted version plus one.
var ErrRootRotation = errors.New("root rotation")

// RootUpdate names the files a root update reads and changes.
type RootUpdate struct {
	// RootPath is the adopted root.json. A newer root replaces it.
	RootPath string
	// SeenPath is the rollback record. A rotation that changes the timestamp
	// or snapshot keys clears it: a record a compromised key raised must not
	// outlast the key (TUF 5.3.11, fast-forward attack recovery).
	SeenPath string
	// Now is the clock the final root's expiry is judged by.
	Now time.Time
}

// UpdateRoot brings the adopted root up to the newest version the repository
// publishes, and reports how many versions it applied. A repository with no
// newer root is not an error and changes nothing. Every intermediate root is
// verified against the one before it, and the newest is adopted only if it is
// well-formed and not expired (AdoptRoot).
func (r Repository) UpdateRoot(ctx context.Context, u RootUpdate) (int, error) {
	if u.Now.IsZero() {
		return 0, errors.New("reference time is required so an expired root is refused")
	}
	adopted, err := ReadRoot(u.RootPath)
	if err != nil {
		return 0, err
	}
	trusted, err := trustedmetadata.New(adopted)
	if err != nil {
		return 0, fmt.Errorf("the adopted release root: %w", err)
	}
	first := trusted.Root
	newest, applied := adopted, 0
	for {
		next, err := r.getMetadata(ctx, fmt.Sprintf(rootVersionFile, trusted.Root.Signed.Version+1))
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			return applied, err
		}
		if applied == maxRootRotations {
			return applied, fmt.Errorf("%w: the repository publishes more than %d root versions after version %d",
				ErrRootRotation, maxRootRotations, first.Signed.Version)
		}
		if _, err := trusted.UpdateRoot(next); err != nil {
			return applied, fmt.Errorf("%w: root version %d: %w", ErrRootRotation, trusted.Root.Signed.Version+1, err)
		}
		newest, applied = next, applied+1
	}
	if applied == 0 {
		return 0, nil
	}
	if err := adoptRotated(u, first, trusted.Root, newest); err != nil {
		return 0, err
	}
	return applied, nil
}

// adoptRotated makes newest the adopted root. When it replaces the timestamp or
// snapshot keys, the rollback record goes first: if the process stops between
// the two steps, the next update sees the same rotation and finishes it.
func adoptRotated(u RootUpdate, first, last *metadata.Metadata[metadata.RootType], newest []byte) error {
	if _, err := ValidateRoot(newest, u.Now); err != nil {
		return fmt.Errorf("%w: the newest root (version %d): %w", ErrRootRotation, last.Signed.Version, err)
	}
	if rolesChanged(first, last, metadata.TIMESTAMP, metadata.SNAPSHOT) {
		if err := clearSeen(u.SeenPath); err != nil {
			return err
		}
	}
	if _, err := AdoptRoot(u.RootPath, newest, u.Now); err != nil {
		return err
	}
	return nil
}

// rolesChanged reports whether any of roles has other keys or another
// threshold in b than in a.
func rolesChanged(a, b *metadata.Metadata[metadata.RootType], roles ...string) bool {
	for _, role := range roles {
		before, after := a.Signed.Roles[role], b.Signed.Roles[role]
		if before == nil || after == nil || before.Threshold != after.Threshold {
			return true
		}
		x, y := slices.Clone(before.KeyIDs), slices.Clone(after.KeyIDs)
		slices.Sort(x)
		slices.Sort(y)
		if !slices.Equal(x, y) {
			return true
		}
	}
	return false
}

// clearSeen removes the rollback record, under its lock. A missing record is
// already clear.
func clearSeen(path string) (err error) {
	unlock, err := lockSeen(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear the release rollback record after the snapshot or timestamp keys changed: %w", err)
	}
	return nil
}

// Sync is what a client does before it reads a channel: bring the adopted root
// up to date (UpdateRoot), then fetch the metadata into dir
// (FetchMetadata). The metadata is fetched after the root so that it is judged
// by the newest root the repository publishes.
func (r Repository) Sync(ctx context.Context, dir string, u RootUpdate) error {
	if _, err := r.UpdateRoot(ctx, u); err != nil {
		return err
	}
	return r.FetchMetadata(ctx, dir)
}
