package report

import "time"

// NodeReport is the top-level JSON output of `orama node report --json`.
type NodeReport struct {
	Timestamp time.Time `json:"timestamp"`
	Hostname  string    `json:"hostname"`
	PublicIP  string    `json:"public_ip,omitempty"`
	WGIP      string    `json:"wireguard_ip,omitempty"`
	Version   string    `json:"version"`
	CollectMS int64     `json:"collect_ms"`
	Errors    []string  `json:"errors,omitempty"`

	System      *SystemReport      `json:"system"`
	Services    *ServicesReport    `json:"services"`
	RQLite      *RQLiteReport      `json:"rqlite,omitempty"`
	Olric       *OlricReport       `json:"olric,omitempty"`
	IPFS        *IPFSReport        `json:"ipfs,omitempty"`
	Vault       *VaultReport       `json:"vault,omitempty"`
	Gateway     *GatewayReport     `json:"gateway,omitempty"`
	WireGuard   *WireGuardReport   `json:"wireguard,omitempty"`
	DNS         *DNSReport         `json:"dns,omitempty"`
	Anyone      *AnyoneReport      `json:"anyone,omitempty"`
	Network     *NetworkReport     `json:"network"`
	Processes   *ProcessReport     `json:"processes"`
	Namespaces  []NamespaceReport  `json:"namespaces,omitempty"`
	Deployments *DeploymentsReport `json:"deployments,omitempty"`
	Serverless  *ServerlessReport  `json:"serverless,omitempty"`
}

// --- System ---

type SystemReport struct {
	UptimeSeconds int64   `json:"uptime_seconds"`
	UptimeSince   string  `json:"uptime_since"`
	CPUCount      int     `json:"cpu_count"`
	LoadAvg1      float64 `json:"load_avg_1"`
	LoadAvg5      float64 `json:"load_avg_5"`
	LoadAvg15     float64 `json:"load_avg_15"`
	MemTotalMB    int     `json:"mem_total_mb"`
	MemUsedMB     int     `json:"mem_used_mb"`
	MemFreeMB     int     `json:"mem_free_mb"`
	MemAvailMB    int     `json:"mem_available_mb"`
	MemUsePct     int     `json:"mem_use_pct"`
	SwapTotalMB   int     `json:"swap_total_mb"`
	SwapUsedMB    int     `json:"swap_used_mb"`
	DiskTotalGB   string  `json:"disk_total_gb"`
	DiskUsedGB    string  `json:"disk_used_gb"`
	DiskAvailGB   string  `json:"disk_avail_gb"`
	DiskUsePct    int     `json:"disk_use_pct"`
	InodePct      int     `json:"inode_use_pct"`
	OOMKills      int     `json:"oom_kills"`
	KernelVersion string  `json:"kernel_version"`
	TimeUnix      int64   `json:"time_unix"`
}

// --- Systemd Services ---

type ServicesReport struct {
	Services    []ServiceInfo `json:"services"`
	FailedUnits []string      `json:"failed_units,omitempty"`
}

type ServiceInfo struct {
	Name            string `json:"name"`
	ActiveState     string `json:"active_state"`
	SubState        string `json:"sub_state"`
	Enabled         bool   `json:"enabled"`
	NRestarts       int    `json:"n_restarts"`
	ActiveSinceSec  int64  `json:"active_since_sec"`
	MemoryCurrentMB int    `json:"memory_current_mb"`
	CPUUsageNSec    int64  `json:"cpu_usage_nsec"`
	MainPID         int    `json:"main_pid"`
	RestartLoopRisk bool   `json:"restart_loop_risk"`
}

// --- RQLite ---

type RQLiteReport struct {
	Responsive  bool                      `json:"responsive"`
	Ready       bool                      `json:"ready"`
	StrongRead  bool                      `json:"strong_read"`
	RaftState   string                    `json:"raft_state,omitempty"`
	LeaderAddr  string                    `json:"leader_addr,omitempty"`
	LeaderID    string                    `json:"leader_id,omitempty"`
	NodeID      string                    `json:"node_id,omitempty"`
	Term        uint64                    `json:"term,omitempty"`
	Applied     uint64                    `json:"applied_index,omitempty"`
	Commit      uint64                    `json:"commit_index,omitempty"`
	FsmPending  uint64                    `json:"fsm_pending,omitempty"`
	LastContact string                    `json:"last_contact,omitempty"`
	NumPeers    int                       `json:"num_peers,omitempty"`
	Voter       bool                      `json:"voter,omitempty"`
	DBSize      string                    `json:"db_size,omitempty"`
	Uptime      string                    `json:"uptime,omitempty"`
	Version     string                    `json:"version,omitempty"`
	Goroutines  int                       `json:"goroutines,omitempty"`
	HeapMB      int                       `json:"heap_mb,omitempty"`
	Nodes       map[string]RQLiteNodeInfo `json:"nodes,omitempty"`
	DebugVars   *RQLiteDebugVarsReport    `json:"debug_vars,omitempty"`
}

type RQLiteNodeInfo struct {
	Reachable bool    `json:"reachable"`
	Leader    bool    `json:"leader"`
	Voter     bool    `json:"voter"`
	TimeMS    float64 `json:"time_ms"`
	Error     string  `json:"error,omitempty"`
}

type RQLiteDebugVarsReport struct {
	QueryErrors      uint64 `json:"query_errors"`
	ExecuteErrors    uint64 `json:"execute_errors"`
	RemoteExecErrors uint64 `json:"remote_exec_errors"`
	LeaderNotFound   uint64 `json:"leader_not_found"`
	SnapshotErrors   uint64 `json:"snapshot_errors"`
	ClientRetries    uint64 `json:"client_retries"`
	ClientTimeouts   uint64 `json:"client_timeouts"`
}

// --- Olric ---

type OlricReport struct {
	ServiceActive bool     `json:"service_active"`
	MemberlistUp  bool     `json:"memberlist_up"`
	MemberCount   int      `json:"member_count,omitempty"`
	Members       []string `json:"members,omitempty"`
	Coordinator   string   `json:"coordinator,omitempty"`
	ProcessMemMB  int      `json:"process_mem_mb"`
	RestartCount  int      `json:"restart_count"`
	LogErrors     int      `json:"log_errors_1h"`
	LogSuspects   int      `json:"log_suspects_1h"`
	LogFlapping   int      `json:"log_flapping_1h"`
}

// --- IPFS ---

type IPFSReport struct {
	DaemonActive     bool   `json:"daemon_active"`
	ClusterActive    bool   `json:"cluster_active"`
	SwarmPeerCount   int    `json:"swarm_peer_count"`
	ClusterPeerCount int    `json:"cluster_peer_count"`
	ClusterErrors    int    `json:"cluster_errors"`
	RepoSizeBytes    int64  `json:"repo_size_bytes"`
	RepoMaxBytes     int64  `json:"repo_max_bytes"`
	RepoUsePct       int    `json:"repo_use_pct"`
	KuboVersion      string `json:"kubo_version,omitempty"`
	ClusterVersion   string `json:"cluster_version,omitempty"`
	HasSwarmKey      bool   `json:"has_swarm_key"`
	BootstrapEmpty   bool   `json:"bootstrap_empty"`
}

// --- Vault ---

type VaultReport struct {
	ServiceActive bool   `json:"service_active"`
	Responsive    bool   `json:"responsive"`
	Status        string `json:"status,omitempty"` // "healthy", "degraded", "unavailable"
	Guardians     int    `json:"guardians,omitempty"`
	Healthy       int    `json:"healthy,omitempty"`
	Threshold     int    `json:"threshold,omitempty"`
	WriteQuorum   int    `json:"write_quorum,omitempty"`
	ProcessMemMB  int    `json:"process_mem_mb"`
	RestartCount  int    `json:"restart_count"`
	LogErrors     int    `json:"log_errors_1h"`
}

// --- Gateway ---

type GatewayReport struct {
	Responsive bool                       `json:"responsive"`
	HTTPStatus int                        `json:"http_status,omitempty"`
	Version    string                     `json:"version,omitempty"`
	Subsystems map[string]SubsystemHealth `json:"subsystems,omitempty"`
}

type SubsystemHealth struct {
	Status  string `json:"status"`
	Latency string `json:"latency,omitempty"`
	Error   string `json:"error,omitempty"`
}

// --- WireGuard ---

type WireGuardReport struct {
	InterfaceUp   bool         `json:"interface_up"`
	ServiceActive bool         `json:"service_active"`
	WgIP          string       `json:"wg_ip,omitempty"`
	ListenPort    int          `json:"listen_port,omitempty"`
	PeerCount     int          `json:"peer_count"`
	MTU           int          `json:"mtu,omitempty"`
	ConfigExists  bool         `json:"config_exists"`
	ConfigPerms   string       `json:"config_perms,omitempty"`
	Peers         []WGPeerInfo `json:"peers,omitempty"`
}

type WGPeerInfo struct {
	PublicKey       string `json:"public_key"`
	Endpoint        string `json:"endpoint,omitempty"`
	AllowedIPs      string `json:"allowed_ips"`
	LatestHandshake int64  `json:"latest_handshake"`
	HandshakeAgeSec int64  `json:"handshake_age_sec"`
	TransferRx      int64  `json:"transfer_rx_bytes"`
	TransferTx      int64  `json:"transfer_tx_bytes"`
	Keepalive       int    `json:"keepalive,omitempty"`
}

// --- DNS ---

type DNSReport struct {
	CoreDNSActive    bool `json:"coredns_active"`
	CaddyActive      bool `json:"caddy_active"`
	Port53Bound      bool `json:"port_53_bound"`
	Port80Bound      bool `json:"port_80_bound"`
	Port443Bound     bool `json:"port_443_bound"`
	CoreDNSMemMB     int  `json:"coredns_mem_mb"`
	CoreDNSRestarts  int  `json:"coredns_restarts"`
	LogErrors        int  `json:"log_errors_5m"`
	CorefileExists   bool `json:"corefile_exists"`
	SOAResolves      bool `json:"soa_resolves"`
	NSResolves       bool `json:"ns_resolves"`
	NSRecordCount    int  `json:"ns_record_count"`
	WildcardResolves bool `json:"wildcard_resolves"`
	BaseAResolves    bool `json:"base_a_resolves"`
	BaseTLSDaysLeft  int  `json:"base_tls_days_left"`
	WildTLSDaysLeft  int  `json:"wild_tls_days_left"`
}

// --- Anyone ---

type AnyoneReport struct {
	RelayActive      bool   `json:"relay_active"`
	ClientActive     bool   `json:"client_active"`
	Mode             string `json:"mode,omitempty"`
	ORPortListening  bool   `json:"orport_listening"`
	SocksListening   bool   `json:"socks_listening"`
	ControlListening bool   `json:"control_listening"`
	Bootstrapped     bool   `json:"bootstrapped"`
	BootstrapPct     int    `json:"bootstrap_pct"`
	Fingerprint      string `json:"fingerprint,omitempty"`
	Nickname         string `json:"nickname,omitempty"`
}

// --- Network ---

type NetworkReport struct {
	InternetReachable bool       `json:"internet_reachable"`
	DefaultRoute      bool       `json:"default_route"`
	WGRouteExists     bool       `json:"wg_route_exists"`
	TCPEstablished    int        `json:"tcp_established"`
	TCPTimeWait       int        `json:"tcp_time_wait"`
	TCPRetransRate    float64    `json:"tcp_retrans_pct"`
	ListeningPorts    []PortInfo `json:"listening_ports"`
	UFWActive         bool       `json:"ufw_active"`
	UFWRules          []string   `json:"ufw_rules,omitempty"`
}

type PortInfo struct {
	Port    int    `json:"port"`
	Proto   string `json:"proto"`
	Process string `json:"process,omitempty"`
}

// --- Processes ---

type ProcessReport struct {
	ZombieCount int           `json:"zombie_count"`
	Zombies     []ProcessInfo `json:"zombies,omitempty"`
	OrphanCount int           `json:"orphan_count"`
	Orphans     []ProcessInfo `json:"orphans,omitempty"`
	PanicCount  int           `json:"panic_count_1h"`
}

type ProcessInfo struct {
	PID     int    `json:"pid"`
	PPID    int    `json:"ppid"`
	State   string `json:"state"`
	Command string `json:"command"`
}

// --- Namespaces ---

type NamespaceReport struct {
	Name          string `json:"name"`
	PortBase      int    `json:"port_base"`
	RQLiteUp      bool   `json:"rqlite_up"`
	RQLiteState   string `json:"rqlite_state,omitempty"`
	RQLiteReady   bool   `json:"rqlite_ready"`
	OlricUp       bool   `json:"olric_up"`
	GatewayUp     bool   `json:"gateway_up"`
	GatewayStatus int    `json:"gateway_status,omitempty"`
	SFUUp         bool   `json:"sfu_up"`
	TURNUp        bool   `json:"turn_up"`
}

// --- Deployments ---

type DeploymentsReport struct {
	TotalCount   int `json:"total_count"`
	RunningCount int `json:"running_count"`
	FailedCount  int `json:"failed_count"`
	StaticCount  int `json:"static_count"`
}

// --- Serverless ---

type ServerlessReport struct {
	FunctionCount int    `json:"function_count"`
	EngineStatus  string `json:"engine_status"`
}
