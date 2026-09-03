package systemd

import "testing"

// The pre-factory host daemons must be recognisable by name.
//
// Their unit files stay on disk for rollback, so any code that decides what to
// start by looking for a unit file will find them and start them — and they
// then race orama-namespace-*@index for 10102, 10107, :53 and :443 until
// IndexSupervisor stops them again on its next start.
func TestIsLeftoverHostUnit(t *testing.T) {
	for _, name := range LeftoverHostUnits {
		if !IsLeftoverHostUnit(name) {
			t.Errorf("%s is in LeftoverHostUnits but not recognised", name)
		}
	}
	for _, name := range []string{LeftoverWireGuardUnit, LeftoverNameserverUnit} {
		if !IsLeftoverHostUnit(name) {
			t.Errorf("%s is a leftover unit but not recognised", name)
		}
	}

	// The supervisor and the namespace instances must never be classified as
	// leftovers — that would stop the node from being restarted at all.
	for _, name := range []string{
		"orama-node.service",
		"orama-namespace-rqlite@index.service",
		"orama-namespace-gateway@index.service",
		"orama-namespace-wireguard@index.service",
		"orama-anyone-relay.service",
	} {
		if IsLeftoverHostUnit(name) {
			t.Errorf("%s must not be treated as a leftover unit", name)
		}
	}
}

// The specific units that used to be restarted on every upgrade.
func TestLeftoverHostUnits_covers_the_racing_daemons(t *testing.T) {
	for _, name := range []string{
		"orama-olric.service",
		"orama-ipfs.service",
		"orama-ipfs-cluster.service",
		"orama-vault.service",
		"caddy.service",
	} {
		if !IsLeftoverHostUnit(name) {
			t.Errorf("%s races orama-namespace-*@index and must be recognised as a leftover", name)
		}
	}
}
