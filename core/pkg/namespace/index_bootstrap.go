package namespace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// indexStart is what the local index raft state and membership record say
// about how rqlited@index must start.
type indexStart struct {
	// hasState: the node holds raft state and restarts into the cluster it has.
	hasState bool
	// recovering: a recovery peers.json is waiting; rqlited reforms from it.
	recovering bool
	// joinAddress is the rqlite_join_address from node.yaml, if any.
	joinAddress string
	// selfRaftAddr is this node's raft advertise address.
	selfRaftAddr string
	// previousAddr is the raft address this node was last confirmed a member
	// at (rqlite.RaftIdentity.PreviousAddr), "" when none is recorded.
	previousAddr string
	// record is the cluster membership record, nil when there is none.
	record *rqlite.ClusterMembership
	// recordPath is where the record lives, for the operator.
	recordPath string
}

// ClusterMembershipPath is where the membership record for the core raft
// state lives; RQLiteManager.ClusterMembershipPath derives the same file from
// the same directory.
func (s *IndexSupervisor) ClusterMembershipPath() string {
	return rqlite.ClusterMembershipPath(s.CoreRQLiteDir())
}

// readIndexStart reads what the node holds. A node found holding raft state
// without a membership record — every node's first boot on this binary — is
// recorded as a member there and then, so the evidence exists before it can
// be lost.
//
// A record that does not parse is evidence of membership but not of whom to
// join. That matters only to a node that lost its raft state, which refuses
// (indexJoinTargets). A node that still holds raft state is a member by that
// state alone: it replaces the torn record rather than being kept down by it,
// and the membership recorder refills the members once rqlited answers.
func (s *IndexSupervisor) readIndexStart(ctx context.Context, rqliteDir, httpAdv, raftAdv, joinAddress string) (indexStart, error) {
	hasState, err := s.raftState(ctx, rqliteDir, httpAdv, filepath.Join(rqliteDir, rqlite.AuthFileName))
	if err != nil {
		return indexStart{}, fmt.Errorf("decide whether @index rqlite joins %q: %w", joinAddress, err)
	}
	recovering, err := rqlite.HasRecoveryPeers(rqliteDir)
	if err != nil {
		return indexStart{}, fmt.Errorf("decide whether @index rqlite joins: %w", err)
	}
	recordPath := s.ClusterMembershipPath()
	record, err := rqlite.ReadClusterMembership(recordPath)
	switch {
	case err == nil:
	case hasState && errors.Is(err, rqlite.ErrCorruptMembershipRecord):
		s.logger.Warn("Replacing a cluster membership record that does not parse; this node's raft state proves its membership",
			zap.String("membership_record", recordPath), zap.Error(err))
		record = nil
	default:
		return indexStart{}, err
	}
	if hasState && record == nil {
		if err := rqlite.WriteClusterMembership(recordPath, rqlite.ClusterMembership{FirstSeen: time.Now().UTC()}); err != nil {
			return indexStart{}, fmt.Errorf("record this node's cluster membership: %w", err)
		}
	}
	return indexStart{
		hasState:     hasState,
		recovering:   recovering,
		joinAddress:  joinAddress,
		selfRaftAddr: raftAdv,
		record:       record,
		recordPath:   recordPath,
	}, nil
}

// indexJoinTargets decides which addresses rqlited@index is started with
// -join, and refuses the one start that would split the cluster.
//
// Bootstrapping a new cluster — no raft state and no -join — is allowed only
// on a node that has never been a member: no membership record. A node with a
// record and no raft state lost its data; it joins the members it recorded
// (and its configured join address), and when it has none to join it refuses
// to start rather than elect itself leader of an empty registry.
//
// A member restarts into its configuration without -join, unless that
// configuration holds it at another address (previousAddr): then it joins the
// other members, which is how the leader re-registers it at the address it
// listens on now. Without one to join it refuses: restarted without -join it
// would sit in a configuration that routes every message for it to an address
// nothing answers.
func indexJoinTargets(st indexStart) ([]string, error) {
	if st.recovering {
		// A pending recovery peers.json is the operator reforming the cluster
		// from this node's data, which a -join would contradict.
		return nil, nil
	}
	if st.hasState {
		if !(rqlite.RaftIdentity{PreviousAddr: st.previousAddr}).AddressChanged(st.selfRaftAddr) {
			// A member restarts into the cluster it has; passing -join would
			// make every restart depend on that one address answering.
			return nil, nil
		}
		return rejoinTargets(st)
	}

	var targets []string
	if st.joinAddress != "" {
		targets = append(targets, st.joinAddress)
	}
	if st.record == nil {
		// Never a member: a fresh joiner joins its address, and a fresh
		// genesis install (no address) bootstraps.
		return targets, nil
	}
	targets, err := appendRecordedMembers(targets, st)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, lostDataWithNoPeersError(st)
	}
	return targets, nil
}

// rejoinTargets are the members a node whose address changed joins: every
// recorded member but itself, under either address, then its configured join
// address.
func rejoinTargets(st indexStart) ([]string, error) {
	var members []string
	if st.record != nil {
		var err error
		if members, err = appendRecordedMembers(nil, st); err != nil {
			return nil, err
		}
	}
	targets := make([]string, 0, len(members)+1)
	for _, m := range members {
		if m != st.previousAddr {
			targets = append(targets, m)
		}
	}
	if j := st.joinAddress; j != "" && j != st.selfRaftAddr && j != st.previousAddr && !slices.Contains(targets, j) {
		targets = append(targets, j)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("refusing to start the index rqlite: the raft configuration holds this node at %s, "+
			"it now listens on %s, and there is no other member to join so that the leader re-registers it "+
			"(membership record %s names none, and node.yaml has no rqlite_join_address). "+
			"On a cluster of one, reform it at the new address with "+
			"`orama node recover-raft --env <env> --leader-raft-addr %s`",
			st.previousAddr, st.selfRaftAddr, st.recordPath, st.selfRaftAddr)
	}
	return targets, nil
}

// appendRecordedMembers appends the record's members other than this node to
// targets, validating each.
func appendRecordedMembers(targets []string, st indexStart) ([]string, error) {
	for _, m := range st.record.Members {
		// The record is a file on disk and its members end up in the -join
		// of a unit env file, so each is checked for the one shape it has.
		if err := rqlite.ValidateRaftAddress(m); err != nil {
			return nil, fmt.Errorf("cluster membership record %s is corrupt: %w", st.recordPath, err)
		}
		if m != st.selfRaftAddr && !slices.Contains(targets, m) {
			targets = append(targets, m)
		}
	}
	return targets, nil
}

// lostDataWithNoPeersError is the refusal: it says what was found and the two
// ways forward, one of which must be chosen by a person.
func lostDataWithNoPeersError(st indexStart) error {
	members := "none recorded"
	if len(st.record.Members) > 0 {
		members = strings.Join(st.record.Members, ", ")
	}
	return fmt.Errorf("refusing to bootstrap a new index rqlite cluster: this node has been a cluster member "+
		"(recorded in %s since %s; members: %s) but has no raft state and no other member to join. "+
		"Starting anyway would create a second, empty cluster alongside the live one. "+
		"If the cluster is still running elsewhere, reform it with `orama node recover-raft --env <env>` from your machine, "+
		"which re-joins this node from the node with the most data. "+
		"If this node was the only member and its data is gone for good, delete %s to bootstrap a new, empty cluster deliberately",
		st.recordPath, st.record.FirstSeen.UTC().Format(time.RFC3339), members, st.recordPath)
}
