//go:build !unix

package auth

import "errors"

// lockCredentialFile needs flock; the CLI runs on unix systems.
func lockCredentialFile() (func(), error) {
	return nil, errors.New("renewing a stored session needs a unix system (flock on the credential file)")
}
