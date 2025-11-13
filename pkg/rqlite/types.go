package rqlite

import "time"

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
			Term              uint64 `json:"term"`
			NumPeers          int    `json:"num_peers"`
			Voter             bool   `json:"voter"`
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
