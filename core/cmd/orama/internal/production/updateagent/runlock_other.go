//go:build !unix

package updateagent

import "errors"

// errRunning is another agent run on this machine.
var errRunning = errors.New("another auto-update run is in progress on this machine")

// lockRun needs flock; nodes run Linux.
func lockRun(string) (func() error, error) {
	return nil, errors.New("the auto-update agent needs a unix system")
}
