package recover

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

const testPeerID = "12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy"

// /nodes is keyed by raft id; the address is the member's "addr". With peer-id
// raft ids the key is not an address at all, and reading it as one refused
// every current cluster.
func TestParseLeaderRaft_readsTheAddrField(t *testing.T) {
	body := `{
		"` + testPeerID + `": {"addr":"10.0.0.1:10101","leader":true,"voter":true,"reachable":true},
		"12D3KooWOtherOtherOtherOtherOther": {"addr":"10.0.0.2:10101","leader":false,"voter":true}
	}`
	got, err := parseLeaderRaft([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.id != testPeerID || got.addr != "10.0.0.1:10101" {
		t.Errorf("parseLeaderRaft() = %+v", got)
	}
}

// A node predating recorded ids is keyed by its address.
func TestParseLeaderRaft_addressIDs(t *testing.T) {
	got, err := parseLeaderRaft([]byte(`{"10.0.0.6:7001":{"addr":"10.0.0.6:7001","leader":true}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.id != "10.0.0.6:7001" || got.addr != "10.0.0.6:7001" {
		t.Errorf("got %+v", got)
	}
}

func TestParseLeaderRaft_noLeader(t *testing.T) {
	// A cluster with no elected leader (all followers/candidates) must error,
	// not silently return an empty address that would produce a broken peers.json.
	body := `{
		"10.0.0.1:7001": {"addr":"10.0.0.1:7001","leader":false},
		"10.0.0.2:7001": {"addr":"10.0.0.2:7001","leader":false}
	}`
	if _, err := parseLeaderRaft([]byte(body)); err == nil {
		t.Fatal("expected error when no node reports leader==true, got nil")
	}
}

func TestParseLeaderRaft_emptyOrInvalid(t *testing.T) {
	for _, body := range []string{`{}`, `not json`} {
		if _, err := parseLeaderRaft([]byte(body)); err == nil {
			t.Errorf("expected an error for %q", body)
		}
	}
}

func TestParseLeaderRaft_rejectsMalformedValues(t *testing.T) {
	// A compromised or corrupt node could return a leader id or address that is
	// not one. It must be rejected before it ever reaches peers.json.
	cases := map[string]string{
		"shell injection in id": `{"; rm -rf / #":{"addr":"10.0.0.1:10101","leader":true}}`,
		"no address":            `{"` + testPeerID + `":{"leader":true}}`,
		"address not host:port": `{"` + testPeerID + `":{"addr":"garbage","leader":true}}`,
		"non-ip host":           `{"` + testPeerID + `":{"addr":"evil.example.com:7001","leader":true}}`,
		"non-numeric port":      `{"` + testPeerID + `":{"addr":"10.0.0.1:1; rm -rf /","leader":true}}`,
		"id with quote":         `{"12D3Koo\"x":{"addr":"10.0.0.1:10101","leader":true}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := parseLeaderRaft([]byte(body)); err == nil {
				t.Fatalf("expected error for %s, got %+v", name, got)
			}
		})
	}
}

// The recovery peers.json names the leader under the id its rqlited runs with
// and at its address. Writing the id as the address (or the other way round)
// left the leader outside its own configuration.
func TestBuildSingleNodePeersJSON_shape(t *testing.T) {
	out, err := buildSingleNodePeersJSON(leaderRaft{id: testPeerID, addr: "10.0.0.1:10101"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must be a JSON array of exactly one voter entry with id/address/non_voter,
	// matching the rqlite v8 recovery format (see cluster_discovery_membership.go).
	var peers []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &peers); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(peers) != 1 {
		t.Fatalf("expected exactly 1 peer, got %d", len(peers))
	}
	p := peers[0]
	if p["id"] != testPeerID {
		t.Errorf("id = %v, want the peer id", p["id"])
	}
	if p["address"] != "10.0.0.1:10101" {
		t.Errorf("address = %v, want 10.0.0.1:10101", p["address"])
	}
	// The single recovered node MUST be a voter, or it can never elect itself
	// leader and the whole recovery deadlocks.
	if nv, ok := p["non_voter"].(bool); !ok || nv {
		t.Errorf("non_voter = %v, want false (single node must be a voter)", p["non_voter"])
	}
}

func TestBuildSingleNodePeersJSON_refusesMalformed(t *testing.T) {
	for _, lr := range []leaderRaft{{id: testPeerID, addr: testPeerID}, {id: "a b", addr: "10.0.0.1:10101"}, {}} {
		if _, err := buildSingleNodePeersJSON(lr); err == nil {
			t.Errorf("built a peers.json for %+v", lr)
		}
	}
}

// --leader-raft-addr gives the address; the id is the one the leader records,
// and with no record (a node started without -node-id) its address.
func TestResolveLeaderRaft_explicitAddressTakesTheRecordedID(t *testing.T) {
	orig := readLeaderMarker
	defer func() { readLeaderMarker = orig }()

	readLeaderMarker = func(inspector.Node) (string, error) { return testPeerID, nil }
	lr, err := resolveLeaderRaft(inspector.Node{Host: "1.2.3.4"}, "10.0.0.1:10101")
	if err != nil {
		t.Fatal(err)
	}
	if lr.id != testPeerID || lr.addr != "10.0.0.1:10101" {
		t.Errorf("got %+v", lr)
	}

	readLeaderMarker = func(inspector.Node) (string, error) { return "", nil }
	lr, err = resolveLeaderRaft(inspector.Node{Host: "1.2.3.4"}, "10.0.0.1:10101")
	if err != nil {
		t.Fatal(err)
	}
	if lr.id != "10.0.0.1:10101" {
		t.Errorf("unrecorded id = %q, want the address", lr.id)
	}

	if _, err := resolveLeaderRaft(inspector.Node{Host: "1.2.3.4"}, "not-an-address"); err == nil {
		t.Error("a malformed --leader-raft-addr was accepted")
	}
}

func TestValidateRaftID(t *testing.T) {
	for _, ok := range []string{testPeerID, "10.0.0.1:7001"} {
		if err := validateRaftID(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "0OIl-not-base58-0OIl", "12D3Koo; rm", "host:7001"} {
		if err := validateRaftID(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A wiped follower is left a membership record naming the leader, so a
// follower with no join address of its own (the genesis node) joins the
// leader instead of bootstrapping or refusing to start.
func TestBuildFollowerMembershipRecord_namesTheLeader(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	raw, err := buildFollowerMembershipRecord("10.0.0.1:10101", now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), rqlite.ClusterMembershipFileName)
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := rqlite.ReadClusterMembership(path)
	if err != nil || rec == nil {
		t.Fatalf("the record does not read back: %v, %v", rec, err)
	}
	if len(rec.Members) != 1 || rec.Members[0] != "10.0.0.1:10101" {
		t.Errorf("Members = %v, want the leader", rec.Members)
	}
}

func TestBuildFollowerMembershipRecord_rejectsMalformedLeader(t *testing.T) {
	for _, bad := range []string{"", "leader:10101", "10.0.0.1", "10.0.0.1:1; rm -rf /"} {
		if _, err := buildFollowerMembershipRecord(bad, time.Now()); err == nil {
			t.Errorf("leader %q accepted", bad)
		}
	}
}

// The record lands beside the rqlite directory the wipe empties, where the
// node reads it.
func TestFollowerMembershipRecordPath(t *testing.T) {
	if got := rqlite.ClusterMembershipPath(rqliteRoot); got != "/opt/orama/.orama/data/cluster-membership.json" {
		t.Errorf("record path = %s", got)
	}
}

// A missing marker means the leader was started without -node-id; an empty,
// malformed or unreadable one is an error, never "use the address".
func TestParseMarkerRead(t *testing.T) {
	if id, err := parseMarkerRead(markerAbsent + "\n"); err != nil || id != "" {
		t.Errorf("absent: %q, %v", id, err)
	}
	if id, err := parseMarkerRead(markerPresent + testPeerID + "\n"); err != nil || id != testPeerID {
		t.Errorf("present: %q, %v", id, err)
	}
	for _, out := range []string{markerPresent, markerPresent + "a b", "", "cat: permission denied"} {
		if id, err := parseMarkerRead(out); err == nil {
			t.Errorf("%q read as %q", out, id)
		}
	}
}

// The read runs the check and the cat in one script as the orama user, and a
// failing cat fails the command (the script's status is cat's).
func TestMarkerReadCommand(t *testing.T) {
	cmd := markerReadCommand("sudo ")
	if !strings.HasPrefix(cmd, "sudo "+asOramaUser+" sh -c ") {
		t.Errorf("not run as the orama user: %s", cmd)
	}
	if strings.Contains(cmd, "|| true") || strings.Contains(cmd, "2>/dev/null") {
		t.Errorf("a failed read is hidden: %s", cmd)
	}
}

// End to end against a real shell, with the marker path swapped for a temp one.
func TestMarkerReadCommand_distinguishesMissingFromUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	dir := t.TempDir()
	run := func(path string) (string, error) {
		script := strings.ReplaceAll(markerReadCommand(""), rqliteRoot+"/"+rqlite.RaftIDMarkerName, path)
		script = strings.TrimPrefix(script, asOramaUser+" ")
		out, err := exec.Command("sh", "-c", script).Output()
		return string(out), err
	}
	if out, err := run(filepath.Join(dir, "absent")); err != nil || strings.TrimSpace(out) != markerAbsent {
		t.Errorf("absent: %q, %v", out, err)
	}
	unreadable := filepath.Join(dir, "raft-node-id")
	if err := os.WriteFile(unreadable, []byte(testPeerID), 0o000); err != nil {
		t.Fatal(err)
	}
	if out, err := run(unreadable); err == nil {
		t.Errorf("an unreadable marker read as %q", out)
	}
}
