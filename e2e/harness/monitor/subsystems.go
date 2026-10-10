package monitor

import "time"

// System is the host.
type System struct {
	UptimeSeconds int64  `json:"uptime_seconds"`
	CPUCount      int    `json:"cpu_count"`
	MemTotalMB    int    `json:"mem_total_mb"`
	MemUsedMB     int    `json:"mem_used_mb"`
	DiskUsePct    int    `json:"disk_use_pct"`
	OOMKills      int    `json:"oom_kills"`
	KernelVersion string `json:"kernel_version"`
	TimeUnix      int64  `json:"time_unix"`
}

// Services are the node's systemd units.
type Services struct {
	Services    []Service `json:"services"`
	FailedUnits []string  `json:"failed_units,omitempty"`
}

// Service is one unit.
type Service struct {
	Name            string `json:"name"`
	ActiveState     string `json:"active_state"`
	SubState        string `json:"sub_state"`
	Enabled         bool   `json:"enabled"`
	NRestarts       int    `json:"n_restarts"`
	RestartLoopRisk bool   `json:"restart_loop_risk"`
}

// RQLite is the node's rqlite.
type RQLite struct {
	Responsive bool   `json:"responsive"`
	Ready      bool   `json:"ready"`
	RaftState  string `json:"raft_state,omitempty"`
	LeaderAddr string `json:"leader_addr,omitempty"`
	LeaderID   string `json:"leader_id,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
	Term       uint64 `json:"term,omitempty"`
	// LastSnapshotTerm is the term of the node's latest raft snapshot.
	LastSnapshotTerm uint64 `json:"last_snapshot_term,omitempty"`
	Applied          uint64 `json:"applied_index,omitempty"`
	Commit           uint64 `json:"commit_index,omitempty"`
	NumPeers         int    `json:"num_peers,omitempty"`
	Voter            bool   `json:"voter,omitempty"`
	Error            string `json:"error,omitempty"`
}

// Gateway is the node's gateway.
type Gateway struct {
	Responsive bool   `json:"responsive"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Version    string `json:"version,omitempty"`
}

// WireGuard is the node's overlay interface.
type WireGuard struct {
	InterfaceUp   bool     `json:"interface_up"`
	ServiceActive bool     `json:"service_active"`
	WgIP          string   `json:"wg_ip,omitempty"`
	PeerCount     int      `json:"peer_count"`
	Peers         []WGPeer `json:"peers,omitempty"`
}

// WGPeer is one WireGuard peer (the public key only; never a private one).
type WGPeer struct {
	PublicKey       string `json:"public_key"`
	Endpoint        string `json:"endpoint,omitempty"`
	AllowedIPs      string `json:"allowed_ips"`
	LatestHandshake int64  `json:"latest_handshake"`
	HandshakeAgeSec int64  `json:"handshake_age_sec"`
}

// DNS is the node's nameserver and TLS front.
type DNS struct {
	CoreDNSActive    bool `json:"coredns_active"`
	CaddyActive      bool `json:"caddy_active"`
	SOAResolves      bool `json:"soa_resolves"`
	NSResolves       bool `json:"ns_resolves"`
	WildcardResolves bool `json:"wildcard_resolves"`
	BaseAResolves    bool `json:"base_a_resolves"`
	BaseTLSDaysLeft  int  `json:"base_tls_days_left"`
	WildTLSDaysLeft  int  `json:"wild_tls_days_left"`
}

// Network is the node's routes, listeners and firewall.
type Network struct {
	InternetReachable bool       `json:"internet_reachable"`
	DefaultRoute      bool       `json:"default_route"`
	WGRouteExists     bool       `json:"wg_route_exists"`
	ListeningPorts    []PortInfo `json:"listening_ports"`
	UFWActive         bool       `json:"ufw_active"`
	UFWRules          []string   `json:"ufw_rules,omitempty"`
}

// PortInfo is one listening port.
type PortInfo struct {
	Port    int    `json:"port"`
	Proto   string `json:"proto"`
	Process string `json:"process,omitempty"`
}

// Chain is the node's co-hosted chain.
type Chain struct {
	ServiceActive   bool      `json:"service_active"`
	Responsive      bool      `json:"responsive"`
	Error           string    `json:"error,omitempty"`
	ChainID         string    `json:"chain_id,omitempty"`
	NodeVersion     string    `json:"node_version,omitempty"`
	LatestHeight    int64     `json:"latest_height"`
	LatestBlockTime time.Time `json:"latest_block_time,omitempty"`
	BlockAgeSec     float64   `json:"block_age_sec"`
	CatchingUp      bool      `json:"catching_up"`
	Peers           int       `json:"peers"`
	IsValidator     bool      `json:"is_validator"`
	VotingPower     int64     `json:"voting_power"`
}
