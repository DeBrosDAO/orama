//go:build !unix

package updateagent

import (
	"errors"
	"os"
)

// checkWorkDir needs unix file ownership; nodes run Linux.
func checkWorkDir(string, uint32) error {
	return errors.New("the auto-update agent needs a unix system")
}

// openNoFollow needs O_NOFOLLOW; nodes run Linux.
func openNoFollow(path string) (*os.File, error) { return os.Open(path) }
