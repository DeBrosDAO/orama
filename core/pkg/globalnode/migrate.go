package globalnode

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// ImportResult is what an import did.
type ImportResult struct {
	// Floor is the source's last sign state, or a restored backup's floor.
	Floor *SignState
	// Replaced is where a different key already on this host was moved.
	Replaced string
}

// ImportMigration installs the key from a bundle sealed to this host's
// migration key. The caller has checked the chain is stopped.
//
// The order is what keeps a failure safe. The floor is written first, then
// the state, and the key last: if anything fails before the key is in place,
// either the key is not here or CheckSignFloor refuses the state, so the
// chain cannot start signing as the validator from a state behind the old
// host. The state written is the source's, unless this host's is already
// ahead (a repeated import). A bundle from Reseal (a restored backup) has no
// state: restore is then required, the floor the operator gives
// (RestoreFloor), and it is written as both the floor and the state. A
// migration bundle takes no restore floor. The migration key is removed once
// the key is in place.
func (h Host) ImportMigration(blob []byte, restore *SignState) (ImportResult, error) {
	b, err := h.openWithMigrationKey(blob)
	if err != nil {
		return ImportResult{}, err
	}
	source, err := importState(b, restore)
	if err != nil {
		return ImportResult{}, err
	}
	uid, gid, err := h.Lookup(constants.ChainUser)
	if err != nil {
		return ImportResult{}, err
	}
	for _, dir := range []string{filepath.Dir(h.StatePath), filepath.Dir(h.KeyPath)} {
		if err := h.requireDir(dir); err != nil {
			return ImportResult{}, err
		}
	}
	var res ImportResult
	pub, err := ValidatorKeyPubKey(b.Key)
	if err != nil {
		return ImportResult{}, err
	}
	if res.Floor, err = h.installState(source, pub, uid, gid); err != nil {
		return res, err
	}
	if res.Replaced, err = h.installKey(b.Key, uid, gid); err != nil {
		return res, err
	}
	if err := h.CheckSignFloor(); err != nil {
		return res, err
	}
	if err := h.Root.Remove(h.recipientPath()); err != nil {
		return res, fmt.Errorf("remove the used migration key %s: %w", h.recipientPath(), err)
	}
	return res, nil
}

func (h Host) openWithMigrationKey(blob []byte) (Bundle, error) {
	if err := h.checkStateDir(); err != nil {
		return Bundle{}, err
	}
	privData, err := h.read(h.recipientPath())
	if err != nil {
		return Bundle{}, fmt.Errorf("no migration key on this host (run the migrate prepare step here first): %w", err)
	}
	priv, err := ParseX25519Hex(string(privData))
	if err != nil {
		return Bundle{}, fmt.Errorf("%s: %w", h.recipientPath(), err)
	}
	return OpenBundle(priv, blob)
}

// installKey writes key as priv_validator_key.json, owned by the chain
// account. A different key already there is quarantined first, never
// overwritten; the same key is left as it is.
func (h Host) installKey(key []byte, uid, gid int) (string, error) {
	replaced := ""
	existing, err := h.Root.ReadFile(h.KeyPath, rootfs.SmallFileLimit)
	switch {
	case err == nil:
		if same, cmpErr := samePubKey(existing, key); cmpErr != nil || same {
			return "", cmpErr
		}
		if replaced, err = h.quarantineKey("key-replaced"); err != nil {
			return "", err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("read %s: %w", h.KeyPath, err)
	}
	return replaced, h.writeChainFile(h.KeyPath, key, uid, gid)
}

// importState is the state an import starts from: the bundle's, or for a
// restored backup the operator's restore floor.
func importState(b Bundle, restore *SignState) ([]byte, error) {
	switch {
	case b.State != nil && restore != nil:
		return nil, fmt.Errorf("this bundle is a migration and carries the old host's sign state; a restore floor is only for a restored backup")
	case b.State != nil:
		return b.State, nil
	case restore == nil:
		return nil, fmt.Errorf("this bundle is a restored backup with no sign state; confirm the old host is destroyed and give the network's current height as the restore floor")
	default:
		return encodeSignState(*restore)
	}
}

// installState records the floor, then writes the sign state.
// A floor already recorded for the same key is never lowered; a floor of
// another key belongs to another validator and is replaced.
func (h Host) installState(source []byte, pub string, uid, gid int) (*SignState, error) {
	existing, err := h.Root.ReadFile(h.StatePath, rootfs.SmallFileLimit)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", h.StatePath, err)
	}
	floor, err := ParseSignState(source)
	if err != nil {
		return nil, err
	}
	recorded, err := h.readFloor()
	if err != nil {
		return nil, err
	}
	if recorded != nil && recorded.PubKey == pub && floor.Behind(recorded.State) {
		return nil, fmt.Errorf("this host already records a sign floor for this key at %s, above the %s this import would start from; a floor is never lowered (the bundle is older than a state signed here, or the restore height is too low)", recorded.State, floor)
	}
	if err := h.writeFloor(pub, source); err != nil {
		return nil, err
	}
	write := existing == nil
	if existing != nil {
		current, err := ParseSignState(existing)
		write = err != nil || current.Behind(floor)
	}
	if write {
		if err := h.writeChainFile(h.StatePath, source, uid, gid); err != nil {
			return nil, err
		}
	}
	return &floor, nil
}

func (h Host) writeChainFile(path string, data []byte, uid, gid int) error {
	if err := h.Root.WriteFile(path, data, secretMode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := h.Chown(h.Root, path, uid, gid); err != nil {
		return fmt.Errorf("chown %s to %s: %w", path, constants.ChainUser, err)
	}
	return nil
}

// requireDir checks a chain home directory exists; the key is never put in a
// home that `oramad init` has not made.
func (h Host) requireDir(dir string) error {
	if _, _, err := h.Root.DirOwner(dir); err != nil {
		return fmt.Errorf("%s is not there; initialise the chain home first with the global install's --init-chain: %w", dir, err)
	}
	return nil
}

func samePubKey(a, b []byte) (bool, error) {
	pa, err := ValidatorKeyPubKey(a)
	if err != nil {
		return false, fmt.Errorf("the key already on this host: %w", err)
	}
	pb, err := ValidatorKeyPubKey(b)
	if err != nil {
		return false, err
	}
	return pa == pb, nil
}
