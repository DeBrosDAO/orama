package globalnode

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
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
		if rmErr := h.Root.Remove(h.sentinelPath()); rmErr != nil {
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
	again, err := h.read(h.StatePath)
	if err != nil {
		return nil, Exported{}, err
	}
	if !bytes.Equal(again, state) {
		return nil, Exported{}, fmt.Errorf("%s changed while the export read it; the chain signed during the export", h.StatePath)
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
	pub, err := ValidatorKeyPubKey(key)
	if err != nil {
		return nil, Exported{}, err
	}
	sealed, err := SealBundle(recipient, key, state)
	if err != nil {
		return nil, Exported{}, err
	}
	if err := h.writeFloor(pub, state); err != nil {
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

// createSentinel creates the export sentinel with an exclusive create, so of
// two exports only one proceeds. One already there means another export
// runs, or one was interrupted.
func (h Host) createSentinel() error {
	err := h.Root.CreateExclusive(h.sentinelPath(), nil, secretMode)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s exists: another export is running, or one was interrupted; check the chain home before removing it", h.sentinelPath())
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", h.sentinelPath(), err)
	}
	return nil
}

// sentinelPresent reports whether the export sentinel exists.
func (h Host) sentinelPresent() (bool, error) {
	_, err := h.Root.ReadFile(h.sentinelPath(), rootfs.SmallFileLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check %s: %w", h.sentinelPath(), err)
	}
	return true, nil
}
