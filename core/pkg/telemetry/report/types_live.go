package report

import "time"

// --- Chain ---

// ChainReport is this node's view of the Orama L1, read from the CometBFT RPC
// the chain unit serves on loopback. Nil when the node runs no chain.
type ChainReport struct {
	// ServiceActive is whether the chain's systemd unit is active.
	ServiceActive bool `json:"service_active"`
	// Responsive is whether the RPC answered every query the collector makes;
	// Error names the one that failed. Fields read before it are kept.
	Responsive bool   `json:"responsive"`
	Error      string `json:"error,omitempty"`

	ChainID         string    `json:"chain_id,omitempty"`
	NodeVersion     string    `json:"node_version,omitempty"`
	LatestHeight    int64     `json:"latest_height"`
	LatestBlockTime time.Time `json:"latest_block_time,omitempty"`
	// BlockAgeSec is how long ago the latest block was made, at collection.
	BlockAgeSec float64 `json:"block_age_sec"`
	// AvgBlockTimeSec is the mean interval over the last blocks.
	AvgBlockTimeSec float64 `json:"avg_block_time_sec"`
	CatchingUp      bool    `json:"catching_up"`
	Peers           int     `json:"peers"`
	MempoolTxs      int     `json:"mempool_txs"`

	// IsValidator is whether this node's key is in the active validator set.
	IsValidator bool  `json:"is_validator"`
	VotingPower int64 `json:"voting_power"`

	Validators       []ChainValidator `json:"validators,omitempty"`
	TotalVotingPower int64            `json:"total_voting_power"`
}

// ChainValidator is one member of the active validator set.
type ChainValidator struct {
	Address     string `json:"address"`
	VotingPower int64  `json:"voting_power"`
}

// --- Traffic ---

// TrafficReport is what a gateway served over a recent window.
type TrafficReport struct {
	// WindowSec is the span the rates and percentiles below cover.
	WindowSec int `json:"window_sec"`

	Requests    int64   `json:"requests"`
	RPS         float64 `json:"rps"`
	Errors5xx   int64   `json:"errors_5xx"`
	Errors4xx   int64   `json:"errors_4xx"`
	ErrorRate   float64 `json:"error_rate"`
	P50Ms       float64 `json:"p50_ms"`
	P95Ms       float64 `json:"p95_ms"`
	P99Ms       float64 `json:"p99_ms"`
	BytesPerSec float64 `json:"bytes_per_sec"`

	// TotalRequests counts every request since the gateway started.
	TotalRequests int64 `json:"total_requests"`

	// Namespaces is the busiest namespaces over the window, most requests first.
	Namespaces []NamespaceTraffic `json:"namespaces,omitempty"`
}

// NamespaceTraffic is one namespace's share of a TrafficReport window.
type NamespaceTraffic struct {
	Namespace string  `json:"namespace"`
	Requests  int64   `json:"requests"`
	RPS       float64 `json:"rps"`
	Errors5xx int64   `json:"errors_5xx"`
	P95Ms     float64 `json:"p95_ms"`
}

// Raft states an rqlite node reports. Leader and Follower are the settled
// ones: a node in either is a working member of the cluster.
const (
	RaftLeader    = "Leader"
	RaftFollower  = "Follower"
	RaftCandidate = "Candidate"
	RaftShutdown  = "Shutdown"
)

// Settled reports whether the node holds a settled raft role.
func (q *RQLiteReport) Settled() bool {
	return q != nil && (q.RaftState == RaftLeader || q.RaftState == RaftFollower)
}
