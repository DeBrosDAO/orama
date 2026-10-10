package rqlite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPeerID = "12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy"

func TestResolveRaftIdentity_freshNodeTakesTheStableID(t *testing.T) {
	dir := t.TempDir()

	got, err := ResolveRaftIdentity(dir, testPeerID, "10.0.0.9:10101", false)
	if err != nil {
		t.Fatalf("ResolveRaftIdentity: %v", err)
	}
	if got.NodeID != testPeerID || !got.Migrated {
		t.Fatalf("got %+v, want the peer id, marked stable", got)
	}

	recorded, err := ReadRaftIDMarker(dir)
	if err != nil {
		t.Fatalf("ReadRaftIDMarker: %v", err)
	}
	if recorded != testPeerID {
		t.Fatalf("marker = %q, want the peer id — without it the next boot cannot tell "+
			"which id the cluster has this node under", recorded)
	}
}

// A node with raft state and no recorded id predates recorded ids: its id is
// the address it last ran under, which nothing on the node states any more.
// Guessing the current address is what put a node outside its own
// configuration when a release moved the raft port, so it refuses.
func TestResolveRaftIdentity_stateWithoutARecordedIDRefuses(t *testing.T) {
	dir := t.TempDir()

	_, err := ResolveRaftIdentity(dir, testPeerID, "10.0.0.9:10101", true)
	if err == nil {
		t.Fatal("raft state without a recorded id was given an id")
	}
	if !strings.Contains(err.Error(), RaftIDMarkerName) {
		t.Errorf("the refusal does not name the marker to write: %v", err)
	}
	if recorded, _ := ReadRaftIDMarker(dir); recorded != "" {
		t.Fatalf("a refusal recorded %q", recorded)
	}
}

// The marker is honoured with or without a peer id: libp2p being late must
// not turn a recorded id into rqlite's address default.
func TestResolveRaftIdentity_markerWinsWithoutAPeerID(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRaftIDMarker(dir, "10.0.0.4:7001"); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveRaftIdentity(dir, "", "10.0.0.4:10101", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "10.0.0.4:7001" {
		t.Fatalf("NodeID = %q, want the recorded id", got.NodeID)
	}
}

// The raft port moving from 7001 to 10101 keeps the id and reports the move:
// the recorded address is what the configuration holds, and it is not where
// rqlited now listens.
func TestResolveRaftIdentity_reportsAnAddressChange(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRaftIDMarker(dir, "10.0.0.4:7001"); err != nil {
		t.Fatal(err)
	}
	if err := WriteRaftAddrMarker(dir, "10.0.0.4:7001"); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveRaftIdentity(dir, testPeerID, "10.0.0.4:10101", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "10.0.0.4:7001" || got.PreviousAddr != "10.0.0.4:7001" {
		t.Fatalf("got %+v", got)
	}
	if !got.AddressChanged("10.0.0.4:10101") {
		t.Error("a moved raft port was not reported as an address change")
	}
	if got.AddressChanged("10.0.0.4:7001") {
		t.Error("an unchanged address was reported as changed")
	}
	if (RaftIdentity{}).AddressChanged("10.0.0.4:10101") {
		t.Error("no recorded address is not a change")
	}
}

func TestWriteRaftAddrMarker_refusesWhatIsNotAnAddress(t *testing.T) {
	for _, bad := range []string{"", testPeerID, "10.0.0.1", "host:10101"} {
		if err := WriteRaftAddrMarker(t.TempDir(), bad); err == nil {
			t.Errorf("recorded %q as a raft address", bad)
		}
	}
}

func liveStatus(nodeID string, members ...[2]string) *RQLiteStatus {
	st := &RQLiteStatus{}
	st.Store.NodeID = nodeID
	for _, m := range members {
		st.Store.Nodes = append(st.Store.Nodes, struct {
			ID       string `json:"id"`
			Addr     string `json:"addr"`
			Suffrage string `json:"suffrage"`
		}{ID: m[0], Addr: m[1]})
	}
	return st
}

// The node's own address comes from the configuration, found by its id.
func TestLiveIdentityFromStatus_takesTheOwnAddressFromTheConfiguration(t *testing.T) {
	live, err := LiveIdentityFromStatus(liveStatus("10.0.0.2:7001",
		[2]string{"10.0.0.1:7001", "10.0.0.1:7001"},
		[2]string{"10.0.0.2:7001", "10.0.0.2:7001"},
		[2]string{testPeerID, "10.0.0.3:10101"}))
	if err != nil {
		t.Fatal(err)
	}
	if live.NodeID != "10.0.0.2:7001" || live.Addr != "10.0.0.2:7001" {
		t.Errorf("got %+v", live)
	}
	if len(live.Members) != 3 {
		t.Errorf("members = %v", live.Members)
	}
}

func TestLiveIdentityFromStatus_refusesANodeOutsideItsConfiguration(t *testing.T) {
	cases := map[string]*RQLiteStatus{
		"no status":         nil,
		"no node id":        liveStatus("", [2]string{"a", "10.0.0.1:7001"}),
		"not a member":      liveStatus("10.0.0.2:10101", [2]string{"10.0.0.2:7001", "10.0.0.2:7001"}),
		"malformed address": liveStatus(testPeerID, [2]string{testPeerID, "not-an-address"}),
		"malformed node id": liveStatus("x'; rm -rf /", [2]string{"x'; rm -rf /", "10.0.0.2:10101"}),
	}
	for name, st := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LiveIdentityFromStatus(st); err == nil {
				t.Fatal("expected a refusal")
			}
		})
	}
}

func TestCheckRecordedID(t *testing.T) {
	live := LiveIdentity{NodeID: testPeerID, Addr: "10.0.0.2:10101"}
	if err := CheckRecordedID("", live); err != nil {
		t.Errorf("no recorded id: %v", err)
	}
	if err := CheckRecordedID(testPeerID, live); err != nil {
		t.Errorf("matching id: %v", err)
	}
	if err := CheckRecordedID("10.0.0.2:7001", live); err == nil {
		t.Error("a recorded id rqlited does not run under was accepted")
	}
}

func TestResolveRaftIdentity_markerIsAuthoritative(t *testing.T) {
	// Once recorded, the marker wins over everything: it is what rqlite's
	// persisted configuration has this node under.
	dir := t.TempDir()
	if err := WriteRaftIDMarker(dir, "10.0.0.4:10101"); err != nil {
		t.Fatalf("WriteRaftIDMarker: %v", err)
	}

	got, err := ResolveRaftIdentity(dir, testPeerID, "10.0.0.9:10101", true)
	if err != nil {
		t.Fatalf("ResolveRaftIdentity: %v", err)
	}
	if got.NodeID != "10.0.0.4:10101" {
		t.Fatalf("got %q, want the recorded id even though the address has since changed", got.NodeID)
	}
	if got.Migrated {
		t.Fatal("an address-derived id must not be reported as stable")
	}
}

func TestResolveRaftIdentity_markerMatchingThePeerIDIsStable(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRaftIDMarker(dir, testPeerID); err != nil {
		t.Fatalf("WriteRaftIDMarker: %v", err)
	}

	got, err := ResolveRaftIdentity(dir, testPeerID, "10.0.0.9:10101", true)
	if err != nil {
		t.Fatalf("ResolveRaftIdentity: %v", err)
	}
	if got.NodeID != testPeerID || !got.Migrated {
		t.Fatalf("got %+v, want the peer id, marked stable", got)
	}
}

func TestResolveRaftIdentity_noPeerIDChangesNothing(t *testing.T) {
	// libp2p not up yet. Behaviour must be exactly what it was before stable
	// ids existed: no -node-id, and no marker invented.
	dir := t.TempDir()

	got, err := ResolveRaftIdentity(dir, "", "10.0.0.9:10101", false)
	if err != nil {
		t.Fatalf("ResolveRaftIdentity: %v", err)
	}
	if got.NodeID != "" || got.Migrated {
		t.Fatalf("got %+v, want no id at all", got)
	}
	if _, err := os.Stat(filepath.Join(dir, RaftIDMarkerName)); !os.IsNotExist(err) {
		t.Fatal("a marker was written without a peer id to record")
	}
}

func TestReadRaftIDMarker_absentIsNotAnError(t *testing.T) {
	got, err := ReadRaftIDMarker(t.TempDir())
	if err != nil {
		t.Fatalf("a missing marker must not be an error: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestReadRaftIDMarker_trimsTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RaftIDMarkerName), []byte(testPeerID+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadRaftIDMarker(dir)
	if err != nil {
		t.Fatalf("ReadRaftIDMarker: %v", err)
	}
	if got != testPeerID {
		t.Fatalf("got %q, want %q", got, testPeerID)
	}
}

func TestWriteRaftIDMarker_refusesAnEmptyID(t *testing.T) {
	// An empty marker reads back as "no marker", which would silently
	// re-classify a migrated node as un-migrated on its next boot.
	for _, id := range []string{"", "   ", "\n"} {
		if err := WriteRaftIDMarker(t.TempDir(), id); err == nil {
			t.Fatalf("expected %q to be refused", id)
		}
	}
}

func TestWriteRaftIDMarker_leavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRaftIDMarker(dir, testPeerID); err != nil {
		t.Fatalf("WriteRaftIDMarker: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != RaftIDMarkerName {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want only the marker", names)
	}
}

func TestWriteRaftIDMarker_overwritesAnExistingID(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRaftIDMarker(dir, "10.0.0.4:10101"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteRaftIDMarker(dir, testPeerID); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, _ := ReadRaftIDMarker(dir)
	if got != testPeerID {
		t.Fatalf("got %q, want the id written second", got)
	}
}

func TestValidateRaftID(t *testing.T) {
	for _, ok := range []string{testPeerID, "10.0.0.1:7001"} {
		if err := ValidateRaftID(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "0OIl-not-base58-0OIl", "12D3Koo; rm", "host:7001", "12D3KooWshort"} {
		if err := ValidateRaftID(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestResolveRaftIdentity_readsTheRecordedSuffrage(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRaftIDMarker(dir, "10.0.0.6:7001"); err != nil {
		t.Fatal(err)
	}
	if err := WriteRaftSuffrageMarker(dir, SuffrageNonvoter); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveRaftIdentity(dir, testPeerID, "10.0.0.6:10101", true)
	if err != nil || !got.NonVoter {
		t.Fatalf("got %+v, %v; want a non-voter", got, err)
	}
	if err := WriteRaftSuffrageMarker(dir, "maybe"); err == nil {
		t.Error("an unknown suffrage was recorded")
	}
}
