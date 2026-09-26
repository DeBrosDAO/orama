package rqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// The cluster membership record.
//
// rqlited decides between bootstrapping a new cluster and joining one by
// looking at its own raft state and its -join flags. Neither survives the
// failure that matters: a genesis node — the only node installed without a
// join address — that loses its rqlite data has no raft state and no -join,
// so rqlited bootstraps a second, empty cluster next to the live one and
// elects itself leader of it.
//
// The record is what tells "never been a member" apart from "lost its data".
// It is written the first time a node is seen holding raft state or reaching a
// leader, and it lives beside the rqlite directory rather than inside it, so
// the loss of the raft state does not take the evidence with it. It also
// carries the raft addresses of the cluster's members as this node last saw
// them: those are the addresses such a node has to join instead.
//
// Other signals were considered and rejected. bootstrap_peers and the join
// address are empty on exactly the node this guards — the genesis node. The
// enrolment and dns_nodes rows live in rqlite and are lost with it. The
// WireGuard peers are in root's /etc/wireguard, unreadable by this process,
// and the libp2p peers carry public addresses, not the overlay raft
// addresses a join has to name.

// ClusterMembershipFileName is the record's filename in the node's data
// directory (~/.orama/data), next to the rqlite directory.
const ClusterMembershipFileName = "cluster-membership.json"

// ClusterMembership records that this node has been part of a cluster.
type ClusterMembership struct {
	// FirstSeen is when the node was first recorded as a member.
	FirstSeen time.Time `json:"first_seen"`
	// Members are the raft addresses of the cluster's members, this node's
	// own included, as last observed. Empty when the record was written from
	// raft state alone, before the node reached a leader on this binary.
	Members []string `json:"members,omitempty"`
}

// ClusterMembershipPath is the record's path for the rqlite data directory
// rqliteDir: beside it, in its parent. Both the writer (RQLiteManager) and
// the reader (the index supervisor) derive it from the rqlite directory they
// use, so they agree whenever they are talking about the same raft state.
func ClusterMembershipPath(rqliteDir string) string {
	return filepath.Join(filepath.Dir(rqliteDir), ClusterMembershipFileName)
}

// ErrCorruptMembershipRecord is wrapped by ReadClusterMembership when the
// record exists but does not parse. A node that still holds raft state has its
// membership proven by that state and replaces such a record; one without
// state cannot tell whom to join and refuses.
var ErrCorruptMembershipRecord = errors.New("cluster membership record is corrupt")

// ReadClusterMembership returns the record at path, or nil when there is none.
func ReadClusterMembership(path string) (*ClusterMembership, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cluster membership record %s: %w", path, err)
	}
	var m ClusterMembership
	if err := json.Unmarshal(data, &m); err != nil {
		// An unreadable record is still evidence of membership, but not
		// evidence of whom to join; saying so beats guessing either way.
		return nil, fmt.Errorf("%w: %s (%v); it records that this node was a cluster member — "+
			"restore it, or see `orama node recover-raft`", ErrCorruptMembershipRecord, path, err)
	}
	return &m, nil
}

// WriteClusterMembership records m at path, atomically and durably: a torn
// record would read as corrupt and stop a node that lost its raft state from
// starting, and a record lost to a power cut is the evidence gone.
func WriteClusterMembership(path string, m ClusterMembership) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cluster membership record: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := writeFileDurable(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write cluster membership record: %w", err)
	}
	return nil
}

// RecordMembership records addrs as the cluster's members at path when they
// differ from what is recorded (see MergeMembership), reporting whether it
// wrote. For the namespace manager, which records each tenant rqlite's
// configuration beside its data directory.
func RecordMembership(path string, addrs []string, now time.Time) (bool, error) {
	return updateClusterMembership(path, addrs, now)
}

// ValidateRaftAddress checks that addr is ip:port — the only shape a raft
// member address has, and the only one that may reach a -join flag in a unit
// env file.
func ValidateRaftAddress(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("raft address %q is not host:port: %w", addr, err)
	}
	if net.ParseIP(host) == nil {
		return fmt.Errorf("raft address %q does not have an IP host", addr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("raft address %q does not have a port", addr)
	}
	return nil
}

// updateClusterMembership records the member addresses at path when they
// differ from what is recorded, keeping the first-seen time. It reports
// whether it wrote.
func updateClusterMembership(path string, addrs []string, now time.Time) (bool, error) {
	existing, err := ReadClusterMembership(path)
	if err != nil {
		return false, err
	}
	record, changed, err := MergeMembership(existing, addrs, now)
	if err != nil || !changed {
		return false, err
	}
	if err := WriteClusterMembership(path, record); err != nil {
		return false, err
	}
	return true, nil
}

// MergeMembership is the record that names addrs as the members, keeping
// existing's first-seen time, and whether it differs from existing. Each
// address is validated; an empty set is refused.
func MergeMembership(existing *ClusterMembership, addrs []string, now time.Time) (ClusterMembership, bool, error) {
	members := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if addr == "" || slices.Contains(members, addr) {
			continue
		}
		if err := ValidateRaftAddress(addr); err != nil {
			return ClusterMembership{}, false, fmt.Errorf("the raft configuration holds a member this record cannot name: %w", err)
		}
		members = append(members, addr)
	}
	if len(members) == 0 {
		return ClusterMembership{}, false, fmt.Errorf("rqlite reported no member addresses; not recording an empty cluster")
	}
	slices.Sort(members)

	record := ClusterMembership{FirstSeen: now.UTC(), Members: members}
	if existing != nil {
		if slices.Equal(existing.Members, members) {
			return *existing, false, nil
		}
		record.FirstSeen = existing.FirstSeen
	}
	return record, true, nil
}

// ClusterMembershipPath is where this node's membership record lives.
func (r *RQLiteManager) ClusterMembershipPath() (string, error) {
	rqliteDir, err := r.rqliteDataDirPath()
	if err != nil {
		return "", err
	}
	return ClusterMembershipPath(rqliteDir), nil
}

// RecordClusterMembership records the cluster this node is a member of, from
// the raft configuration its local rqlited holds. It reads /status, which
// answers locally: /nodes probes every member and stalls on any that is down.
//
// It also records the address the configuration holds this node at
// (raftAddrMarker). That is the confirmation an address change waits for: a
// node restarted on a new address keeps passing -join until the leader has
// re-registered it, and this is where that becomes visible.
func (r *RQLiteManager) RecordClusterMembership(ctx context.Context) error {
	admin, err := r.LocalAdminClient()
	if err != nil {
		return fmt.Errorf("record cluster membership: %w", err)
	}
	status, err := admin.Status(ctx)
	if err != nil {
		return fmt.Errorf("record cluster membership: read /status: %w", err)
	}
	rqliteDir, err := r.rqliteDataDirPath()
	if err != nil {
		return fmt.Errorf("record cluster membership: %w", err)
	}
	live, err := LiveIdentityFromStatus(status)
	if err != nil {
		return fmt.Errorf("record cluster membership: %w", err)
	}
	path := ClusterMembershipPath(rqliteDir)
	wrote, err := updateClusterMembership(path, live.Members, time.Now())
	if err != nil {
		return fmt.Errorf("record cluster membership in %s: %w", path, err)
	}
	if wrote {
		r.logger.Info("Recorded cluster membership", zap.String("path", path), zap.Int("members", len(live.Members)))
	}
	return recordConfirmedAddr(rqliteDir, live, r.logger)
}

// recordConfirmedAddr records the address the configuration holds this node
// at, when it differs from the one recorded.
func recordConfirmedAddr(rqliteDir string, live LiveIdentity, logger *zap.Logger) error {
	recorded, err := ReadRaftAddrMarker(rqliteDir)
	if err != nil {
		return fmt.Errorf("record the confirmed raft address: %w", err)
	}
	suffrage, err := readMarker(rqliteDir, raftSuffrageMarker)
	if err != nil {
		return fmt.Errorf("record the confirmed suffrage: %w", err)
	}
	if suffrage != live.Suffrage {
		if err := WriteRaftSuffrageMarker(rqliteDir, live.Suffrage); err != nil {
			return fmt.Errorf("record the confirmed suffrage: %w", err)
		}
	}
	if recorded == live.Addr {
		return nil
	}
	if err := WriteRaftAddrMarker(rqliteDir, live.Addr); err != nil {
		return fmt.Errorf("record the confirmed raft address: %w", err)
	}
	logger.Info("Recorded the raft address this node is a member at",
		zap.String("previous", recorded), zap.String("addr", live.Addr))
	return nil
}

// HasRecoveryPeers reports whether rqliteDataDir holds a recovery peers.json:
// the operator's instruction (orama node recover-raft) to reform the cluster
// from this node's data. rqlited consumes it at start, so such a node neither
// bootstraps nor joins.
func HasRecoveryPeers(rqliteDataDir string) (bool, error) {
	path := filepath.Join(rqliteDataDir, "raft", "peers.json")
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	return true, nil
}
