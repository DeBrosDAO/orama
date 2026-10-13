// Package chainsoak is the analysis of the chain soak (features/chain-soak): the timeline of samples a
// long run takes of the validators, and the checks that decide whether the chain held. It has no
// dependency on the fleet, so the rules are unit tested here and the feature only gathers samples.
package chainsoak

import "time"

// Service is one daemon beside a validator, as one sample read it.
type Service struct {
	// AnonMB is the cgroup's anonymous memory in MiB: what the daemon allocated, without page cache.
	AnonMB int `json:"anon_mb"`
	// Restarts is systemd's NRestarts of the unit.
	Restarts int `json:"restarts"`
	// DataBytes is the size of the daemon's state directory on disk.
	DataBytes int64 `json:"data_bytes"`
}

// Node is one validator at one sample.
type Node struct {
	Name       string `json:"name"`
	Height     int64  `json:"height"`
	CatchingUp bool   `json:"catching_up"`
	// BlockHash and AppHash are those of the block at the sample's AgreedHeight, as this node stores it.
	BlockHash  string `json:"block_hash"`
	AppHash    string `json:"app_hash"`
	MempoolTxs int    `json:"mempool_txs"`
	// IndexerCursor is the last height the node's indexer has indexed; IndexerLag is Height less that.
	IndexerCursor int64              `json:"indexer_cursor"`
	IndexerLag    int64              `json:"indexer_lag_blocks"`
	Services      map[string]Service `json:"services"`
}

// Sample is one look at every validator.
type Sample struct {
	At      time.Time     `json:"at"`
	Elapsed time.Duration `json:"elapsed"`
	// AgreedHeight is the height every node had committed when the sample was taken: the block whose
	// hashes the nodes are compared at.
	AgreedHeight int64  `json:"agreed_height"`
	Nodes        []Node `json:"nodes"`
	// Sent is the number of transfers the load has broadcast so far.
	Sent int `json:"transfers_sent"`
}

// Timeline is the whole run.
type Timeline struct {
	Started  time.Time     `json:"started"`
	Duration time.Duration `json:"planned_duration"`
	Interval time.Duration `json:"sample_interval"`
	Samples  []Sample      `json:"samples"`
}

// Kind names a failure the analysis finds.
type Kind string

// The failures. Divergence and Stall are fatal at once; the others need a series.
const (
	Divergence   Kind = "divergence"
	Stall        Kind = "stall"
	NodeBehind   Kind = "node-behind"
	MemoryGrowth Kind = "memory-growth"
	Restart      Kind = "restart"
	IndexerLag   Kind = "indexer-lag"
	MempoolStuck Kind = "mempool-stuck"
	NoSamples    Kind = "no-samples"
)

// Finding is one thing the analysis holds against the run.
type Finding struct {
	Kind   Kind   `json:"kind"`
	Node   string `json:"node,omitempty"`
	Detail string `json:"detail"`
}

// Fatal reports whether the run should stop at once on this finding: the chain is already broken, and
// the rest of a 24-hour soak would only watch it stay broken.
func (f Finding) Fatal() bool { return f.Kind == Divergence || f.Kind == Stall || f.Kind == NoSamples }
