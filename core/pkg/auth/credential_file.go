package auth

import (
	"fmt"
	"os"
	"path/filepath"
)

// credentialFilePerm is the mode of the credential file: it holds tokens.
const credentialFilePerm = 0o600

// writeCredentialFile replaces the credential file at path with data in one
// rename. os.WriteFile truncated and rewrote it in place, so two commands
// saving at once interleaved and left a file no command could parse ("invalid
// character '}' after top-level value"), and a file first created with a wider
// mode kept it. The data goes to a 0600 temporary file beside it, is synced,
// and is renamed over the old file, so a reader sees the old file or the new
// one, never part of either.
func writeCredentialFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create a temporary credential file beside %s: %w", path, err)
	}
	name := tmp.Name()
	if err := writeAndSync(tmp, data); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// writeAndSync writes data to f with the credential mode, syncs and closes it.
func writeAndSync(f *os.File, data []byte) error {
	if err := f.Chmod(credentialFilePerm); err != nil {
		f.Close()
		return fmt.Errorf("restrict %s to its owner: %w", f.Name(), err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", f.Name(), err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", f.Name(), err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", f.Name(), err)
	}
	return nil
}
