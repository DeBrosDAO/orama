//go:build !unix

package releaseverify

import "errors"

// lockSeen needs flock; nodes run Linux.
func lockSeen(string) (func() error, error) {
	return nil, errors.New("the release rollback record needs a unix system")
}
