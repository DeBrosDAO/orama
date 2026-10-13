//go:build !unix

package archivetrust

import (
	"errors"
	"io/fs"
	"os"
)

// errNeedsUnix: ownership checks, no-follow opens and flock are unix
// facilities; nodes run Linux.
var errNeedsUnix = errors.New("archive verification on a node needs a unix system")

func ownerOf(fs.FileInfo) (fileOwnerIDs, error) { return fileOwnerIDs{}, errNeedsUnix }

func lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }

func openNoFollow(string) (*os.File, fs.FileInfo, error) { return nil, nil, errNeedsUnix }

// LockArchiveDir needs flock.
func LockArchiveDir(string) (func() error, error) { return nil, errNeedsUnix }
