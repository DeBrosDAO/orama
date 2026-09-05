package node

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/environments/production"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

const (
	// defaultWireGuardPort is the listen port assumed for a peer row that
	// records wg_port = 0 (older rows predate the column being populated).
	defaultWireGuardPort = 51820

	// wgKeyLogPrefixLen is how much of a WireGuard public key is kept in log
	// lines — enough to correlate against `wg show`, short enough to read.
	wgKeyLogPrefixLen = 8

	// wgSyncInterval is how often the local interface is reconciled against
	// cluster membership.
	wgSyncInterval = 60 * time.Second

	// wgBootstrapTimeout bounds the startup-path mesh repair. It runs before
	// rqlite has a leader, so it must not be able to stall boot; the periodic
	// sync retries anything missed.
	wgBootstrapTimeout = 10 * time.Second
)

// wgPeerQuery selects the mesh membership the local interface is reconciled
// against. Ordered by wg_ip so log output and tests are deterministic.
const wgPeerQuery = "SELECT node_id, wg_ip, public_key, public_ip, wg_port FROM wireguard_peers ORDER BY wg_ip"

// desiredWGPeers is the outcome of loading the mesh membership.
//
// Authoritative distinguishes "this is the cluster's committed membership"
// from "this is the best guess I could scrape locally". Only an authoritative
// set may drive REMOVALS — see reconcileWireGuardPeers.
type desiredWGPeers struct {
	peers         map[string]production.WireGuardPeer
	authoritative bool
	source        string
}

// loadDesiredWireGuardPeers reads the mesh membership, preferring the
// leader-routed view and falling back to this node's local replica.
//
// Why the fallback exists (the bootstrap deadlock): raft runs OVER the
// WireGuard mesh. A node whose interface has no peers cannot reach any other
// node, so its rqlite can never elect a leader, so a leader-routed read of
// `wireguard_peers` can never succeed — and the peer list is precisely what it
// needs to repair the mesh. Left there, a single restart is an unrecoverable
// outage: the rows sit readable on local disk while the node retries a query
// that cannot succeed until those very rows are applied.
//
// The local replica may be stale, so a fallback result is returned with
// authoritative=false and is only ever used to ADD peers. Adding a peer that
// has since left is self-correcting (it simply never handshakes, and the next
// authoritative sync removes it); failing to add one is not.
func (n *Node) loadDesiredWireGuardPeers(ctx context.Context, localPubKey string) (desiredWGPeers, error) {
	leaderDB := n.getRQLiteAdapter().GetSQLDB()
	peers, err := scanWGPeers(ctx, leaderDB, localPubKey)
	if err == nil {
		return desiredWGPeers{peers: peers, authoritative: true, source: "leader"}, nil
	}
	leaderErr := err

	localDB, localErr := n.getRQLiteAdapter().LocalDB()
	if localErr != nil {
		return desiredWGPeers{}, fmt.Errorf("failed to query wireguard_peers (leader: %v; local handle: %w)", leaderErr, localErr)
	}
	peers, localErr = scanWGPeers(ctx, localDB, localPubKey)
	if localErr != nil {
		return desiredWGPeers{}, fmt.Errorf("failed to query wireguard_peers (leader: %v; local: %w)", leaderErr, localErr)
	}

	n.logger.ComponentWarn(logging.ComponentNode,
		"WireGuard peer list unavailable from the raft leader — falling back to the local replica so the mesh can be repaired without a quorum (additive only, no peers will be removed)",
		zap.Int("local_peers", len(peers)),
		zap.Error(leaderErr))
	return desiredWGPeers{peers: peers, authoritative: false, source: "local-replica"}, nil
}

// scanWGPeers runs the membership query against db and returns the peer set
// keyed by public key, excluding this node.
//
// A row that fails to scan is a hard error rather than a skip: silently
// dropping rows shrinks the desired set, and a short desired set used to mean
// "remove the peers that are missing from it" — i.e. a malformed row could cut
// the node out of the mesh. Callers get all-or-nothing.
func scanWGPeers(ctx context.Context, db *sql.DB, localPubKey string) (map[string]production.WireGuardPeer, error) {
	rows, err := rqlite.SafeQueryContext(db, ctx, wgPeerQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	peers := make(map[string]production.WireGuardPeer)
	for rows.Next() {
		var nodeID, wgIP, pubKey, pubIP string
		var wgPort int
		if err := rows.Scan(&nodeID, &wgIP, &pubKey, &pubIP, &wgPort); err != nil {
			return nil, fmt.Errorf("scan wireguard_peers row: %w", err)
		}
		if pubKey == "" || wgIP == "" {
			return nil, fmt.Errorf("wireguard_peers row for node %q has an empty public_key or wg_ip", nodeID)
		}
		if pubKey == localPubKey {
			continue // skip self
		}
		if wgPort == 0 {
			wgPort = defaultWireGuardPort
		}
		peers[pubKey] = production.WireGuardPeer{
			PublicKey: pubKey,
			Endpoint:  fmt.Sprintf("%s:%d", pubIP, wgPort),
			AllowedIP: wgIP + "/32",
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate wireguard_peers: %w", err)
	}
	return peers, nil
}

// syncWireGuardPeers reads the mesh membership and reconciles the local
// WireGuard interface against it. Called at startup and every syncInterval.
//
// The lock matters: a retry on the supervisor goroutine can land on the same
// moment as a tick of the periodic loop, and both write wg0.conf. `wg set` is
// idempotent, but the config file rewrite is not.
func (n *Node) syncWireGuardPeers(ctx context.Context) error {
	if n.getRQLiteAdapter() == nil {
		return fmt.Errorf("rqlite adapter not initialized")
	}

	n.wgSyncMu.Lock()
	defer n.wgSyncMu.Unlock()

	// Check if WireGuard is installed and active
	if _, err := exec.LookPath("wg"); err != nil {
		n.logger.ComponentInfo(logging.ComponentNode, "WireGuard not installed, skipping peer sync")
		return nil
	}

	// Check if wg0 interface exists
	out, err := exec.CommandContext(ctx, "wg", "show", "wg0").CombinedOutput()
	if err != nil {
		n.logger.ComponentInfo(logging.ComponentNode, "WireGuard interface wg0 not active, skipping peer sync")
		return nil
	}

	localPubKey := parseWGShowLocalKey(string(out))

	// Read the live peers WITH their endpoints and allowed IPs. `wg show` alone
	// only yields keys, which is why an endpoint that moved could never be
	// detected as drift.
	currentPeers, err := production.ReadLiveWGPeers("wg0")
	if err != nil {
		return fmt.Errorf("read live wireguard peers: %w", err)
	}

	desired, err := n.loadDesiredWireGuardPeers(ctx, localPubKey)
	if err != nil {
		return err
	}

	n.reconcileWireGuardPeers(currentPeers, desired)
	return nil
}

// wgPeerProvisioner is the subset of production.WireGuardProvisioner the
// reconciler needs. Declared here so reconcileWireGuardPeers can be exercised
// without shelling out to `wg` — the add/remove decisions are the part worth
// testing, and getting them wrong severs the cluster.
type wgPeerProvisioner interface {
	AddPeer(peer production.WireGuardPeer) error
	RemovePeer(publicKey string) error
	// PersistPeers writes the resulting peer set to wg0.conf so the mesh
	// survives the next `wg-quick up`. Kernel state and file state are applied
	// separately and reported separately: a peer that reached the interface is
	// live even if the file could not be written.
	PersistPeers(peers []production.WireGuardPeer) error
}

// reconcileWireGuardPeers applies desired onto the live interface.
//
// Removal is deliberately conservative. It runs ONLY when the desired set is
// authoritative AND non-empty, because "I read zero peers" and "the cluster has
// zero peers" are indistinguishable at this layer and the first is far more
// likely — a node that has just lost quorum, a replica mid-restore, or a
// migration in flight all produce an empty read. Treating that as "remove every
// peer" severs the mesh, and severing the mesh is what makes the loss
// unrecoverable. Adding peers is always safe, so adds are unconditional.
func (n *Node) reconcileWireGuardPeers(currentPeers map[string]production.WireGuardPeer, desired desiredWGPeers) {
	n.reconcileWireGuardPeersWith(production.NewWGPeerManager(""), currentPeers, desired)
}

// reconcileWireGuardPeersWith is reconcileWireGuardPeers against an injected
// provisioner.
func (n *Node) reconcileWireGuardPeersWith(wp wgPeerProvisioner, currentPeers map[string]production.WireGuardPeer, desired desiredWGPeers) {
	// live tracks what the interface holds as we change it, so the file we
	// persist at the end describes the mesh that actually exists.
	live := make(map[string]production.WireGuardPeer, len(currentPeers))
	for k, v := range currentPeers {
		live[k] = v
	}

	added, updated := 0, 0
	for pubKey, peer := range desired.peers {
		existing, exists := live[pubKey]
		if exists && !wgPeerDrifted(existing, peer) {
			continue
		}
		if err := wp.AddPeer(peer); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "failed to apply WG peer to the interface",
				zap.String("public_key", shortWGKey(pubKey)),
				zap.Error(err))
			continue
		}
		live[pubKey] = peer
		if exists {
			// `wg set` is idempotent, so re-applying a known key is how an
			// endpoint or allowed-ips change is rolled out. Skipping known keys
			// meant a peer that moved to a new public IP kept the dead endpoint
			// forever, which is the manual "reset the peer on both sides" in
			// docs/COMMON_PROBLEMS.md.
			updated++
			n.logger.ComponentInfo(logging.ComponentNode, "updated WG peer",
				zap.String("public_key", shortWGKey(pubKey)),
				zap.String("endpoint", peer.Endpoint),
				zap.String("allowed_ip", peer.AllowedIP))
			continue
		}
		added++
		n.logger.ComponentInfo(logging.ComponentNode, "added WG peer",
			zap.String("allowed_ip", peer.AllowedIP),
			zap.String("source", desired.source))
	}

	removed := 0
	canRemove := desired.authoritative && len(desired.peers) > 0
	if canRemove {
		for pubKey := range currentPeers {
			if _, exists := desired.peers[pubKey]; exists {
				continue
			}
			if err := wp.RemovePeer(pubKey); err != nil {
				n.logger.ComponentWarn(logging.ComponentNode, "failed to remove stale WG peer",
					zap.String("public_key", shortWGKey(pubKey)),
					zap.Error(err))
				continue
			}
			delete(live, pubKey)
			removed++
			n.logger.ComponentInfo(logging.ComponentNode, "removed stale WG peer",
				zap.String("public_key", shortWGKey(pubKey)))
		}
	} else if len(currentPeers) > 0 {
		n.logger.ComponentInfo(logging.ComponentNode,
			"skipping WG peer removal — membership is not authoritative or came back empty; keeping the live mesh intact",
			zap.Bool("authoritative", desired.authoritative),
			zap.Int("desired_peers", len(desired.peers)),
			zap.String("source", desired.source))
	}

	// Persist whatever the interface now holds. Reported separately from the
	// kernel result: a failure here means the mesh is correct now but will come
	// back stale after the next `wg-quick up`, which is a different problem from
	// a peer that never reached the interface at all.
	persisted := true
	if added+removed+updated > 0 {
		if err := wp.PersistPeers(mapToPeerSlice(live)); err != nil {
			persisted = false
			n.logger.ComponentError(logging.ComponentNode,
				"WG peers applied to the interface but NOT persisted — the mesh will regress on the next wg-quick up",
				zap.Error(err))
		}
	}

	n.logger.ComponentInfo(logging.ComponentNode, "WireGuard peer sync completed",
		zap.Int("desired_peers", len(desired.peers)),
		zap.Int("current_peers", len(currentPeers)),
		zap.Int("added", added),
		zap.Int("updated", updated),
		zap.Int("removed", removed),
		zap.Bool("persisted", persisted),
		zap.Bool("authoritative", desired.authoritative),
		zap.String("source", desired.source))
}

// wgPeerDrifted reports whether the live peer differs from what membership says
// it should be. AllowedIP is compared with the /32 suffix normalised away
// because `wg show dump` always prints a prefix length and the desired set may
// not.
func wgPeerDrifted(live, desired production.WireGuardPeer) bool {
	if desired.Endpoint != "" && live.Endpoint != desired.Endpoint {
		return true
	}
	return normalizeAllowedIP(live.AllowedIP) != normalizeAllowedIP(desired.AllowedIP)
}

func normalizeAllowedIP(v string) string {
	v = strings.TrimSpace(v)
	return strings.TrimSuffix(v, "/32")
}

// mapToPeerSlice flattens the live peer map for persistence.
func mapToPeerSlice(peers map[string]production.WireGuardPeer) []production.WireGuardPeer {
	out := make([]production.WireGuardPeer, 0, len(peers))
	for _, p := range peers {
		out = append(out, p)
	}
	return out
}

// shortWGKey truncates a WireGuard public key for logging. Full keys are not
// secret (they are public keys) but they are noise at 44 chars.
func shortWGKey(pubKey string) string {
	if len(pubKey) <= wgKeyLogPrefixLen {
		return pubKey
	}
	return pubKey[:wgKeyLogPrefixLen] + "..."
}

// ensureWireGuardSelfRegistered ensures this node's WireGuard info is in the
// wireguard_peers table. Without this, joining nodes get an empty peer list
// from the /v1/internal/join endpoint and can't establish WG tunnels.
func (n *Node) ensureWireGuardSelfRegistered(ctx context.Context) {
	if n.getRQLiteAdapter() == nil {
		return
	}

	// Check if wg0 is active
	out, err := exec.CommandContext(ctx, "wg", "show", "wg0").CombinedOutput()
	if err != nil {
		return // WG not active, nothing to register
	}

	// Get local public key
	localPubKey := parseWGShowLocalKey(string(out))
	if localPubKey == "" {
		return
	}

	// Get WG IP from interface
	wgIP := ""
	iface, err := net.InterfaceByName("wg0")
	if err != nil {
		return
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			wgIP = ipnet.IP.String()
			break
		}
	}
	if wgIP == "" {
		return
	}

	// Get public IP
	publicIP, err := n.getNodeIPAddress()
	if err != nil {
		return
	}

	nodeID := n.GetPeerID()
	if nodeID == "" {
		nodeID = fmt.Sprintf("node-%s", wgIP)
	}

	// Query local IPFS peer ID
	ipfsPeerID := queryLocalIPFSPeerID()

	db := n.getRQLiteAdapter().GetSQLDB()

	// Clean up stale entries for this public IP with a different node_id.
	// This prevents ghost peers from previous installs or from the temporary
	// "node-10.0.0.X" ID that the join handler creates.
	//
	// Scoped to unconfirmed rows. Two nodes can legitimately share a public IP
	// — anything behind one NAT — and without the scope each would delete the
	// other's mesh row every 60 seconds, indefinitely. A confirmed row belongs
	// to a machine that came up, and removing those is the membership
	// reconciler's job, under evidence this function does not have.
	if _, err := rqlite.SafeExecContext(db, ctx,
		"DELETE FROM wireguard_peers WHERE public_ip = ? AND node_id != ? AND confirmed_at IS NULL",
		publicIP, nodeID); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to clean stale WG entries", zap.Error(err))
	}

	// confirmed_at is set here, not left to the membership reconciler to infer
	// from dns_nodes. A node writing its own row from its own boot process is
	// the strongest evidence there is that it came up, and without it a running
	// node whose dns_nodes row was missing for any reason would have this row
	// garbage-collected as a failed join and be severed from the mesh.
	//
	// An upsert, not INSERT OR REPLACE. OR REPLACE deletes the conflicting row
	// and inserts a new one, so every column absent from the statement is
	// silently reset — this runs every 60 seconds, and it was quietly wiping
	// the operator_wallet the join handler wrote and resetting created_at on
	// every tick. Naming the columns to update leaves the rest alone.
	//
	// The upsert resolves a conflict on node_id — this node re-asserting its
	// own row — and nothing else. A row held by a DIFFERENT node at this
	// node's wg_ip or public key now fails loudly instead of being deleted:
	// silently taking another machine's row is the same class of bug the join
	// path was just fixed for, and the membership reconciler is what removes
	// rows whose node has genuinely departed.
	_, err = rqlite.SafeExecContext(db, ctx,
		`INSERT INTO wireguard_peers
		   (node_id, wg_ip, public_key, public_ip, wg_port, ipfs_peer_id, confirmed_at)
		 VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(node_id) DO UPDATE SET
		   wg_ip        = excluded.wg_ip,
		   public_key   = excluded.public_key,
		   public_ip    = excluded.public_ip,
		   wg_port      = excluded.wg_port,
		   ipfs_peer_id = excluded.ipfs_peer_id,
		   confirmed_at = COALESCE(wireguard_peers.confirmed_at, excluded.confirmed_at)`,
		nodeID, wgIP, localPubKey, publicIP, defaultWireGuardPort, ipfsPeerID)
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to self-register WG peer", zap.Error(err))
	} else {
		n.logger.ComponentInfo(logging.ComponentNode, "WireGuard self-registered",
			zap.String("wg_ip", wgIP),
			zap.String("public_key", shortWGKey(localPubKey)),
			zap.String("ipfs_peer_id", ipfsPeerID))
	}
}

// queryLocalIPFSPeerID queries the local IPFS daemon for its peer ID
func queryLocalIPFSPeerID() string {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(constants.LocalIPFSAPIURL()+"/api/v0/id", "", nil)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"ID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	return result.ID
}

// startWireGuardSync registers this node in wireguard_peers, reconciles the
// local interface once, and makes sure the periodic sync is running.
//
// The boot supervisor retries it after any failure, so the periodic loop starts
// once while the immediate sync runs on every attempt — which is what you want
// while the mesh is still being repaired: catch up now, then fall back to the
// normal cadence.
func (n *Node) startWireGuardSync(ctx context.Context) error {
	n.wgSyncOnce.Do(func() { go n.wireGuardSyncLoop(ctx) })

	// Ensure this node is registered in wireguard_peers (critical for join flow)
	n.ensureWireGuardSelfRegistered(ctx)

	if err := n.syncWireGuardPeers(ctx); err != nil {
		return fmt.Errorf("WireGuard peer sync failed: %w", err)
	}
	return nil
}

// wireGuardSyncLoop reconciles the local WireGuard interface every
// wgSyncInterval until ctx is done.
func (n *Node) wireGuardSyncLoop(ctx context.Context) {
	ticker := time.NewTicker(wgSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Re-register self on every tick to pick up IPFS peer ID if it wasn't
			// ready at startup (INSERT OR REPLACE is idempotent)
			n.ensureWireGuardSelfRegistered(ctx)
			if err := n.syncWireGuardPeers(ctx); err != nil {
				n.logger.ComponentWarn(logging.ComponentNode, "WireGuard peer sync failed", zap.Error(err))
			}
		}
	}
}

// parseWGShowLocalKey extracts the local public key from `wg show wg0` output
func parseWGShowLocalKey(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "public key:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "public key:"))
		}
	}
	return ""
}

// bootstrapWireGuardMesh repairs the local WireGuard interface during node
// startup, before rqlite blocks waiting for a raft leader.
//
// This is the recovery path for a node whose mesh membership was lost. Raft
// runs over the mesh, so such a node reaches no voter and its rqlite never
// elects a leader; the steady-state sync (startWireGuardSync) cannot run yet
// either, because it needs the adapter that rqlite start-up has not finished
// building. The result would be a node that cannot recover without hands,
// while the peer rows sit readable on its own disk.
//
// Running here breaks that cycle. It reads the local replica directly rather
// than going through the adapter, because the adapter is only constructed after
// rqlite is up — the very thing being waited on. Failures are logged and
// swallowed: this is best-effort repair on the startup path, and the periodic
// sync will retry once the node is running.
func (n *Node) bootstrapWireGuardMesh(ctx context.Context) {
	if _, err := exec.LookPath("wg"); err != nil {
		return // no WireGuard on this node
	}
	out, err := exec.CommandContext(ctx, "wg", "show", "wg0").CombinedOutput()
	if err != nil {
		return // wg0 not active
	}
	localPubKey := parseWGShowLocalKey(string(out))

	currentPeers, err := production.ReadLiveWGPeers("wg0")
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode,
			"WireGuard bootstrap: cannot read live peers — mesh repair skipped",
			zap.Error(err))
		return
	}

	db, err := n.openLocalRQLiteForBootstrap()
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode,
			"WireGuard bootstrap: cannot open local rqlite read handle — mesh repair skipped, node will rely on the periodic sync once rqlite is up",
			zap.Error(err))
		return
	}
	defer db.Close()

	bootCtx, cancel := context.WithTimeout(ctx, wgBootstrapTimeout)
	defer cancel()

	peers, err := scanWGPeers(bootCtx, db, localPubKey)
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode,
			"WireGuard bootstrap: local peer read failed — mesh repair skipped",
			zap.Error(err))
		return
	}

	missing := 0
	for k := range peers {
		if _, ok := currentPeers[k]; !ok {
			missing++
		}
	}
	if missing == 0 {
		return // mesh already matches what we know; stay quiet on the happy path
	}

	n.logger.ComponentInfo(logging.ComponentNode,
		"WireGuard bootstrap: repairing mesh before waiting for a raft leader",
		zap.Int("known_peers", len(peers)),
		zap.Int("live_peers", len(currentPeers)),
		zap.Int("missing", missing))

	// Additive only — a bootstrap read is never authoritative enough to remove.
	n.reconcileWireGuardPeers(currentPeers, desiredWGPeers{
		peers:         peers,
		authoritative: false,
		source:        "bootstrap-local-replica",
	})
}

// openLocalRQLiteForBootstrap opens a short-lived level=none handle to this
// node's own rqlite. Used only by bootstrapWireGuardMesh, which runs before the
// shared adapter exists. The caller owns and must Close the handle.
func (n *Node) openLocalRQLiteForBootstrap() (*sql.DB, error) {
	if n.config == nil {
		return nil, fmt.Errorf("node config unavailable")
	}
	port := n.config.Database.RQLitePort
	if port == 0 {
		return nil, fmt.Errorf("rqlite port not configured")
	}
	dsn := fmt.Sprintf("http://localhost:%d?disableClusterDiscovery=true&level=none", port)
	db, err := sql.Open("rqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open local rqlite handle on port %d: %w", port, err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	return db, nil
}
