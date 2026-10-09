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
	// ValidatorCount is the validator set's reported size. It can be larger
	// than len(Validators) when the collector kept a page and not the whole set.
	ValidatorCount int `json:"validator_count,omitempty"`

	// UnitState is systemd's is-active answer: active, inactive, failed.
	UnitState string `json:"unit_state,omitempty"`
	// ConsAddress is this node's consensus address, 40 upper-case hex characters,
	// when /status included one of that shape.
	ConsAddress string `json:"cons_address,omitempty"`

	// MissedBlockRatio is missed_blocks_counter / signed_blocks_window for this
	// validator. MinSignedPerWindow is the slashing param beside it. Both are
	// nil when this node is not in the validator set or the query did not answer.
	MissedBlockRatio   *float64 `json:"missed_block_ratio,omitempty"`
	MinSignedPerWindow *float64 `json:"min_signed_per_window,omitempty"`
	// Jailed is staking's flag. Tombstoned is slashing's flag. Nil when the
	// query did not find this validator.
	Jailed     *bool `json:"jailed,omitempty"`
	Tombstoned *bool `json:"tombstoned,omitempty"`
	// SigningError is set when the slashing or staking query failed. The RPC
	// section stays responsive: these queries are extra.
	SigningError string `json:"signing_error,omitempty"`
}

// GlobalReport is the global-node services installed on this machine.
// Nil when none of the public Kubo, provider, or relay units are installed.
type GlobalReport struct {
	Units      []GlobalUnit      `json:"units,omitempty"`
	PublicIPFS *PublicIPFSReport `json:"public_ipfs,omitempty"`
	Provider   *ProviderReport   `json:"provider,omitempty"`
	Relay      *RelayReport      `json:"relay,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// GlobalUnit is one installed orama-global unit.
type GlobalUnit struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// PublicIPFSReport is the public Kubo's repo, read from its loopback RPC.
type PublicIPFSReport struct {
	RepoBytes       int64  `json:"repo_bytes"`
	StorageMaxBytes int64  `json:"storage_max_bytes"`
	Error           string `json:"error,omitempty"`
}

// ProviderReport is the storage provider's own status file, when that file exists.
// Hot-key balance, proof misses, disk and the deal slot counts are nil when
// the file omits them. HeldSlots is the deal slots whose piece the provider
// stores; PendingSlots is the slots assigned to it that still wait for a piece.
type ProviderReport struct {
	HotKeyBalanceNorama *int64 `json:"hot_key_balance_norama,omitempty"`
	ProofMisses         *int   `json:"proof_misses,omitempty"`
	DiskBytes           *int64 `json:"disk_bytes,omitempty"`
	StorageMaxBytes     *int64 `json:"storage_max_bytes,omitempty"`
	HeldSlots           *int   `json:"held_slots,omitempty"`
	PendingSlots        *int   `json:"pending_slots,omitempty"`
	Error               string `json:"error,omitempty"`
}

// RelayReport is the Tor relay's own status file (orama-global-tor-relay.service).
// InConsensus is whether the consensus the relay holds lists it, and nil when the
// file omits it because the relay has no valid consensus to say.
type RelayReport struct {
	InConsensus *bool  `json:"in_consensus,omitempty"`
	Error       string `json:"error,omitempty"`
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
