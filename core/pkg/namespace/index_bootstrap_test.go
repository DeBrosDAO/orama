package namespace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

const (
	testPeerA = "10.0.0.1:10101"
	testPeerB = "10.0.0.17:10101"
)

// recordMembership writes a membership record into the supervisor's data dir.
func recordMembership(t *testing.T, s *IndexSupervisor, members ...string) string {
	t.Helper()
	path := s.ClusterMembershipPath()
	if err := rqlite.WriteClusterMembership(path, rqlite.ClusterMembership{
		FirstSeen: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Members:   members,
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

// A fresh genesis install — no raft state, no join address, no record — is the
// one node allowed to bootstrap a new cluster.
func TestEnsureRQLite_freshGenesisBootstraps(t *testing.T) {
	f := &fakeIndexRQLite{}
	if err := f.supervisor(t.TempDir()).EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "", ""); err != nil {
		t.Fatalf("a fresh genesis install was refused: %v", err)
	}
	if f.spawned == nil || len(f.spawned.JoinAddresses) != 0 {
		t.Fatalf("a fresh genesis install must start without -join, got %+v", f.spawned)
	}
}

// The bug: a genesis node that lost its rqlite data has no state and no join
// address, and bootstrapped a second, empty cluster. With a record of the
// cluster it was in, it joins the members it recorded instead — never itself.
func TestEnsureRQLite_genesisWithLostDataJoinsRecordedMembers(t *testing.T) {
	f := &fakeIndexRQLite{}
	s := f.supervisor(t.TempDir())
	recordMembership(t, s, testPeerA, testRaftAdv, testPeerB)

	if err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "", ""); err != nil {
		t.Fatalf("a member with lost data and live peers was refused: %v", err)
	}
	if want := []string{testPeerA, testPeerB}; !reflect.DeepEqual(f.spawned.JoinAddresses, want) {
		t.Errorf("JoinAddresses = %v, want the recorded members without this node %v", f.spawned.JoinAddresses, want)
	}
}

// A member with lost data and nobody recorded to join must not start at all:
// the error names the record and recover-raft, and nothing is spawned.
func TestEnsureRQLite_genesisWithLostDataAndNoPeersRefuses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		members []string
	}{
		{"record written from raft state alone", nil},
		{"record naming only this node", []string{testRaftAdv}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeIndexRQLite{}
			s := f.supervisor(t.TempDir())
			path := recordMembership(t, s, tc.members...)

			err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "", "")
			if err == nil {
				t.Fatal("a node that lost its data bootstrapped a new cluster")
			}
			if f.spawned != nil {
				t.Error("rqlited was started despite the refusal")
			}
			for _, want := range []string{"refusing to bootstrap", "recover-raft", path} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not mention %q: %v", want, err)
				}
			}
		})
	}
}

// A joiner joins its configured address first, then the members it recorded.
func TestEnsureRQLite_joinerWithRecordJoinsAddressThenMembers(t *testing.T) {
	f := &fakeIndexRQLite{}
	s := f.supervisor(t.TempDir())
	recordMembership(t, s, testJoin, testPeerB, testRaftAdv)

	if err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, testJoin, ""); err != nil {
		t.Fatal(err)
	}
	if want := []string{testJoin, testPeerB}; !reflect.DeepEqual(f.spawned.JoinAddresses, want) {
		t.Errorf("JoinAddresses = %v, want %v", f.spawned.JoinAddresses, want)
	}
}

// A node holding raft state is recorded as a member the first time it is
// seen, so the evidence exists before the data can be lost. An existing
// record is left alone.
func TestEnsureRQLite_memberWithStateIsRecorded(t *testing.T) {
	f := &fakeIndexRQLite{hasState: true}
	s := f.supervisor(t.TempDir())
	if err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "", ""); err != nil {
		t.Fatal(err)
	}
	rec, err := rqlite.ReadClusterMembership(s.ClusterMembershipPath())
	if err != nil || rec == nil {
		t.Fatalf("a node with raft state was not recorded as a member: rec=%v err=%v", rec, err)
	}

	path := recordMembership(t, s, testPeerA)
	if err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "", ""); err != nil {
		t.Fatal(err)
	}
	rec, err = rqlite.ReadClusterMembership(path)
	if err != nil || rec == nil || !reflect.DeepEqual(rec.Members, []string{testPeerA}) {
		t.Errorf("an existing record was overwritten: %+v, %v", rec, err)
	}
}

// A recovery peers.json is the operator reforming the cluster from this node:
// rqlited must consume it, not be told to join, and not be refused.
func TestEnsureRQLite_pendingRecoveryNeitherJoinsNorRefuses(t *testing.T) {
	f := &fakeIndexRQLite{}
	s := f.supervisor(t.TempDir())
	recordMembership(t, s, testPeerA, testRaftAdv)
	raftDir := filepath.Join(s.CoreRQLiteDir(), "raft")
	if err := os.MkdirAll(raftDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(raftDir, "peers.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, testJoin, ""); err != nil {
		t.Fatalf("a pending recovery was refused: %v", err)
	}
	if len(f.spawned.JoinAddresses) != 0 {
		t.Errorf("a recovering node was given -join %v", f.spawned.JoinAddresses)
	}
}

// A corrupt record is still evidence of membership: the node refuses rather
// than treating it as absent and bootstrapping.
func TestEnsureRQLite_corruptRecordRefuses(t *testing.T) {
	f := &fakeIndexRQLite{}
	s := f.supervisor(t.TempDir())
	path := s.ClusterMembershipPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "", ""); err == nil {
		t.Fatal("a corrupt membership record was treated as no record")
	}
	if f.spawned != nil {
		t.Error("rqlited was started despite a corrupt record")
	}
}

// The index gateway config moved to the peer-id file name; the one left under
// the old node.id name (which embeds the secrets key) is removed, and the
// current one is kept.
func TestRemoveStaleIndexConfigs_keepsOnlyThisNodes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"gateway-stagenet.yaml", "gateway-12D3KooWX.yaml", "olric-stagenet.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeStaleIndexConfigs(dir, "gateway", "12D3KooWX"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if want := []string{"gateway-12D3KooWX.yaml", "olric-stagenet.yaml"}; !reflect.DeepEqual(left, want) {
		t.Errorf("left %v, want %v", left, want)
	}
}

// A missing config directory has nothing stale in it.
func TestRemoveStaleIndexConfigs_missingDirIsFine(t *testing.T) {
	if err := removeStaleIndexConfigs(filepath.Join(t.TempDir(), "absent"), "gateway", "id"); err != nil {
		t.Errorf("missing dir: %v", err)
	}
}

// A recorded member that is not ip:port is refused, not passed to -join.
func TestEnsureRQLite_malformedRecordedMemberRefuses(t *testing.T) {
	f := &fakeIndexRQLite{}
	s := f.supervisor(t.TempDir())
	recordMembership(t, s, testPeerA, "10.0.0.9:1 -http-addr 0.0.0.0:1")
	err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "", "")
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("a malformed recorded member was accepted: %v", err)
	}
	if f.spawned != nil {
		t.Error("rqlited was started with a malformed member")
	}
}

// The old olric config goes too, and only olric configs are touched.
func TestRemoveStaleIndexConfigs_olric(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"olric-stagenet.yaml", "olric-12D3KooWX.yaml", "gateway-stagenet.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeStaleIndexConfigs(dir, "olric", "12D3KooWX"); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"olric-stagenet.yaml": false, "olric-12D3KooWX.yaml": true, "gateway-stagenet.yaml": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s present=%v, want %v", name, err == nil, want)
		}
	}
}

// A torn record on a node that still holds raft state must not keep a healthy
// member down: its state proves membership, and the record is replaced.
func TestEnsureRQLite_corruptRecordOnAMemberIsReplaced(t *testing.T) {
	f := &fakeIndexRQLite{hasState: true}
	s := f.supervisor(t.TempDir())
	path := s.ClusterMembershipPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"first_seen":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "", ""); err != nil {
		t.Fatalf("a member with state was kept down by a torn record: %v", err)
	}
	if f.spawned == nil {
		t.Fatal("rqlited was not started")
	}
	if rec, err := rqlite.ReadClusterMembership(path); err != nil || rec == nil {
		t.Fatalf("the record was not replaced: %v, %v", rec, err)
	}
}

// The upgrade that moves the index raft port from 7001 to 10101. The node
// keeps its id (the recorded one) and its configuration still holds it at
// :7001, so it joins the other members again: the leader then re-registers it
// at :10101. Without -join it would never be reachable again.
func TestEnsureRQLite_addressChangeRejoinsTheOtherMembers(t *testing.T) {
	const oldSelf = "10.0.0.2:7001"
	f := &fakeIndexRQLite{hasState: true, identity: rqlite.RaftIdentity{NodeID: oldSelf, PreviousAddr: oldSelf}}
	s := f.supervisor(t.TempDir())
	recordMembership(t, s, "10.0.0.1:7001", oldSelf, "10.0.0.17:10101")

	if err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "10.0.0.1:10101", ""); err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.1:7001", "10.0.0.17:10101", "10.0.0.1:10101"}
	if !reflect.DeepEqual(f.spawned.JoinAddresses, want) {
		t.Errorf("JoinAddresses = %v, want %v", f.spawned.JoinAddresses, want)
	}
	if !strings.Contains(f.spawned.ExtraArgs, "-node-id "+oldSelf) {
		t.Errorf("the node did not keep its raft id: %q", f.spawned.ExtraArgs)
	}
}

// An unchanged address is an ordinary restart: no -join.
func TestEnsureRQLite_unchangedAddressRestartsWithoutJoin(t *testing.T) {
	f := &fakeIndexRQLite{hasState: true, identity: rqlite.RaftIdentity{NodeID: "12D3Koo", PreviousAddr: testRaftAdv}}
	s := f.supervisor(t.TempDir())
	recordMembership(t, s, testPeerA, testRaftAdv)
	if err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, testPeerA, ""); err != nil {
		t.Fatal(err)
	}
	if len(f.spawned.JoinAddresses) != 0 {
		t.Errorf("JoinAddresses = %v, want none", f.spawned.JoinAddresses)
	}
}

// With nobody to join, a node whose address changed cannot be re-registered;
// it refuses and names the recovery for a cluster of one.
func TestEnsureRQLite_addressChangeWithNoOneToJoinRefuses(t *testing.T) {
	const oldSelf = "10.0.0.2:7001"
	f := &fakeIndexRQLite{hasState: true, identity: rqlite.RaftIdentity{NodeID: oldSelf, PreviousAddr: oldSelf}}
	s := f.supervisor(t.TempDir())
	recordMembership(t, s, oldSelf)
	err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "", "")
	if err == nil || !strings.Contains(err.Error(), "recover-raft") {
		t.Fatalf("err = %v, want a refusal naming recover-raft", err)
	}
	if f.spawned != nil {
		t.Error("rqlited was started with no way to be re-registered")
	}
}

// A pending recovery peers.json wins over an address change: the operator is
// reforming the cluster from this node, and -join would contradict it.
func TestEnsureRQLite_recoveryWinsOverAnAddressChange(t *testing.T) {
	f := &fakeIndexRQLite{hasState: true, identity: rqlite.RaftIdentity{NodeID: "x", PreviousAddr: "10.0.0.2:7001"}}
	s := f.supervisor(t.TempDir())
	raftDir := filepath.Join(s.CoreRQLiteDir(), "raft")
	if err := os.MkdirAll(raftDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(raftDir, "peers.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, testPeerA, ""); err != nil {
		t.Fatal(err)
	}
	if len(f.spawned.JoinAddresses) != 0 {
		t.Errorf("JoinAddresses = %v, want none during recovery", f.spawned.JoinAddresses)
	}
}

// A non-voter whose address changed rejoins as a non-voter; one whose address
// did not change is not given the flag at all.
func TestEnsureRQLite_nonVoterRejoinsAsANonVoter(t *testing.T) {
	const oldSelf = "10.0.0.2:7001"
	f := &fakeIndexRQLite{hasState: true, identity: rqlite.RaftIdentity{NodeID: oldSelf, PreviousAddr: oldSelf, NonVoter: true}}
	s := f.supervisor(t.TempDir())
	recordMembership(t, s, "10.0.0.1:7001", oldSelf)
	if err := s.EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.spawned.ExtraArgs, raftNonVoterFlag) {
		t.Errorf("a non-voter rejoined without %s: %q", raftNonVoterFlag, f.spawned.ExtraArgs)
	}

	g := &fakeIndexRQLite{hasState: true, identity: rqlite.RaftIdentity{NodeID: "x", PreviousAddr: testRaftAdv, NonVoter: true}}
	if err := g.supervisor(t.TempDir()).EnsureRQLite(context.Background(), "n", "12D3Koo", testHTTPAdv, testRaftAdv, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g.spawned.ExtraArgs, raftNonVoterFlag) {
		t.Errorf("an ordinary restart was given %s", raftNonVoterFlag)
	}
}
