package types

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// MinNameLen and MaxNameLen bound a node identification name: one DNS label.
	MinNameLen = 3
	MaxNameLen = 32

	// punycodePrefix starts an internationalised label ("xn--..."). A name is plain ASCII, so it
	// never carries one.
	punycodePrefix = "xn--"
)

var (
	namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	// numberedReserved matches the reserved words that are numbered families: seed, seed1, seed2,
	// ..., ns1, ns2, ... A seed is a network's published bootstrap host, and a name server is a
	// delegated one; neither may be claimed by an operator.
	numberedReserved = regexp.MustCompile(`^(seed|ns)[0-9]*$`)
)

// ReservedNames are the labels no operator can claim: the names the network itself publishes
// under its domain, and the ones people trust by habit. Numbered seeds and name servers
// (seed, seed1 ... , ns1 ...) are matched by pattern as well.
var ReservedNames = map[string]struct{}{
	"www": {}, "api": {}, "admin": {}, "gateway": {}, "explorer": {}, "status": {}, "releases": {},
	"mail": {}, "root": {}, "dns": {}, "mx": {}, "smtp": {}, "imap": {}, "pop": {}, "ftp": {}, "ssh": {},
	"vpn": {}, "cdn": {}, "docs": {}, "blog": {}, "app": {}, "dev": {}, "test": {}, "staging": {},
	"rpc": {}, "rest": {}, "grpc": {}, "node": {}, "nodes": {}, "validator": {}, "validators": {},
	"relay": {}, "bootstrap": {}, "faucet": {}, "wallet": {}, "auth": {}, "login": {}, "git": {},
	"orama": {}, "network": {}, "support": {}, "security": {}, "abuse": {}, "postmaster": {},
	"hostmaster": {}, "webmaster": {}, "localhost": {}, "monitor": {}, "metrics": {}, "download": {},
	"downloads": {}, "update": {}, "updates": {}, "install": {}, "stagenet": {}, "testnet": {},
	"mainnet": {}, "devnet": {},
}

// ErrInvalidName is returned for a name that is not a valid, unreserved DNS label.
var ErrInvalidName = fmt.Errorf("invalid node name")

// ValidateName checks a node identification name: lowercase DNS label characters (a-z, 0-9 and
// '-'), 3 to 32 of them, no leading or trailing '-', no internationalised prefix, and not reserved.
func ValidateName(name string) error {
	if len(name) < MinNameLen || len(name) > MaxNameLen {
		return fmt.Errorf("name %q must be %d to %d characters: %w", name, MinNameLen, MaxNameLen, ErrInvalidName)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("name %q must use only a-z, 0-9 and '-', without a leading or trailing '-': %w", name, ErrInvalidName)
	}
	if strings.HasPrefix(name, punycodePrefix) {
		return fmt.Errorf("name %q must not start with %q: %w", name, punycodePrefix, ErrInvalidName)
	}
	if IsReservedName(name) {
		return fmt.Errorf("name %q is reserved: %w", name, ErrInvalidName)
	}
	return nil
}

// IsReservedName reports whether name is reserved: a listed word or a numbered seed or name server.
func IsReservedName(name string) bool {
	if _, ok := ReservedNames[name]; ok {
		return true
	}
	return numberedReserved.MatchString(name)
}
