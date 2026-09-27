package hub

import (
	"context"
	"testing"
	"time"
)

func TestDBPeerListerPeers_excludesNodesSilentOverADay(t *testing.T) {
	db := openUptimeDB(t)
	if _, err := db.Exec(`CREATE TABLE dns_nodes (id TEXT PRIMARY KEY, ip_address TEXT, internal_ip TEXT,
		role TEXT, status TEXT, last_seen TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	rows := []struct {
		id       string
		lastSeen time.Time
	}{{"b-recent", now.Add(-time.Minute)}, {"a-dead-hour", now.Add(-time.Hour)}, {"c-gone", now.Add(-25 * time.Hour)}}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO dns_nodes VALUES (?, '1.1.1.1', '10.0.0.1', NULL, 'active', ?)`,
			r.id, r.lastSeen.Format(rqliteTimeLayout)); err != nil {
			t.Fatal(err)
		}
	}
	peers, err := DBPeerLister{DB: db, Now: func() time.Time { return now }}.Peers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 || peers[0].ID != "a-dead-hour" || peers[1].ID != "b-recent" {
		t.Fatalf("peers = %+v, want the two seen within a day, in id order", peers)
	}
	if peers[0].Role != "node" {
		t.Errorf("role = %q, want the default 'node' for a NULL role", peers[0].Role)
	}
}
