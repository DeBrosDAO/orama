package globalnode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// floorFileFormat is what the sign floor file holds: one floor per validator
// key, keyed by the key's public key (canonical base64, as
// ValidatorKeyPubKey returns it), each CometBFT's priv_validator_state.json.
// An entry is never removed, so importing one key never erases another's.
const floorFileFormat = `{"floors":{"<pub_key base64>":<priv_validator_state.json>}}`

type floorFile struct {
	Floors map[string]json.RawMessage `json:"floors"`
}

// writeFloor records state as the floor of the key whose public key is
// pubKey, keeping every other key's floor.
func (h Host) writeFloor(pubKey string, state []byte) error {
	doc, err := h.readFloorFile()
	if err != nil {
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
	if doc.Floors == nil {
		return floorFile{}, h.badFloorFile("no \"floors\" object")
	}
	return doc, nil
}

// badFloorFile says what was wrong and how to re-record the floor.
func (h Host) badFloorFile(found string) error {
	return fmt.Errorf("%s is not a sign floor file (want %s): %s. Move it aside and re-record each migrated key's floor by importing its bundle again with the migrate import step; until then the chain does not start", h.floorPath(), floorFileFormat, found)
}
