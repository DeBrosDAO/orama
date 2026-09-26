package legacylayout

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// The index gateway's signing keys leave secrets/, which install writes as
// root. When orama-node may write that directory the keys are renamed out of
// it like everything else. When it may not (it is root's, or the unit mounts it
// read-only), they are copied and the copies recorded in CopiedKeysMarker; the
// upgrade, as root, then deletes an original only when its contents match the
// digest recorded there and the copy exists (ProductionSetup.
// RemoveCopiedSigningKeys) — two fixed names in one directory, opened without
// following symlinks.

// CopiedKeysMarkerName records, in the index gateway's state directory, the
// SHA-256 of each signing key copied out of secrets/.
const CopiedKeysMarkerName = "legacy-signing-keys.copied"

// copiedKeysMarkerMode keeps the marker private like the keys beside it.
const copiedKeysMarkerMode = 0o600

// access(2) modes: may create/remove entries (write) and reach them (search).
const (
	accessWrite  = 0x2
	accessSearch = 0x1
)

// CopiedKeysMarker is the marker's path.
func CopiedKeysMarker(oramaDir string) string {
	return filepath.Join(IndexGatewayStateDir(oramaDir), CopiedKeysMarkerName)
}

// KeyDigest is how the marker names a key's contents.
func KeyDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ReadCopiedKeys returns key name → digest from the marker at path; an absent
// marker is an empty map.
func ReadCopiedKeys(path string) (map[string]string, error) {
	data, _, err := readNoFollow(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	copied := map[string]string{}
	if err := json.Unmarshal(data, &copied); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return copied, nil
}

// keyCopy is one signing key still in secrets/.
type keyCopy struct {
	name, from, to string
	// copyOnly: secrets/ is not writable by this process.
	copyOnly bool
}

// keyPlan is what planSigningKeys decided.
type keyPlan struct {
	pending []keyCopy
	// clearMarker: the marker records copies whose originals root has since
	// deleted, so it has nothing left to record.
	clearMarker bool
}

// planSigningKeys lists the keys to take out of secrets/.
func (m Migrator) planSigningKeys() (keyPlan, error) {
	markerPath := CopiedKeysMarker(m.OramaDir)
	copied, err := ReadCopiedKeys(markerPath)
	if err != nil {
		return keyPlan{}, err
	}
	var pending []keyCopy
	var conflicts []error
	remaining := 0
	for _, name := range SigningKeyNames {
		k := keyCopy{name: name, from: filepath.Join(SecretsDir(m.OramaDir), name), to: filepath.Join(IndexGatewayStateDir(m.OramaDir), name)}
		todo, awaitingRoot, err := m.checkSigningKey(&k, copied[name])
		switch {
		case err != nil:
			conflicts = append(conflicts, err)
		case awaitingRoot:
			remaining++
			m.Logf("%s was copied to %s earlier; the upgrade removes it from secrets/", k.from, k.to)
		case todo:
			pending = append(pending, k)
		}
	}
	if err := errors.Join(conflicts...); err != nil {
		return keyPlan{}, err
	}
	return keyPlan{pending: pending, clearMarker: remaining == 0 && len(copied) > 0}, nil
}

// checkSigningKey reports whether k is still to be taken out of secrets/, or
// was copied earlier and waits for root to delete it (recorded is its digest in
// the marker, "" when none).
func (m Migrator) checkSigningKey(k *keyCopy, recorded string) (todo, awaitingRoot bool, err error) {
	data, _, err := readNoFollow(k.from)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	toExists, err := exists(k.to)
	if err != nil {
		return false, false, err
	}
	if toExists {
		// A copy this process made, still waiting for root to delete the
		// original. The gateway may since have replaced the copy (it swaps the
		// cluster-derived EdDSA key for its own), so the original is matched
		// against what was copied, not against the copy.
		if recorded != "" && recorded == KeyDigest(data) {
			return false, true, nil
		}
		return false, false, bothLayoutsError(k.from, k.to)
	}
	writable, err := dirWritable(filepath.Dir(k.from))
	if err != nil {
		return false, false, err
	}
	k.copyOnly = !writable
	return true, false, nil
}

// dirWritable reports whether this process may create and remove entries in
// dir. A read-only mount (ReadOnlyPaths=) and a directory owned by someone
// else are both "no"; anything else access(2) reports is an error.
func dirWritable(dir string) (bool, error) {
	err := syscall.Access(dir, accessWrite|accessSearch)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EROFS), errors.Is(err, syscall.EPERM):
		return false, nil
	default:
		return false, fmt.Errorf("check whether %s is writable: %w", dir, err)
	}
}

// namespaceDirMode is the mode of data/namespaces and data/namespaces/index
// when they are created here, as the spawner creates them.
const namespaceDirMode = 0o755

// applySigningKeys takes each pending key out of secrets/, or clears a marker
// that has nothing left to record.
func (m Migrator) applySigningKeys(plan keyPlan) error {
	if plan.clearMarker {
		markerPath := CopiedKeysMarker(m.OramaDir)
		if err := os.Remove(markerPath); err != nil {
			return fmt.Errorf("remove %s now that the keys it records are gone from secrets/: %w", markerPath, err)
		}
	}
	if len(plan.pending) == 0 {
		return nil
	}
	stateDir := IndexGatewayStateDir(m.OramaDir)
	if err := os.MkdirAll(filepath.Dir(stateDir), namespaceDirMode); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(stateDir), err)
	}
	if err := os.MkdirAll(stateDir, constants.GatewayStateDirMode); err != nil {
		return fmt.Errorf("create %s: %w", stateDir, err)
	}
	for _, k := range plan.pending {
		if !k.copyOnly {
			if err := os.Rename(k.from, k.to); err != nil {
				return fmt.Errorf("move %s to %s: %w", k.from, k.to, err)
			}
			m.Logf("moved %s to %s", k.from, k.to)
			continue
		}
		if err := m.copySigningKey(k); err != nil {
			return err
		}
		m.Logf("copied %s to %s; secrets/ is not writable by orama-node, so the upgrade removes the original", k.from, k.to)
	}
	return nil
}

// copySigningKey records the copy and then makes it. The marker comes first so
// that a run interrupted between the two is simply repeated: the key is still
// missing from the state directory, so it is copied again. Root deletes an
// original only when its digest is recorded AND the copy exists.
func (m Migrator) copySigningKey(k keyCopy) error {
	data, mode, err := readNoFollow(k.from)
	if err != nil {
		return err
	}
	markerPath := CopiedKeysMarker(m.OramaDir)
	copied, err := ReadCopiedKeys(markerPath)
	if err != nil {
		return err
	}
	copied[k.name] = KeyDigest(data)
	encoded, err := json.Marshal(copied)
	if err != nil {
		return fmt.Errorf("encode %s: %w", markerPath, err)
	}
	if err := writeFileAtomic(markerPath, encoded, copiedKeysMarkerMode); err != nil {
		return fmt.Errorf("record the copy of %s: %w", k.from, err)
	}
	return writeFileAtomic(k.to, data, mode)
}
