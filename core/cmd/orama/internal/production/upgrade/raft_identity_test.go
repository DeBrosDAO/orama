package upgrade

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// memStore is the rqlite data directory in memory.
type memStore struct {
	files map[string]string
	state bool
}

func (m *memStore) read(name string) (string, error) { return strings.TrimSpace(m.files[name]), nil }
func (m *memStore) write(name string, data []byte) error {
	m.files[name] = string(data)
	return nil
}
func (m *memStore) hasRaftState() (bool, error) { return m.state, nil }

func statusOf(nodeID string, members map[string]string) func() (*rqlite.RQLiteStatus, error) {
	return func() (*rqlite.RQLiteStatus, error) {
		st := &rqlite.RQLiteStatus{}
		st.Store.NodeID = nodeID
		for id, addr := range members {
			st.Store.Nodes = append(st.Store.Nodes, struct {
				ID       string `json:"id"`
				Addr     string `json:"addr"`
				Suffrage string `json:"suffrage"`
			}{ID: id, Addr: addr})
		}
		return st, nil
	}
}

var noRQLite = func() (*rqlite.RQLiteStatus, error) { return nil, errors.New("connection refused") }

// A 0.122.x node: address ids, index raft on 7001. The upgrade records the id
// rqlited runs under, the address the configuration holds it at, and the
// members — what the node needs to restart on :10101 under the same id and
// join again.
func TestCaptureRaftIdentity_recordsWhatTheRunningRQLiteSays(t *testing.T) {
	store := &memStore{files: map[string]string{}, state: true}
	status := statusOf("10.0.0.2:7001", map[string]string{
		"10.0.0.1:7001": "10.0.0.1:7001", "10.0.0.2:7001": "10.0.0.2:7001", "10.0.0.3:7001": "10.0.0.3:7001",
	})
	if err := captureRaftIdentity(store, status, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(store.files[rqlite.RaftIDMarkerName]); got != "10.0.0.2:7001" {
		t.Errorf("id marker = %q", got)
	}
	if got := strings.TrimSpace(store.files[rqlite.RaftAddrMarkerName]); got != "10.0.0.2:7001" {
		t.Errorf("address marker = %q", got)
	}
	var rec rqlite.ClusterMembership
	if err := json.Unmarshal([]byte(store.files[rqlite.ClusterMembershipFileName]), &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Members) != 3 {
		t.Errorf("members = %v", rec.Members)
	}
}

// A recorded id that is not the one rqlited runs under is a contradiction the
// upgrade refuses, before anything is stopped.
func TestCaptureRaftIdentity_refusesAContradictedID(t *testing.T) {
	store := &memStore{files: map[string]string{rqlite.RaftIDMarkerName: "10.0.0.2:7001"}, state: true}
	status := statusOf("12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy", map[string]string{"12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy": "10.0.0.2:10101"})
	if err := captureRaftIdentity(store, status, time.Now()); err == nil {
		t.Fatal("a contradicted raft id was accepted")
	}
}

// Re-running an upgrade on a node it already stopped: rqlited is down, the id
// is recorded, nothing to do. With no id recorded and raft state present, the
// id is unknown and the upgrade stops before stopping anything.
func TestCaptureRaftIdentity_rqliteDown(t *testing.T) {
	recorded := &memStore{files: map[string]string{rqlite.RaftIDMarkerName: "10.0.0.2:7001", rqlite.RaftAddrMarkerName: "10.0.0.2:7001"}, state: true}
	if err := captureRaftIdentity(recorded, noRQLite, time.Now()); err != nil {
		t.Errorf("recorded id and address, rqlite down: %v", err)
	}
	// A crash between the two marker writes: the id without the address would
	// restart on the new address without joining again.
	half := &memStore{files: map[string]string{rqlite.RaftIDMarkerName: "10.0.0.2:7001"}, state: true}
	if err := captureRaftIdentity(half, noRQLite, time.Now()); err == nil {
		t.Error("an id without an address was accepted with rqlited down")
	}
	fresh := &memStore{files: map[string]string{}, state: false}
	if err := captureRaftIdentity(fresh, noRQLite, time.Now()); err != nil {
		t.Errorf("no raft state, rqlite down: %v", err)
	}
	unknown := &memStore{files: map[string]string{}, state: true}
	if err := captureRaftIdentity(unknown, noRQLite, time.Now()); err == nil {
		t.Error("raft state with no recorded id and no rqlited was accepted")
	}
}

// A node outside its own configuration is not upgraded.
func TestCaptureRaftIdentity_refusesANodeOutsideItsConfiguration(t *testing.T) {
	store := &memStore{files: map[string]string{}, state: true}
	status := statusOf("10.0.0.2:7001", map[string]string{"10.0.0.1:7001": "10.0.0.1:7001"})
	if err := captureRaftIdentity(store, status, time.Now()); err == nil {
		t.Fatal("a node outside its configuration was recorded")
	}
	if len(store.files) != 0 {
		t.Errorf("wrote %v", store.files)
	}
}

// A non-voter keeps its suffrage across the rejoin: the capture records it.
func TestCaptureRaftIdentity_recordsSuffrage(t *testing.T) {
	store := &memStore{files: map[string]string{}, state: true}
	status := func() (*rqlite.RQLiteStatus, error) {
		st := &rqlite.RQLiteStatus{}
		st.Store.NodeID = "10.0.0.6:7001"
		st.Store.Nodes = append(st.Store.Nodes, struct {
			ID       string `json:"id"`
			Addr     string `json:"addr"`
			Suffrage string `json:"suffrage"`
		}{ID: "10.0.0.6:7001", Addr: "10.0.0.6:7001", Suffrage: "Nonvoter"})
		return st, nil
	}
	if err := captureRaftIdentity(store, status, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(store.files[rqlite.RaftSuffrageMarkerName]); got != rqlite.SuffrageNonvoter {
		t.Errorf("suffrage marker = %q", got)
	}
}
