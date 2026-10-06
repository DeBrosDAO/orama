package namespace

import "testing"

// peers.json is rqlite's force-recovery mechanism: whatever it says becomes the
// raft configuration at startup. Only verified membership may be asserted.
func TestChoosePeersJSONSource_verifiedMembershipIsAsserted(t *testing.T) {
	if got := choosePeersJSONSource(true); got != peersFromDB {
		t.Errorf("readable membership: got %v, want %v", got, peersFromDB)
	}
}

// A full cold start — every node rebooting at once — is exactly "membership
// unreadable and no peer answers yet". Writing a single-node configuration then
// made every node a cluster of one and split every namespace; the node must
// keep its own configuration and wait for its peers.
func TestChoosePeersJSONSource_unreadableMembershipAssertsNothing(t *testing.T) {
	if got := choosePeersJSONSource(false); got != peersSkip {
		t.Errorf("unreadable membership: got %v, want %v (never a guess, never self-only)", got, peersSkip)
	}
}

func TestPeersJSONSourceStrings(t *testing.T) {
	for src, want := range map[peersJSONSource]string{
		peersFromDB: "live-membership",
		peersSkip:   "skipped-membership-unreadable",
	} {
		if got := src.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}
