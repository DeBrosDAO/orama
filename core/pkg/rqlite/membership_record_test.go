package rqlite

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"go.uber.org/zap"
)

func TestReadClusterMembership_absentIsNil(t *testing.T) {
	rec, err := ReadClusterMembership(filepath.Join(t.TempDir(), ClusterMembershipFileName))
	if err != nil || rec != nil {
		t.Fatalf("absent record: rec=%v err=%v, want nil, nil", rec, err)
	}
}

func TestReadClusterMembership_corruptIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), ClusterMembershipFileName)
	if err := os.WriteFile(path, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadClusterMembership(path); err == nil {
		t.Fatal("a corrupt record read as valid or absent")
	}
}

// The members are recorded sorted and de-duplicated, and nodes without an
// address are skipped.
func TestUpdateClusterMembership_recordsSortedMembers(t *testing.T) {
	path := filepath.Join(t.TempDir(), ClusterMembershipFileName)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	nodes := []string{"10.0.0.2:10101", "10.0.0.1:10101", "", "10.0.0.2:10101"}

	wrote, err := updateClusterMembership(path, nodes, now)
	if err != nil || !wrote {
		t.Fatalf("first record: wrote=%v err=%v", wrote, err)
	}
	rec, err := ReadClusterMembership(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"10.0.0.1:10101", "10.0.0.2:10101"}; !reflect.DeepEqual(rec.Members, want) {
		t.Errorf("Members = %v, want %v", rec.Members, want)
	}
	if !rec.FirstSeen.Equal(now) {
		t.Errorf("FirstSeen = %v, want %v", rec.FirstSeen, now)
	}
}

// An unchanged set is not rewritten; a changed one is, keeping first-seen.
func TestUpdateClusterMembership_rewritesOnlyOnChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), ClusterMembershipFileName)
	first := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := updateClusterMembership(path, []string{"10.0.0.1:10101"}, first); err != nil {
		t.Fatal(err)
	}
	if wrote, err := updateClusterMembership(path, []string{"10.0.0.1:10101"}, first.Add(time.Hour)); err != nil || wrote {
		t.Fatalf("unchanged set: wrote=%v err=%v, want no write", wrote, err)
	}

	later := first.Add(48 * time.Hour)
	if wrote, err := updateClusterMembership(path, []string{"10.0.0.1:10101", "10.0.0.17:10101"}, later); err != nil || !wrote {
		t.Fatalf("changed set: wrote=%v err=%v, want a write", wrote, err)
	}
	rec, err := ReadClusterMembership(path)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.FirstSeen.Equal(first) {
		t.Errorf("FirstSeen moved to %v; it must stay %v", rec.FirstSeen, first)
	}
	if len(rec.Members) != 2 {
		t.Errorf("Members = %v, want both", rec.Members)
	}
}

// A raft configuration with no addresses must not erase what is recorded.
func TestUpdateClusterMembership_refusesAnEmptyCluster(t *testing.T) {
	path := filepath.Join(t.TempDir(), ClusterMembershipFileName)
	if _, err := updateClusterMembership(path, []string{"10.0.0.1:10101"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := updateClusterMembership(path, []string{""}, time.Now()); err == nil {
		t.Fatal("an empty member set was accepted")
	}
	rec, err := ReadClusterMembership(path)
	if err != nil || rec == nil || len(rec.Members) != 1 {
		t.Errorf("the record was damaged: %+v, %v", rec, err)
	}
}

func TestHasRecoveryPeers(t *testing.T) {
	dir := t.TempDir()
	if has, err := HasRecoveryPeers(dir); err != nil || has {
		t.Fatalf("empty dir: has=%v err=%v", has, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "raft"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "raft", "peers.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if has, err := HasRecoveryPeers(dir); err != nil || !has {
		t.Fatalf("with peers.json: has=%v err=%v", has, err)
	}
}

func TestValidateRaftAddress(t *testing.T) {
	if err := ValidateRaftAddress("10.0.0.1:10101"); err != nil {
		t.Errorf("valid address refused: %v", err)
	}
	for _, bad := range []string{"", "10.0.0.1", "host:10101", "10.0.0.1:x", "10.0.0.1:0", "10.0.0.1:70000", "10.0.0.1:1 -http-addr 0.0.0.0:1"} {
		if err := ValidateRaftAddress(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// A member address that is not ip:port is never written: it would end up in
// the -join of a unit env file.
func TestUpdateClusterMembership_refusesMalformedMember(t *testing.T) {
	path := filepath.Join(t.TempDir(), ClusterMembershipFileName)
	if _, err := updateClusterMembership(path, []string{"10.0.0.1:1 -http-addr 0.0.0.0:1"}, time.Now()); err == nil {
		t.Fatal("a malformed member address was recorded")
	}
	if rec, _ := ReadClusterMembership(path); rec != nil {
		t.Errorf("record written: %+v", rec)
	}
}

// raftConfigServer answers /status with body, and counts requests to /nodes.
func raftConfigServer(t *testing.T, body string) (*RQLiteManager, *int) {
	t.Helper()
	nodesCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			_, _ = w.Write([]byte(body))
		case "/nodes":
			nodesCalls++
			http.Error(w, "probing", http.StatusGatewayTimeout)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	mgr := NewRQLiteManager(&config.DatabaseConfig{RQLitePort: port, RQLiteUsername: "u", RQLitePassword: "p"},
		&config.DiscoveryConfig{HttpAdvAddress: u.Host}, dataDir, zap.NewNop())
	return mgr, &nodesCalls
}

// The record comes from the raft configuration in /status — local, so a
// member that is down cannot stall it — and lands beside the rqlite dir.
func TestRecordClusterMembership_readsRaftConfigurationFromStatus(t *testing.T) {
	mgr, nodesCalls := raftConfigServer(t, `{"store":{"node_id":"12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy","nodes":[
		{"id":"12D3KooWQueryQueryQueryQueryQuery2","addr":"10.0.0.1:10101","suffrage":"Voter"},
		{"id":"12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy","addr":"10.0.0.2:10101","suffrage":"Voter"}]}}`)
	if err := mgr.RecordClusterMembership(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *nodesCalls != 0 {
		t.Errorf("/nodes was called %d times; it probes every member", *nodesCalls)
	}
	path, err := mgr.ClusterMembershipPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(mgr.dataDir, ClusterMembershipFileName); path != want {
		t.Errorf("record at %s, want beside the rqlite dir at %s", path, want)
	}
	rec, err := ReadClusterMembership(path)
	if err != nil || rec == nil {
		t.Fatalf("record: %v, %v", rec, err)
	}
	if want := []string{"10.0.0.1:10101", "10.0.0.2:10101"}; !reflect.DeepEqual(rec.Members, want) {
		t.Errorf("Members = %v, want %v", rec.Members, want)
	}
}

func TestRecordClusterMembership_emptyConfigurationIsAnError(t *testing.T) {
	mgr, _ := raftConfigServer(t, `{"store":{"node_id":"12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy","nodes":[]}}`)
	if err := mgr.RecordClusterMembership(context.Background()); err == nil {
		t.Fatal("an empty raft configuration was recorded")
	}
}

func TestClusterMembershipPath_besideTheRQLiteDir(t *testing.T) {
	if got := ClusterMembershipPath("/opt/orama/.orama/data/rqlite"); got != "/opt/orama/.orama/data/cluster-membership.json" {
		t.Errorf("ClusterMembershipPath = %s", got)
	}
}

// Recording membership also records the address the configuration holds this
// node at. A node restarted on a new address passes -join until this changes,
// so it must change only once the configuration says so.
func TestRecordClusterMembership_recordsTheConfirmedAddress(t *testing.T) {
	mgr, _ := raftConfigServer(t, `{"store":{"node_id":"10.0.0.2:7001","nodes":[
		{"id":"10.0.0.1:7001","addr":"10.0.0.1:7001","suffrage":"Voter"},
		{"id":"10.0.0.2:7001","addr":"10.0.0.2:10101","suffrage":"Voter"}]}}`)
	rqliteDir, err := mgr.rqliteDataDirPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteRaftAddrMarker(rqliteDir, "10.0.0.2:7001"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RecordClusterMembership(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadRaftAddrMarker(rqliteDir); got != "10.0.0.2:10101" {
		t.Errorf("address marker = %q, want the address the configuration now holds", got)
	}
	if got, _ := readMarker(rqliteDir, raftSuffrageMarker); got != SuffrageVoter {
		t.Errorf("suffrage marker = %q", got)
	}
}

// A node that is not in its own configuration records nothing: that is not
// membership, and recording it would end the rejoin.
func TestRecordClusterMembership_notAMemberRecordsNothing(t *testing.T) {
	mgr, _ := raftConfigServer(t, `{"store":{"node_id":"10.0.0.2:10101","nodes":[
		{"id":"10.0.0.2:7001","addr":"10.0.0.2:7001","suffrage":"Voter"}]}}`)
	if err := mgr.RecordClusterMembership(context.Background()); err == nil {
		t.Fatal("a node outside its own configuration was recorded as a member")
	}
	rqliteDir, _ := mgr.rqliteDataDirPath()
	if got, _ := ReadRaftAddrMarker(rqliteDir); got != "" {
		t.Errorf("address marker = %q", got)
	}
}

// A torn record is distinguishable from one that cannot be read at all: a node
// holding raft state replaces it (pkg/namespace).
func TestReadClusterMembership_corruptWrapsTheSentinel(t *testing.T) {
	path := filepath.Join(t.TempDir(), ClusterMembershipFileName)
	if err := os.WriteFile(path, []byte(`{"first_seen":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadClusterMembership(path); !errors.Is(err, ErrCorruptMembershipRecord) {
		t.Fatalf("err = %v, want ErrCorruptMembershipRecord", err)
	}
}

// Two writers never share a temporary file, and none is left behind.
func TestWriteClusterMembership_leavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ClusterMembershipFileName)
	for i := 0; i < 3; i++ {
		if err := WriteClusterMembership(path, ClusterMembership{Members: []string{"10.0.0.1:10101"}}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only the record", len(entries))
	}
}
