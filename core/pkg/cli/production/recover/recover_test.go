package recover

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseLeaderRaftID_findsLeader(t *testing.T) {
	body := `{
		"10.0.0.1:7001": {"id":"10.0.0.1:7001","leader":true,"voter":true,"reachable":true},
		"10.0.0.2:7001": {"id":"10.0.0.2:7001","leader":false,"voter":true,"reachable":true},
		"10.0.0.6:7001": {"id":"10.0.0.6:7001","leader":false,"voter":false,"reachable":true}
	}`
	got, err := parseLeaderRaftID([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "10.0.0.1:7001" {
		t.Errorf("parseLeaderRaftID() = %q, want %q", got, "10.0.0.1:7001")
	}
}

func TestParseLeaderRaftID_noLeader(t *testing.T) {
	// A cluster with no elected leader (all followers/candidates) must error,
	// not silently return an empty address that would produce a broken peers.json.
	body := `{
		"10.0.0.1:7001": {"id":"10.0.0.1:7001","leader":false},
		"10.0.0.2:7001": {"id":"10.0.0.2:7001","leader":false}
	}`
	if _, err := parseLeaderRaftID([]byte(body)); err == nil {
		t.Fatal("expected error when no node reports leader==true, got nil")
	}
}

func TestParseLeaderRaftID_emptyResponse(t *testing.T) {
	if _, err := parseLeaderRaftID([]byte(`{}`)); err == nil {
		t.Fatal("expected error on empty /nodes response, got nil")
	}
}

func TestParseLeaderRaftID_invalidJSON(t *testing.T) {
	if _, err := parseLeaderRaftID([]byte(`not json`)); err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}

func TestParseLeaderRaftID_rejectsMalformedAddress(t *testing.T) {
	// A compromised or corrupt node could return a leader "id" that is not a
	// real raft host:port. It must be rejected before it ever reaches peers.json.
	cases := map[string]string{
		"shell injection attempt": `{"; rm -rf / #":{"leader":true}}`,
		"not host:port":           `{"garbage":{"leader":true}}`,
		"non-ip host":             `{"evil.example.com:7001":{"leader":true}}`,
		"empty host":              `{":7001":{"leader":true}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := parseLeaderRaftID([]byte(body)); err == nil {
				t.Fatalf("expected error for %s, got address %q", name, got)
			}
		})
	}
}

func TestParseLeaderRaftID_acceptsWireGuardAddress(t *testing.T) {
	got, err := parseLeaderRaftID([]byte(`{"10.0.0.6:7001":{"leader":true}}`))
	if err != nil {
		t.Fatalf("unexpected error for valid WireGuard address: %v", err)
	}
	if got != "10.0.0.6:7001" {
		t.Errorf("got %q, want 10.0.0.6:7001", got)
	}
}

func TestBuildSingleNodePeersJSON_shape(t *testing.T) {
	out, err := buildSingleNodePeersJSON("10.0.0.1:7001")
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
	if p["id"] != "10.0.0.1:7001" {
		t.Errorf("id = %v, want 10.0.0.1:7001", p["id"])
	}
	if p["address"] != "10.0.0.1:7001" {
		t.Errorf("address = %v, want 10.0.0.1:7001", p["address"])
	}
	// The single recovered node MUST be a voter, or it can never elect itself
	// leader and the whole recovery deadlocks.
	if nv, ok := p["non_voter"].(bool); !ok || nv {
		t.Errorf("non_voter = %v, want false (single node must be a voter)", p["non_voter"])
	}
}

func TestBuildSingleNodePeersJSON_roundTripsLeaderFromNodes(t *testing.T) {
	// End-to-end of the pure logic: whatever address we extract as leader is the
	// exact address that ends up in the recovery peers.json.
	nodesBody := `{"10.0.0.7:7001":{"leader":true},"10.0.0.2:7001":{"leader":false}}`
	addr, err := parseLeaderRaftID([]byte(nodesBody))
	if err != nil {
		t.Fatalf("parseLeaderRaftID: %v", err)
	}
	out, err := buildSingleNodePeersJSON(addr)
	if err != nil {
		t.Fatalf("buildSingleNodePeersJSON: %v", err)
	}
	if !strings.Contains(out, `"10.0.0.7:7001"`) {
		t.Errorf("peers.json missing leader address %q:\n%s", addr, out)
	}
}
