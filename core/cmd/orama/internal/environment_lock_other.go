//go:build !unix

package cli

import "errors"

// lockEnvironmentConfig needs flock; the CLI runs on unix systems.
func lockEnvironmentConfig(string) (func() error, error) {
	return nil, errors.New("changing environments.json needs a unix system (flock on its lock file)")
}
