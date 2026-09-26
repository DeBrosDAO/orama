// Package gatewaykeys stores the index gateway's signing keys where a tenant
// gateway cannot read them.
//
// Every gateway on a node runs as the orama user. A key file in the index
// gateway's state directory is mode 0600 and owned by that user, so a tenant
// gateway — the same uid — reads it. These files are root:root 0400. systemd
// loads them into the index unit only, through LoadCredential; no other unit
// has the credential, and the orama user cannot open the directory.
package gatewaykeys

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// Dir is the root-owned tree the keys live in: <Dir>/<namespace>/<file>.
const Dir = "/var/lib/orama-gateway-keys"

// MaxPEM is the most a signing key PEM may be. An RSA-2048 key is under 2KB.
const MaxPEM = 16 << 10

// dirMode is the tree's mode. The orama user is not root and not in a group
// that can traverse it.
const dirMode = 0o700

// fileMode is a key file's mode.
const fileMode = 0o400

// Names are the only files this tree holds: the index gateway's two signing keys.
func Names() []string {
	return []string{constants.GatewayRSAKeyFileName, constants.GatewayEdDSAKeyFileName}
}

// ValidName reports whether name is one of Names.
func ValidName(name string) bool {
	for _, n := range Names() {
		if name == n {
			return true
		}
	}
	return false
}

// Write stores pem at dir/namespace/name, replacing the file, as root:root 0400.
// namespace is the index gateway only; the caller has already checked that.
func Write(dir, namespace, name string, pem []byte) error {
	if namespace != constants.IndexNamespace {
		return fmt.Errorf("gateway key namespace %q is not %s", namespace, constants.IndexNamespace)
	}
	if !ValidName(name) {
		return fmt.Errorf("gateway key %q is not a signing key", name)
	}
	if len(pem) == 0 || len(pem) > MaxPEM {
		return fmt.Errorf("gateway key %s is %d bytes", name, len(pem))
	}
	destDir := filepath.Join(dir, namespace)
	if err := os.MkdirAll(destDir, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", destDir, err)
	}
	if err := os.Chmod(destDir, dirMode); err != nil {
		return fmt.Errorf("restrict %s: %w", destDir, err)
	}
	tmp, err := os.CreateTemp(destDir, name+".tmp-*")
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return fmt.Errorf("restrict %s: %w", name, err)
	}
	if _, err := tmp.Write(pem); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	dest := filepath.Join(destDir, name)
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("replace %s: %w", dest, err)
	}
	cleanup = false
	if err := os.Chmod(dest, fileMode); err != nil {
		return fmt.Errorf("restrict %s: %w", dest, err)
	}
	return nil
}
