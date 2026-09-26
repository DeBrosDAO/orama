//go:build unix

package push

import (
	"fmt"
	"io/fs"
	"syscall"
)

// ownerUID is the uid that owns the file info describes.
func ownerUID(info fs.FileInfo) (uint32, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("cannot read the owner of %s (%T)", info.Name(), info.Sys())
	}
	return st.Uid, nil
}
