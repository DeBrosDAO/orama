package rqlite

import (
	"os"
	"path/filepath"
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

func TestResolveRaftIdentity_existingNodeKeepsItsAddressID(t *testing.T) {
	// The critical case. A node that already has raft state is registered under
	// its raft address; passing -node-id would make it join as a SECOND member
	// and leave the old id behind as an unreachable voter.
	dir := t.TempDir()

	got, err := ResolveRaftIdentity(dir, testPeerID, "10.0.0.9:10101", true)
	if err != nil {
		t.Fatalf("ResolveRaftIdentity: %v", err)
	}
	if got.NodeID != "" {
		t.Fatalf("got node id %q; an un-migrated node must pass no -node-id at all "+
			"so rqlite keeps defaulting to the address it is registered under", got.NodeID)
	}
	if got.Migrated {
		t.Fatal("an un-migrated node must not be reported as stable")
	}

	recorded, err := ReadRaftIDMarker(dir)
	if err != nil {
		t.Fatalf("ReadRaftIDMarker: %v", err)
	}
	if recorded != "10.0.0.9:10101" {
		t.Fatalf("marker = %q, want the raft address — the migration needs a concrete "+
			"id to remove, and the address may have changed by then", recorded)
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
