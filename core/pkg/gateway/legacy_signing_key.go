package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// legacyKeyRetirementFileName records, in a gateway's state directory, when
// the cluster-derived signing key stops verifying on that gateway.
//
// The key is accepted for one access-token lifetime after the upgrade that
// replaced it, so that tokens minted before it keep working across it. That
// window used to be measured from every boot — RetiredAt was now + lifetime on
// each start — so every restart re-armed a key every node can derive, and a
// gateway that restarted at least once per token lifetime never retired it.
// The deadline is now written once, on the first boot that arms it, and read
// back on every boot after.
const legacyKeyRetirementFileName = "legacy-signing-key-retired-at"

// legacyKeyRetirementFileMode keeps the record with the keys beside it.
const legacyKeyRetirementFileMode = 0o600

// armLegacyClusterKey adds the cluster-derived key to keys as verify-only until
// its recorded retirement, and not at all once that has passed.
//
// migrated is true only when this boot replaced a key file that was the
// cluster-derived key. A gateway that never held that key — every new one,
// and every one whose own key is already on disk — records the cluster key
// retired instead of arming it. Arming it on every boot re-opened a key
// every node can derive, for one token lifetime, on a gateway that had
// nothing to migrate. A gateway with no cluster secret has no such key.
func armLegacyClusterKey(keys *auth.SigningKeys, stateDir, clusterSecret string, now time.Time, migrated bool) error {
	if clusterSecret == "" {
		return nil
	}
	if !migrated {
		path := filepath.Join(stateDir, legacyKeyRetirementFileName)
		_, statErr := os.Stat(path)
		switch {
		case statErr == nil:
			// A previous boot migrated and wrote the deadline. Honor it.
		case os.IsNotExist(statErr):
			at := now.UTC().Truncate(time.Second)
			if err := writeFileAtomic(path, []byte(at.Format(time.RFC3339)+"\n"), legacyKeyRetirementFileMode); err != nil {
				return fmt.Errorf("record the legacy signing key retired in %s: %w", path, err)
			}
			return nil
		default:
			return fmt.Errorf("read the legacy signing key's retirement record %s: %w", path, statErr)
		}
	}
	retiredAt, err := legacyKeyRetirement(stateDir, now, auth.AccessTokenLifetime)
	if err != nil {
		return err
	}
	if !now.Before(retiredAt) {
		return nil
	}
	legacy, err := LegacyClusterSigningKey(clusterSecret)
	if err != nil {
		return fmt.Errorf("derive the previous cluster signing key to accept tokens issued before this upgrade: %w", err)
	}
	keys.Add(auth.SigningKey{KID: auth.KeyIDFor(legacy), Public: legacy, RetiredAt: retiredAt})
	return nil
}

// legacyKeyRetirement is when the cluster-derived key stops verifying on the
// gateway whose state directory is stateDir.
//
// The first call writes now + lifetime and returns it; every later call
// returns what was written, whatever the clock says. A record that cannot be
// read or parsed is an error rather than a fresh window: re-arming the key is
// the thing this record exists to prevent.
func legacyKeyRetirement(stateDir string, now time.Time, lifetime time.Duration) (time.Time, error) {
	path := filepath.Join(stateDir, legacyKeyRetirementFileName)
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		at, perr := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
		if perr != nil {
			return time.Time{}, fmt.Errorf("the legacy signing key's retirement record %s is not an RFC 3339 time (%v); "+
				"write a time in the past to it to keep the key retired", path, perr)
		}
		return at, nil
	case !os.IsNotExist(err):
		return time.Time{}, fmt.Errorf("read the legacy signing key's retirement record %s: %w", path, err)
	}

	at := now.Add(lifetime).UTC().Truncate(time.Second)
	if err := writeFileAtomic(path, []byte(at.Format(time.RFC3339)+"\n"), legacyKeyRetirementFileMode); err != nil {
		return time.Time{}, fmt.Errorf("record the legacy signing key's retirement in %s: %w", path, err)
	}
	return at, nil
}

// writeFileAtomic writes data to a temporary file beside path and renames it
// over path, so a crash leaves either the old record or the new one.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
