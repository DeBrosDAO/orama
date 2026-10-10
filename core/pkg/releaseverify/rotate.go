package releaseverify

import (
	"bytes"
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
	// Adopted, when set, is where the newest root this machine has adopted is
	// kept, for a caller whose RootPath is rebuilt from a pinned root on every
	// run (releasefetch). Whether keys changed is judged against it, not
	// against RootPath, so the same rotation does not clear the rollback
	// record on every run; the newest root is written to it when it is newer.
	// Empty: RootPath is the persistent record.
	Adopted string
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
	first, keysChanged := trusted.Root, false
	newest, applied := adopted, 0
	chain := map[int64][]byte{first.Signed.Version: adopted}
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
		before := trusted.Root
		if _, err := trusted.UpdateRoot(next); err != nil {
			return applied, fmt.Errorf("%w: root version %d: %w", ErrRootRotation, before.Signed.Version+1, err)
		}
		keysChanged = keysChanged || rolesChanged(before, trusted.Root, metadata.TIMESTAMP, metadata.SNAPSHOT)
		newest, applied = next, applied+1
		chain[trusted.Root.Signed.Version] = next
	}
	if err := checkAdoptedFloor(u.Adopted, trusted.Root.Signed.Version, chain); err != nil {
		return applied, err
	}
	return applied, r.settle(u, first, trusted.Root, newest, keysChanged)
}

// settle records the outcome of an update: the newest root becomes the adopted
// one and, if any step of the chain changed the timestamp or snapshot keys, the
// rollback record is cleared first. If the process stops between the two
// steps, the next update sees the same rotation and finishes it.
func (r Repository) settle(u RootUpdate, first, last *metadata.Metadata[metadata.RootType], newest []byte, keysChanged bool) error {
	if first.Signed.Version == last.Signed.Version {
		return nil
	}
	if _, err := ValidateRoot(newest, u.Now); err != nil {
		return fmt.Errorf("%w: the newest root (version %d): %w", ErrRootRotation, last.Signed.Version, err)
	}
	remember := u.Adopted != ""
	if remember {
		known, err := readAdoptedRoot(u.Adopted)
		if err != nil {
			return err
		}
		if known != nil {
			// Judged against what this machine adopted, not against the pinned
			// root the chain starts from.
			newer := known.Signed.Version < last.Signed.Version
			keysChanged = newer && rolesChanged(known, last, metadata.TIMESTAMP, metadata.SNAPSHOT)
			remember = newer
		}
	}
	if keysChanged {
		if err := clearSeen(u.SeenPath); err != nil {
			return err
		}
	}
	if _, err := AdoptRoot(u.RootPath, newest, u.Now); err != nil {
		return err
	}
	if remember {
		_, err := AdoptRoot(u.Adopted, newest, u.Now)
		return err
	}
	return nil
}

// readAdoptedRoot parses the root kept at path, or returns nil if there is none.
func readAdoptedRoot(path string) (*metadata.Metadata[metadata.RootType], error) {
	data, err := ReadRoot(path)
	if errors.Is(err, ErrNoRoot) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	root, err := metadata.Root().FromBytes(data)
	if err != nil {
		return nil, fmt.Errorf("parse the adopted release root %s: %w", path, err)
	}
	return root, nil
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

// checkAdoptedFloor holds the walk to what this machine has already adopted
// (u.Adopted): a repository that now offers an older newest root than the one
// adopted would have a client accept metadata signed by keys since retired, and
// an adopted root that is not the chain's root at its version belongs to another
// chain (another pin). Neither is a rotation to follow.
func checkAdoptedFloor(path string, newest int64, chain map[int64][]byte) error {
	if path == "" {
		return nil
	}
	data, err := ReadRoot(path)
	if errors.Is(err, ErrNoRoot) {
		return nil
	}
	if err != nil {
		return err
	}
	known, err := metadata.Root().FromBytes(data)
	if err != nil {
		return fmt.Errorf("parse the adopted release root %s: %w", path, err)
	}
	version := known.Signed.Version
	if version > newest {
		return fmt.Errorf("%w: the repository's newest root is version %d, older than the version %d this machine adopted (%s): refusing the retired keys", ErrRootRotation, newest, version, path)
	}
	if walked, ok := chain[version]; ok && !bytes.Equal(walked, data) {
		return fmt.Errorf("%w: the root adopted at %s is not the repository's version %d: it belongs to another chain, so it is not trusted", ErrRootRotation, path, version)
	}
	return nil
}

// RotateRoot adopts next as the root at u.RootPath when it is the next version of the root adopted
// there: signed by the adopted root's keys at their threshold and by its own, the way a client
// following the repository accepts it (TUF 5.3.2). It is what a node does with a root an operator
// pushes with a release: the pushed root is the newest the operator's fetch walked to, and the node
// follows it only through a chain of trust from the root it holds, never on the pusher's word.
// It reports whether the adopted root changed: next being the adopted root changes nothing. A next
// that is another root of the same version, an older one, or more than one version ahead (the
// versions between are not here to check) is refused, as is one that has expired. If the timestamp
// or snapshot keys change, the rollback record at u.SeenPath is cleared first, as UpdateRoot does.
func RotateRoot(u RootUpdate, next []byte) (bool, error) {
	if u.Now.IsZero() {
		return false, errors.New("reference time is required so an expired root is refused")
	}
	adopted, err := ReadRoot(u.RootPath)
	if err != nil {
		return false, err
	}
	if bytes.Equal(adopted, next) {
		return false, nil
	}
	trusted, err := trustedmetadata.New(adopted)
	if err != nil {
		return false, fmt.Errorf("the adopted release root: %w", err)
	}
	before := trusted.Root
	if _, err := trusted.UpdateRoot(next); err != nil {
		return false, fmt.Errorf("%w: the pushed root (version %d) does not follow the adopted version %d: %w",
			ErrRootRotation, versionOf(next), before.Signed.Version, err)
	}
	if _, err := ValidateRoot(next, u.Now); err != nil {
		return false, fmt.Errorf("%w: the pushed root (version %d): %w", ErrRootRotation, trusted.Root.Signed.Version, err)
	}
	if rolesChanged(before, trusted.Root, metadata.TIMESTAMP, metadata.SNAPSHOT) {
		if err := clearSeen(u.SeenPath); err != nil {
			return false, err
		}
	}
	return AdoptRoot(u.RootPath, next, u.Now)
}

// versionOf is the version a root file declares, 0 when it cannot be read.
func versionOf(data []byte) int64 {
	root, err := metadata.Root().FromBytes(data)
	if err != nil {
		return 0
	}
	return root.Signed.Version
}
