//go:build !unix

package push

import (
	"errors"
	"io/fs"
)

// ownerUID needs unix file ownership; nodes run Linux.
func ownerUID(fs.FileInfo) (uint32, error) {
	return 0, errors.New("staging an archive needs a unix system")
}
