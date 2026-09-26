package node

import (
	"context"
	"database/sql/driver"
	"testing"

	"go.uber.org/zap"
)

// A row privhelper would refuse is not applied, and it does not stop the
// rows that are peers from being applied. Register refuses these on write;
// a row already stored must not halt the mesh sync for every node.
func TestScanWGPeers_skipsRowsPrivhelperWouldRefuse(t *testing.T) {
	for name, bad := range map[string][]driver.Value{
		"allowed ips smuggled into wg_ip": row("evil", "0.0.0.0/0,10.0.0.9", keyB, "203.0.113.2", 51820),
		"wg_ip outside the mesh":          row("evil", "192.168.1.5", keyB, "203.0.113.2", 51820),
		"wg_ip with its own prefix":       row("evil", "10.0.0.0/24", keyB, "203.0.113.2", 51820),
		"non-canonical key":               row("evil", "10.0.0.2", keyB+"\n", "203.0.113.2", 51820),
		"key that is not 32 bytes":        row("evil", "10.0.0.2", "c2hvcnQ=", "203.0.113.2", 51820),
		"public_ip that is not an IP":     row("evil", "10.0.0.2", keyB, "example.com", 51820),
		"this node's own overlay address": row("evil", selfWGIP, keyB, "203.0.113.2", 51820),
	} {
		t.Run(name, func(t *testing.T) {
			d := &wgRowsDriver{rows: [][]driver.Value{
				row("nodeA", "10.0.0.1", keyA, "203.0.113.1", 51820),
				bad,
			}}
			db := openWGDB(t, d)
			peers, err := scanWGPeers(context.Background(), db, keySelf, selfWGIP, zap.NewNop())
			if err != nil {
				t.Fatalf("a malformed row halted the sync: %v", err)
			}
			if len(peers) != 1 || peers[keyA].AllowedIP != "10.0.0.1/32" {
				t.Fatalf("peers = %+v, want only the valid row", peers)
			}
		})
	}
}

// wg routes an address to one peer only: two rows claiming it would silently
// hand it to whichever was applied last.
func TestScanWGPeers_refusesTwoRowsForOneAddress(t *testing.T) {
	d := &wgRowsDriver{rows: [][]driver.Value{
		row("nodeA", "10.0.0.1", keyA, "203.0.113.1", 51820),
		row("nodeB", "10.0.0.1", keyB, "203.0.113.2", 51820),
	}}
	db := openWGDB(t, d)
	if _, err := scanWGPeers(context.Background(), db, keySelf, selfWGIP, zap.NewNop()); err == nil {
		t.Fatal("two peers claiming 10.0.0.1/32 were accepted")
	}
}

// A row with no public IP is a peer with no endpoint, not ":51820".
func TestScanWGPeers_rowWithoutPublicIPHasNoEndpoint(t *testing.T) {
	d := &wgRowsDriver{rows: [][]driver.Value{
		row("nodeA", "10.0.0.1", keyA, "", 51820),
	}}
	db := openWGDB(t, d)
	peers, err := scanWGPeers(context.Background(), db, keySelf, selfWGIP, zap.NewNop())
	if err != nil {
		t.Fatalf("scanWGPeers: %v", err)
	}
	if got := peers[keyA]; got.Endpoint != "" || got.AllowedIP != "10.0.0.1/32" {
		t.Errorf("peer = %+v, want no endpoint and 10.0.0.1/32", got)
	}
}
