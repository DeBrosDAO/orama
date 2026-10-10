package namespace

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
)

const membershipCluster = "cluster-m"

// membershipDB is the real schema with a three-node cluster in which every node
// holds the three roles provisioning records (rqlite, olric, gateway) — one
// namespace_cluster_nodes row per role, as on a live fleet.
func membershipDB(t *testing.T, nodes ...string) rqlite.Client {
	t.Helper()
	c, db := rqlitetest.SQLite(t)
	if err := rqlite.ApplyEmbeddedMigrations(context.Background(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	for i, id := range nodes {
		port := 10030 + 5*i
		stmts := []string{
			fmt.Sprintf(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES ('%s', '192.0.2.%d', '10.0.0.%d', 'active')`, id, i+1, i+1),
			fmt.Sprintf(`INSERT INTO namespace_port_allocations (id, node_id, namespace_cluster_id, port_start, port_end,
				rqlite_http_port, rqlite_raft_port, olric_http_port, olric_memberlist_port, gateway_http_port)
				VALUES ('pa-%s', '%s', '%s', %d, %d, %d, %d, %d, %d, %d)`,
				id, id, membershipCluster, port, port+4, port, port+1, port+2, port+3, port+4),
		}
		for _, role := range []string{"rqlite_follower", "olric", "gateway"} {
			stmts = append(stmts, fmt.Sprintf(`INSERT INTO namespace_cluster_nodes (id, namespace_cluster_id, node_id, role, status)
				VALUES ('cn-%s-%s', '%s', '%s', '%s', 'running')`, id, role, membershipCluster, id, role))
		}
		for _, s := range stmts {
			if _, err := db.Exec(s); err != nil {
				t.Fatalf("seed %s: %v", id, err)
			}
		}
	}
	return c
}

// TestDesiredLocalConfig_eachPeerOnce is the bug that restarted every freshly
// provisioned gateway: joining the per-role membership rows listed each peer
// three times, so the desired Olric server list never matched the one spawn
// wrote and the reconciler rewrote and restarted a live gateway.
func TestDesiredLocalConfig_eachPeerOnce(t *testing.T) {
	cm := &ClusterManager{db: membershipDB(t, "n1", "n2", "n3"), localNodeID: "n1", logger: zap.NewNop()}

	cfg, err := cm.desiredLocalConfig(context.Background(), membershipCluster)
	if err != nil {
		t.Fatalf("desiredLocalConfig: %v", err)
	}
	wantServers := []string{"10.0.0.1:10032", "10.0.0.2:10037", "10.0.0.3:10042"}
	if !stringSetEqual(cfg.Gateway.OlricServers, wantServers) {
		t.Errorf("olric servers = %v, want %v", cfg.Gateway.OlricServers, wantServers)
	}
	wantPeers := []string{"10.0.0.2:10038", "10.0.0.3:10043"}
	if !stringSetEqual(cfg.Olric.PeerAddresses, wantPeers) {
		t.Errorf("olric peers = %v, want %v", cfg.Olric.PeerAddresses, wantPeers)
	}
}

// A node with an allocation but no membership row (pruned) is not a peer.
func TestDesiredLocalConfig_prunedMemberIsNotAPeer(t *testing.T) {
	db := membershipDB(t, "n1", "n2")
	if _, err := db.Exec(context.Background(), `DELETE FROM namespace_cluster_nodes WHERE node_id = 'n2'`); err != nil {
		t.Fatalf("prune n2: %v", err)
	}
	cm := &ClusterManager{db: db, localNodeID: "n1", logger: zap.NewNop()}

	cfg, err := cm.desiredLocalConfig(context.Background(), membershipCluster)
	if err != nil {
		t.Fatalf("desiredLocalConfig: %v", err)
	}
	if want := []string{"10.0.0.1:10032"}; !stringSetEqual(cfg.Gateway.OlricServers, want) {
		t.Errorf("olric servers = %v, want %v", cfg.Gateway.OlricServers, want)
	}
	if len(cfg.Olric.PeerAddresses) != 0 {
		t.Errorf("olric peers = %v, want none", cfg.Olric.PeerAddresses)
	}
}

// A node with no allocation for the cluster gets no config, not an error.
func TestDesiredLocalConfig_noAllocationIsNil(t *testing.T) {
	cm := &ClusterManager{db: membershipDB(t, "n1"), localNodeID: "absent", logger: zap.NewNop()}
	cfg, err := cm.desiredLocalConfig(context.Background(), membershipCluster)
	if err != nil || cfg != nil {
		t.Fatalf("desiredLocalConfig = %v, %v; want nil, nil", cfg, err)
	}
}

// survivingNodes shares the membership test, so a raft removal is not issued
// three times through the same survivor.
func TestSurvivingNodes_eachMemberOnce(t *testing.T) {
	cm := &ClusterManager{db: membershipDB(t, "n1", "n2", "n3"), logger: zap.NewNop()}
	got, err := cm.survivingNodes(context.Background(), membershipCluster)
	if err != nil {
		t.Fatalf("survivingNodes: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("survivingNodes returned %d rows, want 3: %+v", len(got), got)
	}
}
