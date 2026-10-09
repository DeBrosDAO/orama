package install

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// recordGlobalRole writes role global into preferences.yaml on a machine that
// has no cluster node, so an orama-node started there boots the global graph
// (data-dir only) and never WireGuard, RQLite, Olric or the gateway. A machine
// that already has preferences keeps them: a cluster node installing the
// global services beside it stays a cluster node, and role both is only ever
// written by a --colocated install.
func recordGlobalRole(n NetnsHost) error {
	if n.OramaDir == "" {
		return nil
	}
	path := n.OramaDir + "/" + preferencesFile
	_, err := OramaRoot(n.OramaDir).ReadFile(path, rootfs.SmallFileLimit)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := SavePreferences(n.OramaDir, &NodePreferences{Role: roleGlobal}); err != nil {
		return fmt.Errorf("record the global role: %w", err)
	}
	return nil
}
