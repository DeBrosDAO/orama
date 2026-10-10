//go:build e2e_fleet

package namespacebackup

import "time"

const (
	// keyCacheBudget covers a gateway's credential cache and the revocation
	// reload (docs/whitepaper/technical-reference/vol1/13-identity.md#revocation) after a restore replaces the key rows.
	keyCacheBudget = 30 * time.Second
	pollEvery      = 2 * time.Second
)
