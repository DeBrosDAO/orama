//go:build !unix

package autoupdate

import (
	"errors"
	"io/fs"
)

// chownLike needs unix file ownership; nodes run Linux.
func chownLike(string, fs.FileInfo) error {
	return errors.New("swapping a binary needs a unix system")
}
