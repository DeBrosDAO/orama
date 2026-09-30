package rqlite

import (
	"fmt"
	"strings"
	"time"
)

// Raft timing for every rqlited the platform runs: the index and each
// namespace's. rqlite's own defaults (a one-second heartbeat and election
// timeout) are sized for a LAN; over the WireGuard overlay, with a node whose
// CPU the hypervisor takes away for seconds at a time, they mistake a slow
// heartbeat for a dead leader. The namespace instances used to start with those
// defaults while the index ran with these, and on stagenet the namespace
// clusters held an election every few seconds (hundreds an hour against three
// for the index on the same machines): every write in the gap failed with "not
// leader".
const (
	DefaultRaftElectionTimeout    = 5 * time.Second
	DefaultRaftHeartbeatTimeout   = 2 * time.Second
	DefaultRaftApplyTimeout       = 30 * time.Second
	DefaultRaftLeaderLeaseTimeout = 2 * time.Second
)

// RaftTimeouts are the Raft timing flags of one rqlited.
type RaftTimeouts struct {
	Election    time.Duration
	Heartbeat   time.Duration
	Apply       time.Duration
	LeaderLease time.Duration
}

// DefaultRaftTimeouts is the platform's Raft timing.
func DefaultRaftTimeouts() RaftTimeouts {
	return RaftTimeouts{
		Election:    DefaultRaftElectionTimeout,
		Heartbeat:   DefaultRaftHeartbeatTimeout,
		Apply:       DefaultRaftApplyTimeout,
		LeaderLease: DefaultRaftLeaderLeaseTimeout,
	}
}

// flags pairs each rqlited flag with its value.
func (t RaftTimeouts) flags() [][2]string {
	return [][2]string{
		{"-raft-election-timeout", t.Election.String()},
		{"-raft-heartbeat-timeout", t.Heartbeat.String()},
		{"-raft-apply-timeout", t.Apply.String()},
		{"-raft-leader-lease-timeout", t.LeaderLease.String()},
	}
}

// Args renders t as rqlited flags.
func (t RaftTimeouts) Args() string {
	parts := make([]string, 0, 4)
	for _, f := range t.flags() {
		parts = append(parts, fmt.Sprintf("%s %s", f[0], f[1]))
	}
	return strings.Join(parts, " ")
}

// WithDefaultRaftTimeouts returns extraArgs with each platform Raft timing flag
// it does not already set appended. A flag the caller set (the index's, from
// the node config) is kept as given.
func WithDefaultRaftTimeouts(extraArgs string) string {
	present := map[string]bool{}
	for _, field := range strings.Fields(extraArgs) {
		name, _, _ := strings.Cut(field, "=")
		present[name] = true
	}
	out := strings.TrimSpace(extraArgs)
	for _, f := range DefaultRaftTimeouts().flags() {
		if present[f[0]] {
			continue
		}
		out = strings.TrimSpace(out + " " + f[0] + " " + f[1])
	}
	return out
}
