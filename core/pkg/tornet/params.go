// Package tornet holds the Orama Tor network parameters.
// It does not generate authority keys, start a directory authority,
// or open an exit.
package tornet

import (
	"errors"
	"fmt"
)

const (
	// ExitPolicy is the only policy this package allows.
	ExitPolicy = "reject *:*"
	// MinAuthorities is the smallest directory-authority set.
	MinAuthorities = 3
	// CertMonths is the signing-certificate lifetime.
	CertMonths = 12
)

// ErrExitRefused means a public exit was requested.
var ErrExitRefused = errors.New("public Tor exit is not launched")

// Params is the network description shipped to relays and clients.
type Params struct {
	Name        string
	Authorities int
	ExitPolicy  string
	CertMonths  int
}

// Validate checks a stagenet parameter set. Fewer than three authorities,
// a different exit policy, or a different certificate lifetime is refused.
func (p Params) Validate() error {
	if p.Name == "" {
		return errors.New("tor network name is empty")
	}
	if p.Authorities < MinAuthorities {
		return fmt.Errorf("tor network needs at least %d authorities", MinAuthorities)
	}
	if p.ExitPolicy != ExitPolicy {
		return fmt.Errorf("exit policy must be %s", ExitPolicy)
	}
	if p.CertMonths != CertMonths {
		return fmt.Errorf("signing certificates last %d months", CertMonths)
	}
	return nil
}

// StartExit refuses to launch an exit.
func StartExit() error {
	return ErrExitRefused
}
