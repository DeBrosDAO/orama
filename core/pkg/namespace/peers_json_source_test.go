package namespace

import "testing"

// peers.json is rqlite's force-recovery mechanism: whatever it says becomes the
// raft configuration at startup. The disk restore used to build it from
// cluster-state.json, whose AllNodes list is refreshed by a best-effort push -
// so the node most likely to hold a stale copy is exactly the one that was down
// while the cluster changed, and on its next boot it reinstated a removed member
// as a voter.
//
// The rule is a preference order: assert verified membership, else assert
// nothing, else assert the least that can still produce a leader.
func TestChoosePeersJSONSource(t *testing.T) {
	cases := []struct {
		name             string
		dbOK             bool
		anyPeerReachable bool
		want             peersJSONSource
		why              string
	}{
		{
			name: "membership readable", dbOK: true, anyPeerReachable: false,
			want: peersFromDB,
			why:  "a readable index DB is authoritative and outranks anything on local disk",
		},
		{
			name: "membership readable and peers reachable", dbOK: true, anyPeerReachable: true,
			want: peersFromDB,
			why:  "reachability never overrides verified membership",
		},
		{
			name: "membership unreadable but a peer answers", dbOK: false, anyPeerReachable: true,
			want: peersSkip,
			why:  "rqlited can rejoin using its own raft state; writing a guess would overwrite the real configuration",
		},
		{
			name: "membership unreadable and nothing answers", dbOK: false, anyPeerReachable: false,
			want: peersSelfOnly,
			why:  "a single-node configuration yields a leader; asserting an unverifiable membership does not",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := choosePeersJSONSource(tc.dbOK, tc.anyPeerReachable)
			if got != tc.want {
				t.Errorf("choosePeersJSONSource(dbOK=%v, reachable=%v) = %v, want %v\n  %s",
					tc.dbOK, tc.anyPeerReachable, got, tc.want, tc.why)
			}
		})
	}
}

// The stale local file must never be a source on its own. Whatever the inputs,
// the decision is one of the three verified/abstain/minimal outcomes.
func TestPeersJSONSourceNeverTrustsLocalStateAlone(t *testing.T) {
	for _, dbOK := range []bool{true, false} {
		for _, reachable := range []bool{true, false} {
			switch choosePeersJSONSource(dbOK, reachable) {
			case peersFromDB, peersSkip, peersSelfOnly:
			default:
				t.Fatalf("unexpected source for dbOK=%v reachable=%v", dbOK, reachable)
			}
		}
	}
}

func TestPeersJSONSourceStrings(t *testing.T) {
	for src, want := range map[peersJSONSource]string{
		peersFromDB:   "live-membership",
		peersSkip:     "skipped-peer-reachable",
		peersSelfOnly: "self-only",
	} {
		if got := src.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}
