//go:build unix

package autoupdate

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// chownLike gives path the owner and group of the file info describes, so
// a swapped binary is runnable by the same service accounts as the one it
// replaces.
func chownLike(path string, info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read the owner of %s (%T)", info.Name(), info.Sys())
	}
	if err := os.Lchown(path, int(st.Uid), int(st.Gid)); err != nil {
		return fmt.Errorf("chown %s to %d:%d: %w", path, st.Uid, st.Gid, err)
	}
	return nil
}
