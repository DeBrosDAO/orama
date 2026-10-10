package remotessh

import (
	"errors"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// uploadBytesFilePerm: the temporary copy of what is uploaded is the uploading
// user's alone.
const uploadBytesFilePerm = 0o600

// UploadBytes copies data to a file on the remote host via SCP, through a
// temporary local file that is removed afterwards.
// Requires node.SSHKey to be set (via PrepareNodeKeys).
func UploadBytes(node inspector.Node, data []byte, remotePath string) (err error) {
	f, err := os.CreateTemp("", "orama-upload-*")
	if err != nil {
		return fmt.Errorf("create a temporary file to upload: %w", err)
	}
	defer func() { err = errors.Join(err, os.Remove(f.Name())) }()
	if err := f.Chmod(uploadBytesFilePerm); err != nil {
		return errors.Join(fmt.Errorf("restrict %s: %w", f.Name(), err), f.Close())
	}
	if _, err := f.Write(data); err != nil {
		return errors.Join(fmt.Errorf("write %s: %w", f.Name(), err), f.Close())
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", f.Name(), err)
	}
	return UploadFile(node, f.Name(), remotePath)
}
