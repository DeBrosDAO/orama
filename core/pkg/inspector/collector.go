package inspector

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ClusterData holds all collected data from the cluster.
type ClusterData struct {
	Nodes    map[string]*NodeData // keyed by host IP
	Duration time.Duration
}

// NodeData holds collected data for a single node.
type NodeData struct {
	Node       Node
	RQLite     *RQLiteData
	Olric      *OlricData
	IPFS       *IPFSData
	DNS        *DNSData
	WireGuard  *WireGuardData
	System     *SystemData
	Network    *NetworkData
	Anyone     *AnyoneData
	Namespaces []NamespaceData // namespace instances on this node
	Errors     []string        // collection errors for this node
}

// NamespaceData holds data for a single namespace on a node.
type NamespaceData struct {
	Name          string // namespace name (from systemd unit)
	PortBase      int    // starting port of the 5-port block
	RQLiteUp      bool   // RQLite HTTP port responding
	RQLiteState   string // Raft state (Leader/Follower)
	RQLiteReady   bool   // /readyz
	OlricUp       bool   // Olric memberlist port listening
	GatewayUp     bool   // Gateway HTTP port responding
	GatewayStatus int    // HTTP status code from gateway health
	SFUUp         bool   // SFU systemd service active (optional, WebRTC)
	TURNUp        bool   // TURN systemd service active (optional, WebRTC)
}

// RQLiteData holds parsed RQLite status from a single node.
type RQLiteData struct {
	Responsive bool
	StatusRaw  string                 // raw JSON from /status
	NodesRaw   string                 // raw JSON from /nodes?nonvoters
	ReadyzRaw  string                 // raw response from /readyz
	DebugRaw   string                 // raw JSON from /debug/vars
	Status     *RQLiteStatus          // parsed /status
	Nodes      map[string]*RQLiteNode // parsed /nodes
	Readyz     *RQLiteReadyz          // parsed /readyz
	DebugVars  *RQLiteDebugVars       // parsed /debug/vars
	StrongRead bool                   // SELECT 1 with level=strong succeeded
}

// RQLiteDebugVars holds metrics from /debug/vars.
type RQLiteDebugVars struct {
	QueryErrors      uint64
	ExecuteErrors    uint64
	RemoteExecErrors uint64
	LeaderNotFound   uint64
	SnapshotErrors   uint64
	ClientRetries    uint64
	ClientTimeouts   uint64
}

// RQLiteStatus holds parsed fields from /status.
type RQLiteStatus struct {
	RaftState      string // Leader, Follower, Candidate, Shutdown
	LeaderNodeID   string // store.leader.node_id
	LeaderAddr     string // store.leader.addr
	NodeID         string // store.node_id
	Term           uint64 // store.raft.term (current_term)
	AppliedIndex   uint64 // store.raft.applied_index
	CommitIndex    uint64 // store.raft.commit_index
	FsmPending     uint64 // store.raft.fsm_pending
	LastContact    string // store.raft.last_contact (followers only)
	LastLogIndex   uint64 // store.raft.last_log_index
	LastLogTerm    uint64 // store.raft.last_log_term
	NumPeers       int    // store.raft.num_peers (string in JSON)
	LastSnapshot   uint64 // store.raft.last_snapshot_index
	Voter          bool   // store.raft.voter
	DBSize         int64  // store.sqlite3.db_size
	DBSizeFriendly string // store.sqlite3.db_size_friendly
	DBAppliedIndex uint64 // store.db_applied_index
	FsmIndex       uint64 // store.fsm_index
	Uptime         string // http.uptime
	Version        string // build.version
	GoVersion      string // runtime.GOARCH + runtime.version
	Goroutines     int    // runtime.num_goroutine
	HeapAlloc      uint64 // runtime.memory.heap_alloc (bytes)
}

// RQLiteNode holds parsed fields from /nodes response per node.
type RQLiteNode struct {
	Addr      string
	Reachable bool
	Leader    bool
	Voter     bool
	Time      float64 // response time
	Error     string
}

// RQLiteReadyz holds parsed readiness state.
type RQLiteReadyz struct {
	Ready   bool
	Store   string // "ready" or error
	Leader  string // "ready" or error
	Node    string // "ready" or error
	RawBody string
}

// OlricData holds parsed Olric status from a single node.
type OlricData struct {
	ServiceActive bool
	MemberlistUp  bool
	MemberCount   int
	Members       []string // memberlist member addresses
	Coordinator   string   // current coordinator address
	LogErrors     int      // error count in recent logs
	LogSuspects   int      // "suspect" or "Marking as failed" count
	LogFlapping   int      // rapid join/leave count
	ProcessMemMB  int      // RSS memory in MB
	RestartCount  int      // NRestarts from systemd
}

// IPFSData holds parsed IPFS status from a single node.
type IPFSData struct {
	DaemonActive     bool
	ClusterActive    bool
	SwarmPeerCount   int
	ClusterPeerCount int
	RepoSizeBytes    int64
	RepoMaxBytes     int64
	KuboVersion      string
	ClusterVersion   string
	ClusterErrors    int // peers reporting errors
	HasSwarmKey      bool
	BootstrapEmpty   bool // true if bootstrap list is empty (private swarm)
}

// DNSData holds parsed DNS/CoreDNS status from a nameserver node.
type DNSData struct {
	CoreDNSActive   bool
	CaddyActive     bool
	Port53Bound     bool
	Port80Bound     bool
	Port443Bound    bool
	CoreDNSMemMB    int
	CoreDNSRestarts int
	LogErrors       int // error count in recent CoreDNS logs
	// Resolution tests (dig results)
	SOAResolves      bool
	NSResolves       bool
	NSRecordCount    int
	WildcardResolves bool
	BaseAResolves    bool
	// TLS
	BaseTLSDaysLeft int // -1 = failed to check
	WildTLSDaysLeft int // -1 = failed to check
	// Corefile
	CorefileExists bool
}

// WireGuardData holds parsed WireGuard status from a node.
type WireGuardData struct {
	InterfaceUp   bool
	ServiceActive bool
	WgIP          string
	PeerCount     int
	Peers         []WGPeer
	MTU           int
	ListenPort    int
	ConfigExists  bool
	ConfigPerms   string // e.g. "600"
}

// WGPeer holds parsed data for a single WireGuard peer.
type WGPeer struct {
	PublicKey       string
	Endpoint        string
	AllowedIPs      string
	LatestHandshake int64 // seconds since epoch, 0 = never
	TransferRx      int64
	TransferTx      int64
	Keepalive       int
}

// SystemData holds parsed system-level data from a node.
type SystemData struct {
	Services       map[string]string // service name → status
	FailedUnits    []string          // systemd units in failed state
	MemTotalMB     int
	MemUsedMB      int
	MemFreeMB      int
	DiskTotalGB    string
	DiskUsedGB     string
	DiskAvailGB    string
	DiskUsePct     int
	UptimeRaw      string
	LoadAvg        string
	CPUCount       int
	OOMKills       int
	SwapUsedMB     int
	SwapTotalMB    int
	InodePct       int   // inode usage percentage
	ListeningPorts []int // ports from ss -tlnp
	UFWActive      bool
	ProcessUser    string // user running orama-node (e.g. "orama")
	PanicCount     int    // panic/fatal in recent logs
}

// NetworkData holds parsed network-level data from a node.
type NetworkData struct {
	InternetReachable bool
	TCPEstablished    int
	TCPTimeWait       int
	TCPRetransRate    float64 // retransmission % from /proc/net/snmp
	DefaultRoute      bool
	WGRouteExists     bool
	PingResults       map[string]bool // WG peer IP → ping success
}

// AnyoneData holds parsed Anyone relay/client status from a node.
type AnyoneData struct {
	RelayActive      bool            // orama-anyone-relay systemd service active
	ClientActive     bool            // orama-anyone-client systemd service active
	Mode             string          // "relay" or "client" (from anonrc ORPort presence)
	ORPortListening  bool            // port 9001 bound locally
	SocksListening   bool            // port 9050 bound locally (client SOCKS5)
	ControlListening bool            // port 9051 bound locally (control port)
	Bootstrapped     bool            // relay has bootstrapped to 100%
	BootstrapPct     int             // bootstrap percentage (0-100)
	Fingerprint      string          // relay fingerprint
	Nickname         string          // relay nickname
	UptimeStr        string          // uptime from control port
	ORPortReachable  map[string]bool // host IP → whether we can TCP connect to their 9001 from this node
}

// Collect gathers data from all nodes in parallel.
func Collect(ctx context.Context, nodes []Node, subsystems []string, verbose bool) *ClusterData {
	start := time.Now()
	data := &ClusterData{
		Nodes: make(map[string]*NodeData, len(nodes)),
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, node := range nodes {
		wg.Add(1)
		go func(n Node) {
			defer wg.Done()
			nd := collectNode(ctx, n, subsystems, verbose)
			mu.Lock()
			data.Nodes[n.Host] = nd
			mu.Unlock()
		}(node)
	}

	wg.Wait()

	data.Duration = time.Since(start)
	return data
}

func collectNode(ctx context.Context, node Node, subsystems []string, verbose bool) *NodeData {
	nd := &NodeData{Node: node}

	shouldCollect := func(name string) bool {
		if len(subsystems) == 0 {
			return true
		}
		for _, s := range subsystems {
			if s == name || s == "all" {
				return true
			}
		}
		return false
	}

	if shouldCollect("rqlite") {
		nd.RQLite = collectRQLite(ctx, node, verbose)
	}
	if shouldCollect("olric") {
		nd.Olric = collectOlric(ctx, node)
	}
	if shouldCollect("ipfs") {
		nd.IPFS = collectIPFS(ctx, node)
	}
	if shouldCollect("dns") && node.IsNameserver() {
		nd.DNS = collectDNS(ctx, node)
	}
	if shouldCollect("wireguard") || shouldCollect("wg") {
		nd.WireGuard = collectWireGuard(ctx, node)
	}
	if shouldCollect("system") {
		nd.System = collectSystem(ctx, node)
	}
	if shouldCollect("network") {
		nd.Network = collectNetwork(ctx, node, nd.WireGuard)
	}
	if shouldCollect("anyone") && !node.IsNameserver() {
		nd.Anyone = collectAnyone(ctx, node)
	}
	// Namespace collection — always collect if any subsystem is collected
	nd.Namespaces = collectNamespaces(ctx, node)

	return nd
}

// collectRQLite gathers RQLite data from a node via SSH.
func collectRQLite(ctx context.Context, node Node, verbose bool) *RQLiteData {
	data := &RQLiteData{}

	// Collect all endpoints in a single SSH session for efficiency.
	// We use a separator to split the outputs.
	cmd := `
SEP="===INSPECTOR_SEP==="
echo "$SEP"
curl -sf http://localhost:5001/status 2>/dev/null || echo '{"error":"unreachable"}'
echo "$SEP"
curl -sf 'http://localhost:5001/nodes?nonvoters' 2>/dev/null || echo '{"error":"unreachable"}'
echo "$SEP"
curl -sf http://localhost:5001/readyz 2>/dev/null; echo "EXIT:$?"
echo "$SEP"
curl -sf http://localhost:5001/debug/vars 2>/dev/null || echo '{"error":"unreachable"}'
echo "$SEP"
curl -sf -H 'Content-Type: application/json' 'http://localhost:5001/db/query?level=strong' -d '["SELECT 1"]' 2>/dev/null && echo "STRONG_OK" || echo "STRONG_FAIL"
`

	result := RunSSH(ctx, node, cmd)
	if !result.OK() && result.Stdout == "" {
		return data
	}

	parts := strings.Split(result.Stdout, "===INSPECTOR_SEP===")
	if len(parts) < 5 {
		return data
	}

	data.StatusRaw = strings.TrimSpace(parts[1])
	data.NodesRaw = strings.TrimSpace(parts[2])
	readyzSection := strings.TrimSpace(parts[3])
	data.DebugRaw = strings.TrimSpace(parts[4])

	// Parse /status
	if data.StatusRaw != "" && !strings.Contains(data.StatusRaw, `"error":"unreachable"`) {
		data.Responsive = true
		data.Status = parseRQLiteStatus(data.StatusRaw)
	}

	// Parse /nodes
	if data.NodesRaw != "" && !strings.Contains(data.NodesRaw, `"error":"unreachable"`) {
		data.Nodes = parseRQLiteNodes(data.NodesRaw)
	}

	// Parse /readyz
	data.Readyz = parseRQLiteReadyz(readyzSection)

	// Parse /debug/vars
	if data.DebugRaw != "" && !strings.Contains(data.DebugRaw, `"error":"unreachable"`) {
		data.DebugVars = parseRQLiteDebugVars(data.DebugRaw)
	}

	// Parse strong read
	if len(parts) > 5 {
		data.StrongRead = strings.Contains(parts[5], "STRONG_OK")
	}

	return data
}

func parseRQLiteStatus(raw string) *RQLiteStatus {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}

	s := &RQLiteStatus{}

	store, _ := m["store"].(map[string]interface{})
	if store == nil {
		return s
	}

	// Raft state
	raft, _ := store["raft"].(map[string]interface{})
	if raft != nil {
		s.RaftState, _ = raft["state"].(string)
		s.Term = jsonUint64(raft, "current_term")
		s.AppliedIndex = jsonUint64(raft, "applied_index")
		s.CommitIndex = jsonUint64(raft, "commit_index")
		s.FsmPending = jsonUint64(raft, "fsm_pending")
		s.LastContact, _ = raft["last_contact"].(string)
		s.LastLogIndex = jsonUint64(raft, "last_log_index")
		s.LastLogTerm = jsonUint64(raft, "last_log_term")
		s.LastSnapshot = jsonUint64(raft, "last_snapshot_index")
		s.Voter = jsonBool(raft, "voter")

		// num_peers can be a string or number
		if np, ok := raft["num_peers"].(string); ok {
			s.NumPeers, _ = strconv.Atoi(np)
		} else if np, ok := raft["num_peers"].(float64); ok {
			s.NumPeers = int(np)
		}
	}

	// Leader info
	leader, _ := store["leader"].(map[string]interface{})
	if leader != nil {
		s.LeaderNodeID, _ = leader["node_id"].(string)
		s.LeaderAddr, _ = leader["addr"].(string)
	}

	s.NodeID, _ = store["node_id"].(string)
	s.DBAppliedIndex = jsonUint64(store, "db_applied_index")
	s.FsmIndex = jsonUint64(store, "fsm_index")

	// SQLite
	sqlite3, _ := store["sqlite3"].(map[string]interface{})
	if sqlite3 != nil {
		s.DBSize = int64(jsonUint64(sqlite3, "db_size"))
		s.DBSizeFriendly, _ = sqlite3["db_size_friendly"].(string)
	}

	// HTTP
	httpMap, _ := m["http"].(map[string]interface{})
	if httpMap != nil {
		s.Uptime, _ = httpMap["uptime"].(string)
	}

	// Build
	build, _ := m["build"].(map[string]interface{})
	if build != nil {
		s.Version, _ = build["version"].(string)
	}

	// Runtime
	runtime, _ := m["runtime"].(map[string]interface{})
	if runtime != nil {
		if ng, ok := runtime["num_goroutine"].(float64); ok {
			s.Goroutines = int(ng)
		}
		s.GoVersion, _ = runtime["version"].(string)
		if mem, ok := runtime["memory"].(map[string]interface{}); ok {
			s.HeapAlloc = jsonUint64(mem, "heap_alloc")
		}
	}

	return s
}

func parseRQLiteNodes(raw string) map[string]*RQLiteNode {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}

	nodes := make(map[string]*RQLiteNode, len(m))
	for addr, v := range m {
		info, _ := v.(map[string]interface{})
		if info == nil {
			continue
		}
		n := &RQLiteNode{
			Addr:      addr,
			Reachable: jsonBool(info, "reachable"),
			Leader:    jsonBool(info, "leader"),
			Voter:     jsonBool(info, "voter"),
		}
		if t, ok := info["time"].(float64); ok {
			n.Time = t
		}
		if e, ok := info["error"].(string); ok {
			n.Error = e
		}
		nodes[addr] = n
	}
	return nodes
}

func parseRQLiteReadyz(raw string) *RQLiteReadyz {
	r := &RQLiteReadyz{RawBody: raw}

	// /readyz returns body like "[+]node ok\n[+]leader ok\n[+]store ok" with exit 0
	// or "[-]leader not ok\n..." with non-zero exit
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[+]node") {
			r.Node = "ready"
		} else if strings.HasPrefix(line, "[-]node") {
			r.Node = "not ready"
		} else if strings.HasPrefix(line, "[+]leader") {
			r.Leader = "ready"
		} else if strings.HasPrefix(line, "[-]leader") {
			r.Leader = "not ready"
		} else if strings.HasPrefix(line, "[+]store") {
			r.Store = "ready"
		} else if strings.HasPrefix(line, "[-]store") {
			r.Store = "not ready"
		}
	}

	r.Ready = r.Node == "ready" && r.Leader == "ready" && r.Store == "ready"

	// Check exit code from our appended "EXIT:$?"
	for _, line := range lines {
		if strings.HasPrefix(line, "EXIT:0") {
			r.Ready = true
		}
	}

	return r
}

func parseRQLiteDebugVars(raw string) *RQLiteDebugVars {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}

	d := &RQLiteDebugVars{}

	// /debug/vars has flat keys like "store.query_errors", "store.execute_errors", etc.
	// But they can also be nested under "cmdstats" or flat depending on rqlite version.
	// Try flat numeric keys first.
	getUint := func(keys ...string) uint64 {
		for _, key := range keys {
			if v, ok := m[key]; ok {
				switch val := v.(type) {
				case float64:
					return uint64(val)
				case string:
					n, _ := strconv.ParseUint(val, 10, 64)
					return n
				}
			}
		}
		return 0
	}

	d.QueryErrors = getUint("query_errors", "store.query_errors")
	d.ExecuteErrors = getUint("execute_errors", "store.execute_errors")
	d.RemoteExecErrors = getUint("remote_execute_errors", "store.remote_execute_errors")
	d.LeaderNotFound = getUint("leader_not_found", "store.leader_not_found")
	d.SnapshotErrors = getUint("snapshot_errors", "store.snapshot_errors")
	d.ClientRetries = getUint("client_retries", "cluster.client_retries")
	d.ClientTimeouts = getUint("client_timeouts", "cluster.client_timeouts")

	return d
}

// Placeholder collectors for Phase 2

func collectOlric(ctx context.Context, node Node) *OlricData {
	data := &OlricData{}

	cmd := `
SEP="===INSPECTOR_SEP==="
echo "$SEP"
systemctl is-active orama-olric 2>/dev/null
echo "$SEP"
ss -tlnp 2>/dev/null | grep ':3322 ' | head -1
echo "$SEP"
journalctl -u orama-olric --no-pager -n 200 --since "1 hour ago" 2>/dev/null | grep -ciE '(error|ERR)' || echo 0
echo "$SEP"
journalctl -u orama-olric --no-pager -n 200 --since "1 hour ago" 2>/dev/null | grep -ciE '(suspect|marking.*(failed|dead))' || echo 0
echo "$SEP"
journalctl -u orama-olric --no-pager -n 200 --since "1 hour ago" 2>/dev/null | grep -ciE '(memberlist.*(join|leave))' || echo 0
echo "$SEP"
systemctl show orama-olric --property=NRestarts 2>/dev/null | cut -d= -f2
echo "$SEP"
ps -C olric-server -o rss= 2>/dev/null | head -1 || echo 0
`
	res := RunSSH(ctx, node, cmd)
	if !res.OK() && res.Stdout == "" {
		return data
	}

	parts := strings.Split(res.Stdout, "===INSPECTOR_SEP===")
	if len(parts) < 8 {
		return data
	}

	data.ServiceActive = strings.TrimSpace(parts[1]) == "active"
	data.MemberlistUp = strings.TrimSpace(parts[2]) != ""

	data.LogErrors = parseIntDefault(strings.TrimSpace(parts[3]), 0)
	data.LogSuspects = parseIntDefault(strings.TrimSpace(parts[4]), 0)
	data.LogFlapping = parseIntDefault(strings.TrimSpace(parts[5]), 0)
	data.RestartCount = parseIntDefault(strings.TrimSpace(parts[6]), 0)

	rssKB := parseIntDefault(strings.TrimSpace(parts[7]), 0)
	data.ProcessMemMB = rssKB / 1024

	return data
}

func collectIPFS(ctx context.Context, node Node) *IPFSData {
	data := &IPFSData{}

	cmd := `
SEP="===INSPECTOR_SEP==="
echo "$SEP"
systemctl is-active orama-ipfs 2>/dev/null
echo "$SEP"
systemctl is-active orama-ipfs-cluster 2>/dev/null
echo "$SEP"
curl -sf -X POST 'http://localhost:4501/api/v0/swarm/peers' 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('Peers') or []))" 2>/dev/null || echo -1
echo "$SEP"
curl -sf --max-time 10 'http://localhost:9094/peers' 2>/dev/null | python3 -c "import sys,json; peers=json.load(sys.stdin); print(len(peers)); errs=sum(1 for p in peers if p.get('error','')); print(errs)" 2>/dev/null || (curl -sf 'http://localhost:9094/id' 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); peers=d.get('cluster_peers',[]); print(len(peers)); print(0)" 2>/dev/null || echo -1)
echo "$SEP"
curl -sf -X POST 'http://localhost:4501/api/v0/repo/stat' 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('RepoSize',0)); print(d.get('StorageMax',0))" 2>/dev/null || echo -1
echo "$SEP"
curl -sf -X POST 'http://localhost:4501/api/v0/version' 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('Version',''))" 2>/dev/null || echo unknown
echo "$SEP"
curl -sf 'http://localhost:9094/id' 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('version',''))" 2>/dev/null || echo unknown
echo "$SEP"
test -f /opt/orama/.orama/data/ipfs/repo/swarm.key && echo yes || echo no
echo "$SEP"
curl -sf -X POST 'http://localhost:4501/api/v0/bootstrap/list' 2>/dev/null | python3 -c "import sys,json; peers=json.load(sys.stdin).get('Peers',[]); print(len(peers))" 2>/dev/null || echo -1
`
	res := RunSSH(ctx, node, cmd)
	if !res.OK() && res.Stdout == "" {
		return data
	}

	parts := strings.Split(res.Stdout, "===INSPECTOR_SEP===")
	if len(parts) < 10 {
		return data
	}

	data.DaemonActive = strings.TrimSpace(parts[1]) == "active"
	data.ClusterActive = strings.TrimSpace(parts[2]) == "active"
	data.SwarmPeerCount = parseIntDefault(strings.TrimSpace(parts[3]), -1)

	// Cluster peers: first line = count, second = errors
	clusterLines := strings.Split(strings.TrimSpace(parts[4]), "\n")
	if len(clusterLines) >= 1 {
		data.ClusterPeerCount = parseIntDefault(strings.TrimSpace(clusterLines[0]), -1)
	}
	if len(clusterLines) >= 2 {
		data.ClusterErrors = parseIntDefault(strings.TrimSpace(clusterLines[1]), 0)
	}

	// Repo stat: first line = size, second = max
	repoLines := strings.Split(strings.TrimSpace(parts[5]), "\n")
	if len(repoLines) >= 1 {
		data.RepoSizeBytes = int64(parseIntDefault(strings.TrimSpace(repoLines[0]), 0))
	}
	if len(repoLines) >= 2 {
		data.RepoMaxBytes = int64(parseIntDefault(strings.TrimSpace(repoLines[1]), 0))
	}

	data.KuboVersion = strings.TrimSpace(parts[6])
	data.ClusterVersion = strings.TrimSpace(parts[7])
	data.HasSwarmKey = strings.TrimSpace(parts[8]) == "yes"

	bootstrapCount := parseIntDefault(strings.TrimSpace(parts[9]), -1)
	data.BootstrapEmpty = bootstrapCount == 0

	return data
}

func collectDNS(ctx context.Context, node Node) *DNSData {
	data := &DNSData{
		BaseTLSDaysLeft: -1,
		WildTLSDaysLeft: -1,
	}

	// Get the domain from the node's role (e.g. "nameserver-ns1" -> we need the domain)
	// We'll discover the domain from Corefile
	cmd := `
SEP="===INSPECTOR_SEP==="
echo "$SEP"
systemctl is-active coredns 2>/dev/null
echo "$SEP"
systemctl is-active caddy 2>/dev/null
echo "$SEP"
ss -ulnp 2>/dev/null | grep ':53 ' | head -1
echo "$SEP"
ss -tlnp 2>/dev/null | grep ':80 ' | head -1
echo "$SEP"
ss -tlnp 2>/dev/null | grep ':443 ' | head -1
echo "$SEP"
ps -C coredns -o rss= 2>/dev/null | head -1 || echo 0
echo "$SEP"
systemctl show coredns --property=NRestarts 2>/dev/null | cut -d= -f2
echo "$SEP"
journalctl -u coredns --no-pager -n 100 --since "5 minutes ago" 2>/dev/null | grep -iE '(error|ERR)' | grep -cvF 'NOERROR' || echo 0
echo "$SEP"
test -f /etc/coredns/Corefile && echo yes || echo no
echo "$SEP"
DOMAIN=$(grep -oP '^\S+(?=\s*\{)' /etc/coredns/Corefile 2>/dev/null | grep -v '^\.' | head -1)
echo "DOMAIN:${DOMAIN}"
dig @127.0.0.1 SOA ${DOMAIN} +short 2>/dev/null | head -1
echo "$SEP"
dig @127.0.0.1 NS ${DOMAIN} +short 2>/dev/null
echo "$SEP"
dig @127.0.0.1 A test-wildcard.${DOMAIN} +short 2>/dev/null | head -1
echo "$SEP"
dig @127.0.0.1 A ${DOMAIN} +short 2>/dev/null | head -1
echo "$SEP"
echo | openssl s_client -servername ${DOMAIN} -connect localhost:443 2>/dev/null | openssl x509 -noout -dates 2>/dev/null | grep notAfter | cut -d= -f2
echo "$SEP"
echo | openssl s_client -servername "*.${DOMAIN}" -connect localhost:443 2>/dev/null | openssl x509 -noout -dates 2>/dev/null | grep notAfter | cut -d= -f2
`
	res := RunSSH(ctx, node, cmd)
	if !res.OK() && res.Stdout == "" {
		return data
	}

	parts := strings.Split(res.Stdout, "===INSPECTOR_SEP===")
	if len(parts) < 9 {
		return data
	}

	data.CoreDNSActive = strings.TrimSpace(parts[1]) == "active"
	data.CaddyActive = strings.TrimSpace(parts[2]) == "active"
	data.Port53Bound = strings.TrimSpace(parts[3]) != ""
	data.Port80Bound = strings.TrimSpace(parts[4]) != ""
	data.Port443Bound = strings.TrimSpace(parts[5]) != ""

	rssKB := parseIntDefault(strings.TrimSpace(parts[6]), 0)
	data.CoreDNSMemMB = rssKB / 1024
	data.CoreDNSRestarts = parseIntDefault(strings.TrimSpace(parts[7]), 0)
	data.LogErrors = parseIntDefault(strings.TrimSpace(parts[8]), 0)

	// Corefile exists
	if len(parts) > 9 {
		data.CorefileExists = strings.TrimSpace(parts[9]) == "yes"
	}

	// SOA resolution
	if len(parts) > 10 {
		soaSection := strings.TrimSpace(parts[10])
		// First line might be DOMAIN:xxx, rest is dig output
		for _, line := range strings.Split(soaSection, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "DOMAIN:") {
				continue
			}
			if line != "" {
				data.SOAResolves = true
			}
		}
	}

	// NS records
	if len(parts) > 11 {
		nsSection := strings.TrimSpace(parts[11])
		count := 0
		for _, line := range strings.Split(nsSection, "\n") {
			if strings.TrimSpace(line) != "" {
				count++
			}
		}
		data.NSRecordCount = count
		data.NSResolves = count > 0
	}

	// Wildcard resolution
	if len(parts) > 12 {
		data.WildcardResolves = strings.TrimSpace(parts[12]) != ""
	}

	// Base A record
	if len(parts) > 13 {
		data.BaseAResolves = strings.TrimSpace(parts[13]) != ""
	}

	// TLS cert days left (base domain)
	if len(parts) > 14 {
		data.BaseTLSDaysLeft = parseTLSExpiry(strings.TrimSpace(parts[14]))
	}

	// TLS cert days left (wildcard)
	if len(parts) > 15 {
		data.WildTLSDaysLeft = parseTLSExpiry(strings.TrimSpace(parts[15]))
	}

	return data
}

// parseTLSExpiry parses an openssl date string and returns days until expiry (-1 on error).
func parseTLSExpiry(dateStr string) int {
	if dateStr == "" {
		return -1
	}
	// OpenSSL format: "Jan  2 15:04:05 2006 GMT"
	layouts := []string{
		"Jan  2 15:04:05 2006 GMT",
		"Jan 2 15:04:05 2006 GMT",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, dateStr); err == nil {
			days := int(time.Until(t).Hours() / 24)
			return days
		}
	}
	return -1
}

func collectWireGuard(ctx context.Context, node Node) *WireGuardData {
	data := &WireGuardData{}

	cmd := `
SEP="===INSPECTOR_SEP==="
echo "$SEP"
ip -4 addr show wg0 2>/dev/null | grep -oP 'inet \K[0-9.]+'
echo "$SEP"
systemctl is-active wg-quick@wg0 2>/dev/null
echo "$SEP"
cat /sys/class/net/wg0/mtu 2>/dev/null || echo 0
echo "$SEP"
sudo wg show wg0 dump 2>/dev/null
echo "$SEP"
sudo test -f /etc/wireguard/wg0.conf && echo yes || echo no
echo "$SEP"
sudo stat -c '%a' /etc/wireguard/wg0.conf 2>/dev/null || echo 000
`
	res := RunSSH(ctx, node, cmd)
	if !res.OK() && res.Stdout == "" {
		return data
	}

	parts := strings.Split(res.Stdout, "===INSPECTOR_SEP===")
	if len(parts) < 7 {
		return data
	}

	wgIP := strings.TrimSpace(parts[1])
	data.WgIP = wgIP
	data.InterfaceUp = wgIP != ""
	data.ServiceActive = strings.TrimSpace(parts[2]) == "active"
	data.MTU = parseIntDefault(strings.TrimSpace(parts[3]), 0)
	data.ConfigExists = strings.TrimSpace(parts[5]) == "yes"
	data.ConfigPerms = strings.TrimSpace(parts[6])

	// Parse wg show dump output
	// First line = interface: private-key public-key listen-port fwmark
	// Subsequent lines = peers: public-key preshared-key endpoint allowed-ips latest-handshake transfer-rx transfer-tx persistent-keepalive
	dumpLines := strings.Split(strings.TrimSpace(parts[4]), "\n")
	if len(dumpLines) >= 1 {
		ifFields := strings.Split(dumpLines[0], "\t")
		if len(ifFields) >= 3 {
			data.ListenPort = parseIntDefault(ifFields[2], 0)
		}
	}
	for _, line := range dumpLines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) < 8 {
			continue
		}
		handshake := int64(parseIntDefault(fields[4], 0))
		rx := int64(parseIntDefault(fields[5], 0))
		tx := int64(parseIntDefault(fields[6], 0))
		keepalive := parseIntDefault(fields[7], 0)

		data.Peers = append(data.Peers, WGPeer{
			PublicKey:       fields[0],
			Endpoint:        fields[2],
			AllowedIPs:      fields[3],
			LatestHandshake: handshake,
			TransferRx:      rx,
			TransferTx:      tx,
			Keepalive:       keepalive,
		})
	}
	data.PeerCount = len(data.Peers)

	return data
}

func collectSystem(ctx context.Context, node Node) *SystemData {
	data := &SystemData{
		Services: make(map[string]string),
	}

	services := []string{
		"orama-node", "orama-ipfs", "orama-ipfs-cluster",
		"orama-olric", "orama-anyone-relay", "orama-anyone-client",
		"coredns", "caddy", "wg-quick@wg0",
	}

	cmd := `SEP="===INSPECTOR_SEP==="`
	// Service statuses
	for _, svc := range services {
		cmd += fmt.Sprintf(` && echo "%s:$(systemctl is-active %s 2>/dev/null || echo inactive)"`, svc, svc)
	}
	cmd += ` && echo "$SEP"`
	cmd += ` && free -m | awk '/Mem:/{print $2","$3","$4} /Swap:/{print "SWAP:"$2","$3}'`
	cmd += ` && echo "$SEP"`
	cmd += ` && df -h / | awk 'NR==2{print $2","$3","$4","$5}'`
	cmd += ` && echo "$SEP"`
	cmd += ` && uptime -s 2>/dev/null || echo unknown`
	cmd += ` && echo "$SEP"`
	cmd += ` && nproc 2>/dev/null || echo 1`
	cmd += ` && echo "$SEP"`
	cmd += ` && uptime | grep -oP 'load average: \K.*'`
	cmd += ` && echo "$SEP"`
	cmd += ` && systemctl --failed --no-legend --no-pager 2>/dev/null | awk '{print $1}'`
	cmd += ` && echo "$SEP"`
	cmd += ` && dmesg 2>/dev/null | grep -ci 'out of memory' || echo 0`
	cmd += ` && echo "$SEP"`
	cmd += ` && df -i / 2>/dev/null | awk 'NR==2{print $5}' | tr -d '%'`
	cmd += ` && echo "$SEP"`
	cmd += ` && ss -tlnp 2>/dev/null | awk 'NR>1{split($4,a,":"); print a[length(a)]}' | sort -un`
	cmd += ` && echo "$SEP"`
	cmd += ` && sudo ufw status 2>/dev/null | head -1`
	cmd += ` && echo "$SEP"`
	cmd += ` && ps -C orama-node -o user= 2>/dev/null | head -1 || echo unknown`
	cmd += ` && echo "$SEP"`
	cmd += ` && journalctl -u orama-node --no-pager -n 500 --since "1 hour ago" 2>/dev/null | grep -ciE '(panic|fatal)' || echo 0`

	res := RunSSH(ctx, node, cmd)
	if !res.OK() && res.Stdout == "" {
		return data
	}

	parts := strings.Split(res.Stdout, "===INSPECTOR_SEP===")

	// Part 0: service statuses (before first SEP)
	if len(parts) > 0 {
		for _, line := range strings.Split(strings.TrimSpace(parts[0]), "\n") {
			line = strings.TrimSpace(line)
			if idx := strings.Index(line, ":"); idx > 0 {
				data.Services[line[:idx]] = line[idx+1:]
			}
		}
	}

	// Part 1: memory
	if len(parts) > 1 {
		for _, line := range strings.Split(strings.TrimSpace(parts[1]), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "SWAP:") {
				swapParts := strings.Split(strings.TrimPrefix(line, "SWAP:"), ",")
				if len(swapParts) >= 2 {
					data.SwapTotalMB = parseIntDefault(swapParts[0], 0)
					data.SwapUsedMB = parseIntDefault(swapParts[1], 0)
				}
			} else {
				memParts := strings.Split(line, ",")
				if len(memParts) >= 3 {
					data.MemTotalMB = parseIntDefault(memParts[0], 0)
					data.MemUsedMB = parseIntDefault(memParts[1], 0)
					data.MemFreeMB = parseIntDefault(memParts[2], 0)
				}
			}
		}
	}

	// Part 2: disk
	if len(parts) > 2 {
		diskParts := strings.Split(strings.TrimSpace(parts[2]), ",")
		if len(diskParts) >= 4 {
			data.DiskTotalGB = diskParts[0]
			data.DiskUsedGB = diskParts[1]
			data.DiskAvailGB = diskParts[2]
			pct := strings.TrimSuffix(diskParts[3], "%")
			data.DiskUsePct = parseIntDefault(pct, 0)
		}
	}

	// Part 3: uptime
	if len(parts) > 3 {
		data.UptimeRaw = strings.TrimSpace(parts[3])
	}

	// Part 4: CPU count
	if len(parts) > 4 {
		data.CPUCount = parseIntDefault(strings.TrimSpace(parts[4]), 1)
	}

	// Part 5: load average
	if len(parts) > 5 {
		data.LoadAvg = strings.TrimSpace(parts[5])
	}

	// Part 6: failed units
	if len(parts) > 6 {
		for _, line := range strings.Split(strings.TrimSpace(parts[6]), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				data.FailedUnits = append(data.FailedUnits, line)
			}
		}
	}

	// Part 7: OOM kills
	if len(parts) > 7 {
		data.OOMKills = parseIntDefault(strings.TrimSpace(parts[7]), 0)
	}

	// Part 8: inode usage
	if len(parts) > 8 {
		data.InodePct = parseIntDefault(strings.TrimSpace(parts[8]), 0)
	}

	// Part 9: listening ports
	if len(parts) > 9 {
		for _, line := range strings.Split(strings.TrimSpace(parts[9]), "\n") {
			line = strings.TrimSpace(line)
			if p := parseIntDefault(line, 0); p > 0 {
				data.ListeningPorts = append(data.ListeningPorts, p)
			}
		}
	}

	// Part 10: UFW status
	if len(parts) > 10 {
		data.UFWActive = strings.Contains(strings.TrimSpace(parts[10]), "active")
	}

	// Part 11: process user
	if len(parts) > 11 {
		data.ProcessUser = strings.TrimSpace(parts[11])
	}

	// Part 12: panic count
	if len(parts) > 12 {
		data.PanicCount = parseIntDefault(strings.TrimSpace(parts[12]), 0)
	}

	return data
}

func collectNetwork(ctx context.Context, node Node, wg *WireGuardData) *NetworkData {
	data := &NetworkData{
		PingResults: make(map[string]bool),
	}

	// Build ping commands for WG peer IPs
	var pingCmds string
	if wg != nil {
		for _, peer := range wg.Peers {
			// Extract IP from AllowedIPs (e.g. "10.0.0.2/32")
			ip := strings.Split(peer.AllowedIPs, "/")[0]
			if ip != "" && strings.HasPrefix(ip, "10.0.0.") {
				pingCmds += fmt.Sprintf(`echo "PING:%s:$(ping -c 1 -W 2 %s >/dev/null 2>&1 && echo ok || echo fail)"
`, ip, ip)
			}
		}
	}

	cmd := fmt.Sprintf(`
SEP="===INSPECTOR_SEP==="
echo "$SEP"
ping -c 1 -W 2 8.8.8.8 >/dev/null 2>&1 && echo yes || echo no
echo "$SEP"
ss -s 2>/dev/null | awk '/^TCP:/{print $0}'
echo "$SEP"
ip route show default 2>/dev/null | head -1
echo "$SEP"
ip route show 10.0.0.0/24 dev wg0 2>/dev/null | head -1
echo "$SEP"
awk '/^Tcp:/{getline; print $12" "$13}' /proc/net/snmp 2>/dev/null; sleep 1; awk '/^Tcp:/{getline; print $12" "$13}' /proc/net/snmp 2>/dev/null
echo "$SEP"
%s
`, pingCmds)

	res := RunSSH(ctx, node, cmd)
	if !res.OK() && res.Stdout == "" {
		return data
	}

	parts := strings.Split(res.Stdout, "===INSPECTOR_SEP===")

	if len(parts) > 1 {
		data.InternetReachable = strings.TrimSpace(parts[1]) == "yes"
	}

	// Parse TCP stats: "TCP:   42 (estab 15, closed 3, orphaned 0, timewait 2/0), ports 0/0/0"
	if len(parts) > 2 {
		tcpLine := strings.TrimSpace(parts[2])
		if idx := strings.Index(tcpLine, "estab "); idx >= 0 {
			rest := tcpLine[idx+6:]
			if comma := strings.IndexByte(rest, ','); comma > 0 {
				data.TCPEstablished = parseIntDefault(rest[:comma], 0)
			}
		}
		if idx := strings.Index(tcpLine, "timewait "); idx >= 0 {
			rest := tcpLine[idx+9:]
			if slash := strings.IndexByte(rest, '/'); slash > 0 {
				data.TCPTimeWait = parseIntDefault(rest[:slash], 0)
			} else if comma := strings.IndexByte(rest, ')'); comma > 0 {
				data.TCPTimeWait = parseIntDefault(rest[:comma], 0)
			}
		}
	}

	if len(parts) > 3 {
		data.DefaultRoute = strings.TrimSpace(parts[3]) != ""
	}
	if len(parts) > 4 {
		data.WGRouteExists = strings.TrimSpace(parts[4]) != ""
	}

	// Parse TCP retransmission rate from /proc/net/snmp (delta over 1 second)
	// Two snapshots: "OutSegs RetransSegs\nOutSegs RetransSegs"
	if len(parts) > 5 {
		lines := strings.Split(strings.TrimSpace(parts[5]), "\n")
		if len(lines) >= 2 {
			before := strings.Fields(lines[0])
			after := strings.Fields(lines[1])
			if len(before) >= 2 && len(after) >= 2 {
				outBefore := parseIntDefault(before[0], 0)
				retBefore := parseIntDefault(before[1], 0)
				outAfter := parseIntDefault(after[0], 0)
				retAfter := parseIntDefault(after[1], 0)
				deltaOut := outAfter - outBefore
				deltaRet := retAfter - retBefore
				if deltaOut > 0 {
					data.TCPRetransRate = float64(deltaRet) / float64(deltaOut) * 100
				}
			}
		}
	}

	// Parse ping results
	if len(parts) > 6 {
		for _, line := range strings.Split(strings.TrimSpace(parts[6]), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "PING:") {
				// Format: PING:<ip>:<ok|fail>
				pingParts := strings.SplitN(line, ":", 3)
				if len(pingParts) == 3 {
					data.PingResults[pingParts[1]] = pingParts[2] == "ok"
				}
			}
		}
	}

	return data
}

func collectAnyone(ctx context.Context, node Node) *AnyoneData {
	data := &AnyoneData{
		ORPortReachable: make(map[string]bool),
	}

	cmd := `
SEP="===INSPECTOR_SEP==="
echo "$SEP"
systemctl is-active orama-anyone-relay 2>/dev/null || echo inactive
echo "$SEP"
systemctl is-active orama-anyone-client 2>/dev/null || echo inactive
echo "$SEP"
ss -tlnp 2>/dev/null | grep -q ':9001 ' && echo yes || echo no
echo "$SEP"
ss -tlnp 2>/dev/null | grep -q ':9050 ' && echo yes || echo no
echo "$SEP"
ss -tlnp 2>/dev/null | grep -q ':9051 ' && echo yes || echo no
echo "$SEP"
# Check bootstrap status from log. Fall back to notices.log.1 if current log
# is empty (logrotate may have rotated the file without signaling the relay).
BPCT=$(grep -oP 'Bootstrapped \K[0-9]+' /var/log/anon/notices.log 2>/dev/null | tail -1)
if [ -z "$BPCT" ]; then
  BPCT=$(grep -oP 'Bootstrapped \K[0-9]+' /var/log/anon/notices.log.1 2>/dev/null | tail -1)
fi
echo "${BPCT:-0}"
echo "$SEP"
# Read fingerprint (sudo needed: file is owned by debian-anon with 0600 perms)
sudo cat /var/lib/anon/fingerprint 2>/dev/null || echo ""
echo "$SEP"
# Read nickname from config
grep -oP '^Nickname \K\S+' /etc/anon/anonrc 2>/dev/null || echo ""
echo "$SEP"
# Detect relay vs client mode: check if ORPort is configured in anonrc
grep -qP '^\s*ORPort\s' /etc/anon/anonrc 2>/dev/null && echo relay || echo client
`

	res := RunSSH(ctx, node, cmd)
	if !res.OK() && res.Stdout == "" {
		return data
	}

	parts := strings.Split(res.Stdout, "===INSPECTOR_SEP===")

	if len(parts) > 1 {
		data.RelayActive = strings.TrimSpace(parts[1]) == "active"
	}
	if len(parts) > 2 {
		data.ClientActive = strings.TrimSpace(parts[2]) == "active"
	}
	if len(parts) > 3 {
		data.ORPortListening = strings.TrimSpace(parts[3]) == "yes"
	}
	if len(parts) > 4 {
		data.SocksListening = strings.TrimSpace(parts[4]) == "yes"
	}
	if len(parts) > 5 {
		data.ControlListening = strings.TrimSpace(parts[5]) == "yes"
	}
	if len(parts) > 6 {
		pct := parseIntDefault(strings.TrimSpace(parts[6]), 0)
		data.BootstrapPct = pct
		data.Bootstrapped = pct >= 100
	}
	if len(parts) > 7 {
		data.Fingerprint = strings.TrimSpace(parts[7])
	}
	if len(parts) > 8 {
		data.Nickname = strings.TrimSpace(parts[8])
	}
	if len(parts) > 9 {
		data.Mode = strings.TrimSpace(parts[9])
	}

	// If neither relay nor client is active, skip further checks
	if !data.RelayActive && !data.ClientActive {
		return data
	}

	return data
}

func collectNamespaces(ctx context.Context, node Node) []NamespaceData {
	// Detect namespace services: orama-namespace-gateway@<name>.service
	cmd := `
SEP="===INSPECTOR_SEP==="
echo "$SEP"
systemctl list-units --type=service --all --no-pager --no-legend 'orama-namespace-gateway@*.service' 2>/dev/null | awk '{print $1}' | sed 's/orama-namespace-gateway@//;s/\.service//'
echo "$SEP"
`
	res := RunSSH(ctx, node, cmd)
	if !res.OK() && res.Stdout == "" {
		return nil
	}

	parts := strings.Split(res.Stdout, "===INSPECTOR_SEP===")
	if len(parts) < 2 {
		return nil
	}

	var names []string
	for _, line := range strings.Split(strings.TrimSpace(parts[1]), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}

	if len(names) == 0 {
		return nil
	}

	// For each namespace, check its services
	// Namespace ports: base = 10000 + (index * 5)
	// offset 0=RQLite HTTP, 1=RQLite Raft, 2=Olric HTTP, 3=Olric Memberlist, 4=Gateway HTTP
	// We discover actual ports by querying each namespace's services
	var nsCmd string
	for _, name := range names {
		nsCmd += fmt.Sprintf(`
echo "NS_START:%s"
# Get gateway port from systemd or default discovery
GWPORT=$(ss -tlnp 2>/dev/null | grep 'orama-namespace-gateway@%s' | grep -oP ':\K[0-9]+' | head -1)
echo "GW_PORT:${GWPORT:-0}"
# Try common namespace port ranges (10000-10099)
for BASE in $(seq 10000 5 10099); do
  RQLITE_PORT=$((BASE))
  if curl -sf --connect-timeout 1 "http://localhost:${RQLITE_PORT}/status" >/dev/null 2>&1; then
    STATUS=$(curl -sf --connect-timeout 1 "http://localhost:${RQLITE_PORT}/status" 2>/dev/null)
    STATE=$(echo "$STATUS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('store',{}).get('raft',{}).get('state',''))" 2>/dev/null || echo "")
    READYZ=$(curl -sf --connect-timeout 1 "http://localhost:${RQLITE_PORT}/readyz" 2>/dev/null && echo "yes" || echo "no")
    echo "RQLITE:${BASE}:up:${STATE}:${READYZ}"
    break
  fi
done
# Check Olric memberlist
OLRIC_PORT=$((BASE + 2))
ss -tlnp 2>/dev/null | grep -q ":${OLRIC_PORT} " && echo "OLRIC:up" || echo "OLRIC:down"
# Check Gateway
GW_PORT2=$((BASE + 4))
GW_STATUS=$(curl -sf -o /dev/null -w '%%{http_code}' --connect-timeout 1 "http://localhost:${GW_PORT2}/health" 2>/dev/null || echo "0")
echo "GATEWAY:${GW_STATUS}"
echo "NS_END"
`, name, name)
	}

	nsRes := RunSSH(ctx, node, nsCmd)
	if !nsRes.OK() && nsRes.Stdout == "" {
		// Return namespace names at minimum
		var result []NamespaceData
		for _, name := range names {
			result = append(result, NamespaceData{Name: name})
		}
		return result
	}

	// Parse namespace results
	var result []NamespaceData
	var current *NamespaceData
	for _, line := range strings.Split(nsRes.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "NS_START:") {
			name := strings.TrimPrefix(line, "NS_START:")
			nd := NamespaceData{Name: name}
			current = &nd
		} else if line == "NS_END" && current != nil {
			result = append(result, *current)
			current = nil
		} else if current != nil {
			if strings.HasPrefix(line, "RQLITE:") {
				// RQLITE:<base>:up:<state>:<readyz>
				rParts := strings.SplitN(line, ":", 5)
				if len(rParts) >= 5 {
					current.PortBase = parseIntDefault(rParts[1], 0)
					current.RQLiteUp = rParts[2] == "up"
					current.RQLiteState = rParts[3]
					current.RQLiteReady = rParts[4] == "yes"
				}
			} else if strings.HasPrefix(line, "OLRIC:") {
				current.OlricUp = strings.TrimPrefix(line, "OLRIC:") == "up"
			} else if strings.HasPrefix(line, "GATEWAY:") {
				code := parseIntDefault(strings.TrimPrefix(line, "GATEWAY:"), 0)
				current.GatewayStatus = code
				current.GatewayUp = code >= 200 && code < 500
			}
		}
	}

	return result
}

// Parse helper functions

func parseIntDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// JSON helper functions

func jsonUint64(m map[string]interface{}, key string) uint64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return uint64(val)
	case string:
		n, _ := strconv.ParseUint(val, 10, 64)
		return n
	case json.Number:
		n, _ := val.Int64()
		return uint64(n)
	default:
		return 0
	}
}

func jsonBool(m map[string]interface{}, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true"
	default:
		return false
	}
}
