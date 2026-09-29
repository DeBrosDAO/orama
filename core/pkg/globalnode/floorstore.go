package globalnode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// floorFileFormat is what the sign floor file holds: one floor per validator
// key, keyed by the key's public key (the canonical base64 ValidatorKeyPubKey
// returns), each CometBFT's priv_validator_state.json.
// An entry is never removed, so importing one key never erases another's.
const floorFileFormat = `{"floors":{"<pub_key base64>":<priv_validator_state.json>}}`

type floorFile struct {
	Floors map[string]json.RawMessage `json:"floors"`
}

// floorLockFile serialises every read-modify-write of the floor file.
const floorLockFile = "validator-sign-floor.lock"

// updateFloor records state as the floor of the key whose public key is
// pubKey, keeping every other key's floor. It holds an exclusive lock on the
// floor's lock file from the read to the write, so two exports or imports
// cannot lose each other's entry. check sees the key's recorded floor (nil
// when there is none) and the new one, and may refuse the update.
func (h Host) updateFloor(pubKey string, state []byte, check func(recorded *SignState, next SignState) error) error {
	next, err := ParseSignState(state)
	if err != nil {
		return err
	}
	unlock, err := h.lockFloor()
	if err != nil {
		return err
	}
	defer unlock()
	doc, err := h.readFloorFile()
	if err != nil {
		return err
	}
	var recorded *SignState
	if raw, ok := doc.Floors[pubKey]; ok {
		parsed, err := ParseSignState(raw)
		if err != nil {
			return h.badFloorFile(fmt.Sprintf("the entry for key %s: %v", pubKey, err))
		}
		recorded = &parsed
	}
	if err := check(recorded, next); err != nil {
		return err
	}
	doc.Floors[pubKey] = state
	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encode the sign floor: %w", err)
	}
	if err := h.Root.WriteFile(h.floorPath(), data, secretMode); err != nil {
		return fmt.Errorf("record the sign floor %s: %w", h.floorPath(), err)
	}
	return nil
}

// neverLower refuses a floor behind the one already recorded for the key.
func neverLower(recorded *SignState, next SignState) error {
	if recorded != nil && next.Behind(*recorded) {
		return fmt.Errorf("this host already records a sign floor for this key at %s, above %s; a floor is never lowered (a state signed here is newer)", *recorded, next)
	}
	return nil
}

// lockFloor takes an exclusive flock on the lock file in the state root,
// opened without following a symlink, and returns its release.
func (h Host) lockFloor() (func(), error) {
	if err := h.checkStateDir(); err != nil {
		return nil, err
	}
	path := filepath.Join(h.StateDir, floorLockFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, secretMode)
	if err != nil {
		return nil, fmt.Errorf("open the sign floor lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() { f.Close() }, nil
}

// readFloors is every recorded floor by public key; empty when there is
// none. A state root that does not exist yet has none.
func (h Host) readFloors() (map[string]SignState, error) {
	doc, err := h.readFloorFile()
	if err != nil {
		return nil, err
	}
	out := make(map[string]SignState, len(doc.Floors))
	for pub, raw := range doc.Floors {
		state, err := ParseSignState(raw)
		if err != nil {
			return nil, h.badFloorFile(fmt.Sprintf("the entry for key %s: %v", pub, err))
		}
		out[pub] = state
	}
	return out, nil
}

func (h Host) readFloorFile() (floorFile, error) {
	empty := floorFile{Floors: map[string]json.RawMessage{}}
	if _, err := os.Lstat(h.StateDir); errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err := h.checkStateDir(); err != nil {
		return floorFile{}, err
	}
	data, err := h.Root.ReadFile(h.floorPath(), rootfs.SmallFileLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return floorFile{}, fmt.Errorf("read %s: %w", h.floorPath(), err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc floorFile
	if err := dec.Decode(&doc); err != nil {
		return floorFile{}, h.badFloorFile(err.Error())
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return floorFile{}, h.badFloorFile("data after the JSON object")
	}
	if doc.Floors == nil {
		return floorFile{}, h.badFloorFile("no \"floors\" object")
	}
	return doc, nil
}

// badFloorFile says what was wrong and how to write the file again. A
// bundle cannot be imported twice (its one-time key is gone), so the floors
// are rewritten by hand from each key's last known state.
func (h Host) badFloorFile(found string) error {
	return fmt.Errorf("%s is not a sign floor file (want %s): %s. The chain does not start until it is fixed: move it aside, then write %s again, mode 0600, with one entry per migrated key: the pub_key value of its priv_validator_key.json (or of its validator-key-*.json copy in %s) and its last known priv_validator_state.json, which is the newest of the chain home's data/priv_validator_state.json, the validator-state-*.json copies in %s, and the state file of any other host that ran the key. Check that state before writing it; a floor below what the key signed does not protect it", h.floorPath(), floorFileFormat, found, h.floorPath(), h.StateDir, h.StateDir)
}
