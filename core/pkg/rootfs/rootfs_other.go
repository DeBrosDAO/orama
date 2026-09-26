//go:build !unix

package rootfs

import (
	"errors"
	"io/fs"
)

// errUnsupported: resolving a path without following symlinks needs openat
// with O_NOFOLLOW, which only unix systems provide. Nodes run Linux.
var errUnsupported = errors.New("rootfs needs a unix system")

func (r Root) ReadFile(path string, limit int64) ([]byte, error) { return nil, errUnsupported }

func (r Root) WriteFile(path string, data []byte, perm fs.FileMode) error { return errUnsupported }

func (r Root) MkdirAll(path string, perm fs.FileMode) error { return errUnsupported }

func (r Root) Chmod(path string, mode fs.FileMode) error { return errUnsupported }

func (r Root) Chown(path string, uid, gid int) error { return errUnsupported }

func (r Root) Remove(path string) error { return errUnsupported }

func (r Root) DirOwner(path string) (uid, gid uint32, err error) { return 0, 0, errUnsupported }
