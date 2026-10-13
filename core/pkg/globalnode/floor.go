package globalnode

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// exportSentinelFile exists while a migration export runs. CheckSignFloor
// refuses every start while it is there, so a start that races the export
// cannot run between the chain stopping and the floor being written.
const exportSentinelFile = "validator-export-in-progress"

// writableByOthers are the mode bits that let another account change the
// entries of a directory.
const writableByOthers = 0o022

func (h Host) sentinelPath() string { return filepath.Join(h.StateDir, exportSentinelFile) }

// checkStateDir refuses a state root that another account could write: the
// floor, the migration key and the key copies in it are trusted only because
// nobody but root can create, replace or remove them.
func (h Host) checkStateDir() error {
	info, err := os.Lstat(h.StateDir)
	if err != nil {
		return fmt.Errorf("check %s: %w", h.StateDir, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok {
		return fmt.Errorf("%s is not a directory (a symlink is refused)", h.StateDir)
	}
	if int(st.Uid) != h.StateOwner {
		return fmt.Errorf("%s is owned by uid %d, not %d; its sign floor and keys cannot be trusted (chown root %s)", h.StateDir, st.Uid, h.StateOwner, h.StateDir)
	}
	if info.Mode().Perm()&writableByOthers != 0 {
		return fmt.Errorf("%s is writable by its group or others (mode %o); its sign floor and keys cannot be trusted (chmod go-w %s)", h.StateDir, info.Mode().Perm(), h.StateDir)
	}
	return nil
}

// CheckSignFloor is the double-sign guard the chain unit runs before every
// start (ExecStartPre) and `orama global start` runs too. It refuses while a
// migration export is in progress. With no floor recorded there is nothing
// else to check. With any, the chain may start only when a validator key is in
// the chain home (a missing key means it moved to another host, and oramad
// would otherwise generate a fresh one), and, when a floor is recorded for
// that key, only when its sign state is not behind that floor. A key with no
// floor recorded here has never been migrated to or from this host.
func (h Host) CheckSignFloor() error {
	floors, err := h.readFloors()
	if err != nil {
		return err
	}
	if present, err := h.sentinelPresent(); err != nil {
		return err
	} else if present {
		return fmt.Errorf("a validator migration export is in progress (%s); the chain must not start", h.sentinelPath())
	}
	if len(floors) == 0 {
		return nil
	}
	key, err := h.Root.ReadFile(h.KeyPath, rootfs.SmallFileLimit)
	if err != nil {
		return fmt.Errorf("a sign floor is recorded but the validator key is not usable: %w; the key was migrated to another host. The key and state copies in %s may be put back only to abandon a migration, and only if the new host never started the chain", err, h.StateDir)
	}
	pub, err := ValidatorKeyPubKey(key)
	if err != nil {
		return err
	}
	floor, ok := floors[pub]
	if !ok {
		return nil
	}
	stateData, err := h.read(h.StatePath)
	if err != nil {
		return fmt.Errorf("a sign floor is recorded for this key but the sign state is unreadable: %w", err)
	}
	state, err := ParseSignState(stateData)
	if err != nil {
		return err
	}
	return CheckNotBehind(state, floor)
}

// MigratedAway reports whether this host's validator key was migrated away:
// a floor is recorded and no key is in the chain home. It is for a warning;
// CheckSignFloor is what refuses the start.
func (h Host) MigratedAway() (bool, error) {
	floors, err := h.readFloors()
	if err != nil || len(floors) == 0 {
		return false, err
	}
	_, err = h.Root.ReadFile(h.KeyPath, rootfs.SmallFileLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", h.KeyPath, err)
	}
	return false, nil
}
