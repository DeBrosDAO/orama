package releaseverify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic replaces path with data and perm: written and synced under
// a temporary name beside it, then renamed, so a reader sees the old file or
// the new one and a crash leaves one of them.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr, os.Chmod(name, perm)); err != nil {
		return errors.Join(fmt.Errorf("write %s: %w", name, err), os.Remove(name))
	}
	if err := os.Rename(name, path); err != nil {
		return errors.Join(fmt.Errorf("replace %s: %w", path, err), os.Remove(name))
	}
	return nil
}
