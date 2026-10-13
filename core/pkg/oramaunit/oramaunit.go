// Package oramaunit says which systemd units Orama owns. It has no
// dependencies, so the node report, the inspector and the e2e harnesses share
// one rule.
package oramaunit

import "strings"

// legacyOramaUnits are units Orama owns outside the orama-* namespace: the
// host units an older install ran before the per-namespace templates.
var legacyOramaUnits = map[string]bool{
	"wg-quick@wg0.service": true,
	"caddy.service":        true,
	"coredns.service":      true,
}

// Is reports whether Orama owns the systemd unit name. A failed unit
// it owns is a cluster problem; one the host image ships (a cloud-init that
// failed at first boot) is the operator's warning, and no cluster check may
// wait on it.
func Is(name string) bool {
	return strings.HasPrefix(name, "orama-") || legacyOramaUnits[name]
}

// Filter is the subset of units that Orama owns, in order.
func Filter(units []string) []string {
	var out []string
	for _, u := range units {
		if Is(u) {
			out = append(out, u)
		}
	}
	return out
}
