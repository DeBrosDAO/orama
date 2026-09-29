package globalnode

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// Exported is what ExportMigration left behind on the old host.
type Exported struct {
	State     SignState
	KeyCopy   string
	StateCopy string
}

// ExportMigration seals the key and its last sign state to recipient, in
// memory, then makes this host unable to sign as the validator: it records
// the state as this host's sign floor, keeps a copy of the state, and moves
// the key out of the chain home into the state root. The caller has stopped
// and disabled the chain, and writes the returned bundle afterwards.
//
// An export-in-progress sentinel is written before anything is read and
// removed at the end, so a start that races the export is refused by
// CheckSignFloor. stillStopped is asked again after the state is read: a
// chain that started in between may have signed past it. From the end of the
// export on, CheckSignFloor refuses the chain here because the floor is
// recorded and the key is gone; oramad cannot generate a fresh key and a zero
// state in its place.
func (h Host) ExportMigration(recipient *[32]byte, stillStopped func() error) (sealed []byte, out Exported, err error) {
	if err := h.checkStateDir(); err != nil {
		return nil, Exported{}, err
	}
	if err := h.createSentinel(); err != nil {
		return nil, Exported{}, err
	}
	defer func() {
		if rmErr := os.Remove(h.sentinelPath()); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove the export sentinel %s: %w", h.sentinelPath(), rmErr))
		}
	}()
	key, state, err := h.readKeyAndState()
	if err != nil {
		return nil, Exported{}, err
	}
	if err := stillStopped(); err != nil {
		return nil, Exported{}, fmt.Errorf("the chain is no longer stopped after its state was read: %w", err)
	}
	return h.leave(recipient, key, state)
}

func (h Host) readKeyAndState() ([]byte, []byte, error) {
	key, err := h.read(h.KeyPath)
	if err != nil {
		return nil, nil, err
	}
	state, err := h.read(h.StatePath)
	if err != nil {
		return nil, nil, err
	}
	return key, state, nil
}

// leave seals, records the floor, keeps the state and moves the key away.
func (h Host) leave(recipient *[32]byte, key, state []byte) ([]byte, Exported, error) {
	parsed, err := ParseSignState(state)
	if err != nil {
		return nil, Exported{}, err
	}
	sealed, err := SealBundle(recipient, key, state)
	if err != nil {
		return nil, Exported{}, err
	}
	if err := h.writeFloor(state); err != nil {
		return nil, Exported{}, err
	}
	out := Exported{State: parsed}
	if out.StateCopy, err = h.keepCopy(state, "state-migrated"); err != nil {
		return nil, out, err
	}
	if out.KeyCopy, err = h.quarantineKey("key-migrated"); err != nil {
		return nil, out, err
	}
	return sealed, out, nil
}

// createSentinel creates the export sentinel; one already there means another
// export runs, or one was interrupted.
func (h Host) createSentinel() error {
	f, err := os.OpenFile(h.sentinelPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, secretMode)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s exists: another export is running, or one was interrupted; check the chain home before removing it", h.sentinelPath())
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", h.sentinelPath(), err)
	}
	return f.Close()
}
