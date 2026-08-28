package rqlite

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RQLiteStatus represents the response from RQLite's /status endpoint
type RQLiteStatus struct {
	Store struct {
		Raft struct {
			AppliedIndex      uint64 `json:"applied_index"`
			CommitIndex       uint64 `json:"commit_index"`
			LastLogIndex      uint64 `json:"last_log_index"`
			LastSnapshotIndex uint64 `json:"last_snapshot_index"`
			State             string `json:"state"`
			LeaderID          string `json:"leader_id"`
			LeaderAddr        string `json:"leader_addr"`
			// LastContact is the time since this follower last heard from the
			// leader, formatted by rqlite as a Go duration string ("2.5ms",
			// "1.2s") or the literal "never" before first contact. Empty/absent
			// on the leader's own /status. Used by the local-follower freshness
			// gate (freshness.go) to decide whether a none-read is safe.
			LastContact LastContactValue `json:"last_contact"`
			Term        uint64           `json:"term"`
			NumPeers    int              `json:"num_peers"`
			Voter       bool             `json:"voter"`
		} `json:"raft"`
		DBConf struct {
			DSN    string `json:"dsn"`
			Memory bool   `json:"memory"`
		} `json:"db_conf"`
	} `json:"store"`
	Runtime struct {
		GOARCH       string `json:"GOARCH"`
		GOOS         string `json:"GOOS"`
		GOMAXPROCS   int    `json:"GOMAXPROCS"`
		NumCPU       int    `json:"num_cpu"`
		NumGoroutine int    `json:"num_goroutine"`
		Version      string `json:"version"`
	} `json:"runtime"`
	HTTP struct {
		Addr string `json:"addr"`
		Auth string `json:"auth"`
	} `json:"http"`
	Node struct {
		Uptime    string `json:"uptime"`
		StartTime string `json:"start_time"`
	} `json:"node"`
}

// RQLiteNode represents a node in the RQLite cluster
type RQLiteNode struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	Leader    bool   `json:"leader"`
	Voter     bool   `json:"voter"`
	Reachable bool   `json:"reachable"`
}

// RQLiteNodes represents the response from RQLite's /nodes endpoint
type RQLiteNodes []RQLiteNode

// PeerHealth tracks the health status of a peer
type PeerHealth struct {
	LastSeen       time.Time
	LastSuccessful time.Time
	FailureCount   int
	Status         string // "active", "degraded", "inactive"
}

// ClusterMetrics contains cluster-wide metrics
type ClusterMetrics struct {
	ClusterSize       int
	ActiveNodes       int
	InactiveNodes     int
	RemovedNodes      int
	LastUpdate        time.Time
	DiscoveryStatus   string
	CurrentLeader     string
	AveragePeerHealth float64
}

// LastContactValue decodes rqlite's `store.raft.last_contact`, whose JSON type
// depends on the node's raft role:
//
//	follower: "last_contact": "30.204191ms"   (duration string, or "never")
//	leader:   "last_contact": 0               (number)
//
// Declaring it as a plain string made json.Unmarshal fail on the LEADER's
// /status with "cannot unmarshal number into ... of type string". That error
// aborted the whole RQLiteStatus decode, so GetRaftStatus returned an error,
// so the freshness gate fail-safed to "not fresh" — silently disabling the
// local (level=none) read path on the one node where a local read is always
// authoritative. The gate's own `state == "Leader" → always fresh` fast path
// was unreachable in production because decoding never got that far.
//
// Accepting both shapes keeps the gate working for either role.
type LastContactValue string

// UnmarshalJSON accepts a JSON string, a JSON number, or null.
func (v *LastContactValue) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*v = ""
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("last_contact string: %w", err)
		}
		*v = LastContactValue(s)
		return nil
	}
	// Numeric form: rqlite emits nanoseconds (0 on the leader). Render it as a
	// duration string so downstream has a single representation to parse.
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("last_contact number: %w", err)
	}
	ns, err := n.Int64()
	if err != nil {
		f, ferr := n.Float64()
		if ferr != nil {
			return fmt.Errorf("last_contact numeric value %q: %w", n.String(), ferr)
		}
		ns = int64(f)
	}
	*v = LastContactValue(time.Duration(ns).String())
	return nil
}

// String returns the duration text for logging and parsing.
func (v LastContactValue) String() string { return string(v) }
