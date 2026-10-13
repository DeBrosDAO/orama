package provision

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// stateTempPattern names the temp file a state is written to before it is
// put in place.
const stateTempPattern = ".state-*.json"

// writeNewFile creates path, which must not exist and is never followed as a
// symlink, with mode 0600 and data (as agent.writeSecret does).
func writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, secretMode)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", path, err)
	}
	return writeAndClose(f, data)
}

// saveState writes st to path through a new 0600 temp file in the same
// directory. The first save links it into place, failing if path exists (so
// another run's state is never overwritten); later saves rename over the
// file this run wrote. A symlink at path is replaced, never followed.
func saveState(st *fleet.State, path string, first bool) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode fleet state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), stateTempPattern)
	if err != nil {
		return fmt.Errorf("failed to create a temp file for fleet state %s: %w", path, err)
	}
	name := tmp.Name()
	if err := writeAndClose(tmp, raw); err != nil {
		return errors.Join(err, os.Remove(name))
	}
	if !first {
		if err := os.Rename(name, path); err != nil {
			return errors.Join(fmt.Errorf("failed to replace fleet state %s: %w", path, err), os.Remove(name))
		}
		return nil
	}
	linkErr := os.Link(name, path)
	if linkErr != nil {
		linkErr = fmt.Errorf("failed to put fleet state in place at %s: %w", path, linkErr)
	}
	if err := os.Remove(name); err != nil {
		return errors.Join(linkErr, fmt.Errorf("failed to remove the temp state %s: %w", name, err))
	}
	return linkErr
}

func writeAndClose(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("failed to write %s: %w", f.Name(), err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close %s: %w", f.Name(), err)
	}
	return nil
}
