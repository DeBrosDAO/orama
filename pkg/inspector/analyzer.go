package inspector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// System prompt with architecture context and remediation knowledge.
const systemPrompt = `You are a distributed systems expert analyzing health check results for an Orama Network cluster.

## Architecture
- **RQLite**: Raft consensus SQLite database. Requires N/2+1 quorum for writes. Each node runs one instance.
- **Olric**: Distributed in-memory cache using memberlist protocol. Coordinates via elected coordinator node.
- **IPFS**: Decentralized storage with private swarm (swarm key). Runs Kubo daemon + IPFS Cluster for pinning.
- **CoreDNS + Caddy**: DNS resolution (port 53) and TLS termination (ports 80/443). Only on nameserver nodes.
- **WireGuard**: Mesh VPN connecting all nodes via 10.0.0.0/8 on port 51820. All inter-node traffic goes over WG.
- **Namespaces**: Isolated tenant environments. Each namespace runs its own RQLite + Olric + Gateway on a 5-port block (base+0=RQLite HTTP, +1=Raft, +2=Olric HTTP, +3=Memberlist, +4=Gateway).

## Common Failure Patterns
- If WireGuard is down on a node, ALL services on that node will appear unreachable from other nodes.
- RQLite losing quorum (< N/2+1 voters) means the cluster cannot accept writes. Reads may still work.
- Olric suspects/flapping in logs usually means unstable network between nodes (check WireGuard first).
- IPFS swarm peers dropping to 0 means the node is isolated from the private swarm.
- High TCP retransmission (>2%) indicates packet loss, often due to WireGuard MTU issues.

## Service Management
- ALWAYS use the CLI for service operations: ` + "`sudo orama prod restart`" + `, ` + "`sudo orama prod stop`" + `, ` + "`sudo orama prod start`" + `
- NEVER use raw systemctl commands (they skip important lifecycle hooks).
- For rolling restarts: upgrade followers first, leader LAST, one node at a time.
- Check RQLite leader: ` + "`curl -s localhost:4001/status | python3 -c \"import sys,json; print(json.load(sys.stdin)['store']['raft']['state'])\"`" + `

## Response Format
Respond in this exact structure:

### Root Cause
What is causing these failures? If multiple issues, explain each briefly.

### Impact
What is broken for users right now? Can they still deploy apps, access services?

### Fix
Step-by-step commands to resolve. Include actual node IPs/names from the data when possible.

### Prevention
What could prevent this in the future? (omit if not applicable)`

// SubsystemAnalysis holds the AI analysis for a single subsystem or failure group.
type SubsystemAnalysis struct {
	Subsystem string
	GroupID   string // e.g. "anyone.bootstrapped" — empty when analyzing whole subsystem
	Analysis  string
	Duration  time.Duration
	Error     error
}

// AnalysisResult holds the AI's analysis of check failures.
type AnalysisResult struct {
	Model    string
	Analyses []SubsystemAnalysis
	Duration time.Duration
}

// Analyze sends failures and cluster context to OpenRouter for AI analysis.
// Each subsystem with issues gets its own API call, run in parallel.
func Analyze(results *Results, data *ClusterData, model, apiKey string) (*AnalysisResult, error) {
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("no API key: set --api-key or OPENROUTER_API_KEY env")
	}

	// Group failures and warnings by subsystem
	issues := results.FailuresAndWarnings()
	bySubsystem := map[string][]CheckResult{}
	for _, c := range issues {
		bySubsystem[c.Subsystem] = append(bySubsystem[c.Subsystem], c)
	}

	if len(bySubsystem) == 0 {
		return &AnalysisResult{Model: model}, nil
	}

	// Build healthy summary (subsystems with zero failures/warnings)
	healthySummary := buildHealthySummary(results, bySubsystem)

	// Build collection errors summary
	collectionErrors := buildCollectionErrors(data)

	// Build cluster overview (shared across all calls)
	clusterOverview := buildClusterOverview(data, results)

	// Launch one AI call per subsystem in parallel
	start := time.Now()
	var mu sync.Mutex
	var wg sync.WaitGroup
	var analyses []SubsystemAnalysis

	// Sort subsystems for deterministic ordering
	subsystems := make([]string, 0, len(bySubsystem))
	for sub := range bySubsystem {
		subsystems = append(subsystems, sub)
	}
	sort.Strings(subsystems)

	for _, sub := range subsystems {
		checks := bySubsystem[sub]
		wg.Add(1)
		go func(subsystem string, checks []CheckResult) {
			defer wg.Done()

			prompt := buildSubsystemPrompt(subsystem, checks, data, clusterOverview, healthySummary, collectionErrors)
			subStart := time.Now()
			response, err := callOpenRouter(model, apiKey, prompt)

			sa := SubsystemAnalysis{
				Subsystem: subsystem,
				Duration:  time.Since(subStart),
			}
			if err != nil {
				sa.Error = err
			} else {
				sa.Analysis = response
			}

			mu.Lock()
			analyses = append(analyses, sa)
			mu.Unlock()
		}(sub, checks)
	}
	wg.Wait()

	// Sort by subsystem name for consistent output
	sort.Slice(analyses, func(i, j int) bool {
		return analyses[i].Subsystem < analyses[j].Subsystem
	})

	return &AnalysisResult{
		Model:    model,
		Analyses: analyses,
		Duration: time.Since(start),
	}, nil
}

// AnalyzeGroups sends each failure group to OpenRouter for focused AI analysis.
// Unlike Analyze which sends one call per subsystem, this sends one call per unique
// failure pattern, producing more focused and actionable results.
func AnalyzeGroups(groups []FailureGroup, results *Results, data *ClusterData, model, apiKey string) (*AnalysisResult, error) {
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("no API key: set --api-key or OPENROUTER_API_KEY env")
	}

	if len(groups) == 0 {
		return &AnalysisResult{Model: model}, nil
	}

	// Build shared context
	issuesBySubsystem := map[string][]CheckResult{}
	for _, c := range results.FailuresAndWarnings() {
		issuesBySubsystem[c.Subsystem] = append(issuesBySubsystem[c.Subsystem], c)
	}
	healthySummary := buildHealthySummary(results, issuesBySubsystem)
	collectionErrors := buildCollectionErrors(data)

	start := time.Now()
	var mu sync.Mutex
	var wg sync.WaitGroup
	var analyses []SubsystemAnalysis

	for _, g := range groups {
		wg.Add(1)
		go func(group FailureGroup) {
			defer wg.Done()

			prompt := buildGroupPrompt(group, data, healthySummary, collectionErrors)
			subStart := time.Now()
			response, err := callOpenRouter(model, apiKey, prompt)

			sa := SubsystemAnalysis{
				Subsystem: group.Subsystem,
				GroupID:   group.ID,
				Duration:  time.Since(subStart),
			}
			if err != nil {
				sa.Error = err
			} else {
				sa.Analysis = response
			}

			mu.Lock()
			analyses = append(analyses, sa)
			mu.Unlock()
		}(g)
	}
	wg.Wait()

	// Sort by subsystem then group ID for consistent output
	sort.Slice(analyses, func(i, j int) bool {
		if analyses[i].Subsystem != analyses[j].Subsystem {
			return analyses[i].Subsystem < analyses[j].Subsystem
		}
		return analyses[i].GroupID < analyses[j].GroupID
	})

	return &AnalysisResult{
		Model:    model,
		Analyses: analyses,
		Duration: time.Since(start),
	}, nil
}

func buildGroupPrompt(group FailureGroup, data *ClusterData, healthySummary, collectionErrors string) string {
	var b strings.Builder

	icon := "FAILURE"
	if group.Status == StatusWarn {
		icon = "WARNING"
	}

	b.WriteString(fmt.Sprintf("## %s: %s\n\n", icon, group.Name))
	b.WriteString(fmt.Sprintf("**Check ID:** %s  \n", group.ID))
	b.WriteString(fmt.Sprintf("**Severity:** %s  \n", group.Severity))
	b.WriteString(fmt.Sprintf("**Nodes affected:** %d  \n\n", len(group.Nodes)))

	b.WriteString("**Affected nodes:**\n")
	for _, n := range group.Nodes {
		b.WriteString(fmt.Sprintf("- %s\n", n))
	}
	b.WriteString("\n")

	b.WriteString("**Error messages:**\n")
	for _, m := range group.Messages {
		b.WriteString(fmt.Sprintf("- %s\n", m))
	}
	b.WriteString("\n")

	// Subsystem raw data
	contextData := buildSubsystemContext(group.Subsystem, data)
	if contextData != "" {
		b.WriteString(fmt.Sprintf("## %s Raw Data (all nodes)\n", strings.ToUpper(group.Subsystem)))
		b.WriteString(contextData)
		b.WriteString("\n")
	}

	if healthySummary != "" {
		b.WriteString("## Healthy Subsystems\n")
		b.WriteString(healthySummary)
		b.WriteString("\n")
	}

	if collectionErrors != "" {
		b.WriteString("## Collection Errors\n")
		b.WriteString(collectionErrors)
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("\nAnalyze this specific %s issue. Be concise — focus on this one problem.\n", group.Subsystem))
	return b.String()
}

func buildClusterOverview(data *ClusterData, results *Results) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Nodes: %d\n", len(data.Nodes)))
	for host, nd := range data.Nodes {
		b.WriteString(fmt.Sprintf("- %s (role: %s)\n", host, nd.Node.Role))
	}
	passed, failed, warned, skipped := results.Summary()
	b.WriteString(fmt.Sprintf("\nCheck totals: %d passed, %d failed, %d warnings, %d skipped\n", passed, failed, warned, skipped))
	return b.String()
}

func buildHealthySummary(results *Results, issueSubsystems map[string][]CheckResult) string {
	// Count passes per subsystem
	passBySubsystem := map[string]int{}
	totalBySubsystem := map[string]int{}
	for _, c := range results.Checks {
		totalBySubsystem[c.Subsystem]++
		if c.Status == StatusPass {
			passBySubsystem[c.Subsystem]++
		}
	}

	var b strings.Builder
	for sub, total := range totalBySubsystem {
		if _, hasIssues := issueSubsystems[sub]; hasIssues {
			continue
		}
		passed := passBySubsystem[sub]
		if passed == total && total > 0 {
			b.WriteString(fmt.Sprintf("- %s: all %d checks pass\n", sub, total))
		}
	}

	if b.Len() == 0 {
		return ""
	}
	return b.String()
}

func buildCollectionErrors(data *ClusterData) string {
	var b strings.Builder
	for _, nd := range data.Nodes {
		if len(nd.Errors) > 0 {
			for _, e := range nd.Errors {
				b.WriteString(fmt.Sprintf("- %s: %s\n", nd.Node.Name(), e))
			}
		}
	}
	return b.String()
}

func buildSubsystemPrompt(subsystem string, checks []CheckResult, data *ClusterData, clusterOverview, healthySummary, collectionErrors string) string {
	var b strings.Builder

	b.WriteString("## Cluster Overview\n")
	b.WriteString(clusterOverview)
	b.WriteString("\n")

	// Failures
	var failures, warnings []CheckResult
	for _, c := range checks {
		if c.Status == StatusFail {
			failures = append(failures, c)
		} else if c.Status == StatusWarn {
			warnings = append(warnings, c)
		}
	}

	if len(failures) > 0 {
		b.WriteString(fmt.Sprintf("## %s Failures\n", strings.ToUpper(subsystem)))
		for _, f := range failures {
			node := f.Node
			if node == "" {
				node = "cluster-wide"
			}
			b.WriteString(fmt.Sprintf("- [%s] %s (%s): %s\n", f.Severity, f.Name, node, f.Message))
		}
		b.WriteString("\n")
	}

	if len(warnings) > 0 {
		b.WriteString(fmt.Sprintf("## %s Warnings\n", strings.ToUpper(subsystem)))
		for _, w := range warnings {
			node := w.Node
			if node == "" {
				node = "cluster-wide"
			}
			b.WriteString(fmt.Sprintf("- [%s] %s (%s): %s\n", w.Severity, w.Name, node, w.Message))
		}
		b.WriteString("\n")
	}

	// Subsystem-specific raw data
	contextData := buildSubsystemContext(subsystem, data)
	if contextData != "" {
		b.WriteString(fmt.Sprintf("## %s Raw Data\n", strings.ToUpper(subsystem)))
		b.WriteString(contextData)
		b.WriteString("\n")
	}

	// Healthy subsystems for cross-reference
	if healthySummary != "" {
		b.WriteString("## Healthy Subsystems (for context)\n")
		b.WriteString(healthySummary)
		b.WriteString("\n")
	}

	// Collection errors
	if collectionErrors != "" {
		b.WriteString("## Collection Errors\n")
		b.WriteString(collectionErrors)
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("\nAnalyze the %s issues above.\n", subsystem))
	return b.String()
}

// buildSubsystemContext dispatches to the right context builder.
func buildSubsystemContext(subsystem string, data *ClusterData) string {
	switch subsystem {
	case "rqlite":
		return buildRQLiteContext(data)
	case "olric":
		return buildOlricContext(data)
	case "ipfs":
		return buildIPFSContext(data)
	case "dns":
		return buildDNSContext(data)
	case "wireguard":
		return buildWireGuardContext(data)
	case "system":
		return buildSystemContext(data)
	case "network":
		return buildNetworkContext(data)
	case "namespace":
		return buildNamespaceContext(data)
	case "anyone":
		return buildAnyoneContext(data)
	default:
		return ""
	}
}

func buildRQLiteContext(data *ClusterData) string {
	var b strings.Builder
	for host, nd := range data.Nodes {
		if nd.RQLite == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("### %s\n", host))
		if !nd.RQLite.Responsive {
			b.WriteString("  NOT RESPONDING\n")
			continue
		}
		if s := nd.RQLite.Status; s != nil {
			b.WriteString(fmt.Sprintf("  raft_state=%s term=%d applied=%d commit=%d leader=%s peers=%d voter=%v\n",
				s.RaftState, s.Term, s.AppliedIndex, s.CommitIndex, s.LeaderNodeID, s.NumPeers, s.Voter))
			b.WriteString(fmt.Sprintf("  fsm_pending=%d db_size=%s version=%s goroutines=%d uptime=%s\n",
				s.FsmPending, s.DBSizeFriendly, s.Version, s.Goroutines, s.Uptime))
		}
		if r := nd.RQLite.Readyz; r != nil {
			b.WriteString(fmt.Sprintf("  readyz=%v store=%s leader=%s\n", r.Ready, r.Store, r.Leader))
		}
		if d := nd.RQLite.DebugVars; d != nil {
			b.WriteString(fmt.Sprintf("  query_errors=%d execute_errors=%d leader_not_found=%d snapshot_errors=%d\n",
				d.QueryErrors, d.ExecuteErrors, d.LeaderNotFound, d.SnapshotErrors))
		}
		b.WriteString(fmt.Sprintf("  strong_read=%v\n", nd.RQLite.StrongRead))
		if nd.RQLite.Nodes != nil {
			b.WriteString(fmt.Sprintf("  /nodes (%d members):", len(nd.RQLite.Nodes)))
			for addr, n := range nd.RQLite.Nodes {
				reachable := "ok"
				if !n.Reachable {
					reachable = "UNREACHABLE"
				}
				leader := ""
				if n.Leader {
					leader = " LEADER"
				}
				b.WriteString(fmt.Sprintf(" %s(%s%s)", addr, reachable, leader))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func buildOlricContext(data *ClusterData) string {
	var b strings.Builder
	for host, nd := range data.Nodes {
		if nd.Olric == nil {
			continue
		}
		o := nd.Olric
		b.WriteString(fmt.Sprintf("### %s\n", host))
		b.WriteString(fmt.Sprintf("  active=%v memberlist=%v members=%d coordinator=%s\n",
			o.ServiceActive, o.MemberlistUp, o.MemberCount, o.Coordinator))
		b.WriteString(fmt.Sprintf("  memory=%dMB restarts=%d log_errors=%d suspects=%d flapping=%d\n",
			o.ProcessMemMB, o.RestartCount, o.LogErrors, o.LogSuspects, o.LogFlapping))
	}
	return b.String()
}

func buildIPFSContext(data *ClusterData) string {
	var b strings.Builder
	for host, nd := range data.Nodes {
		if nd.IPFS == nil {
			continue
		}
		ip := nd.IPFS
		repoPct := 0.0
		if ip.RepoMaxBytes > 0 {
			repoPct = float64(ip.RepoSizeBytes) / float64(ip.RepoMaxBytes) * 100
		}
		b.WriteString(fmt.Sprintf("### %s\n", host))
		b.WriteString(fmt.Sprintf("  daemon=%v cluster=%v swarm_peers=%d cluster_peers=%d cluster_errors=%d\n",
			ip.DaemonActive, ip.ClusterActive, ip.SwarmPeerCount, ip.ClusterPeerCount, ip.ClusterErrors))
		b.WriteString(fmt.Sprintf("  repo=%.0f%% (%d/%d bytes) kubo=%s cluster=%s\n",
			repoPct, ip.RepoSizeBytes, ip.RepoMaxBytes, ip.KuboVersion, ip.ClusterVersion))
		b.WriteString(fmt.Sprintf("  swarm_key=%v bootstrap_empty=%v\n", ip.HasSwarmKey, ip.BootstrapEmpty))
	}
	return b.String()
}

func buildDNSContext(data *ClusterData) string {
	var b strings.Builder
	for host, nd := range data.Nodes {
		if nd.DNS == nil {
			continue
		}
		d := nd.DNS
		b.WriteString(fmt.Sprintf("### %s\n", host))
		b.WriteString(fmt.Sprintf("  coredns=%v caddy=%v ports(53=%v,80=%v,443=%v) corefile=%v\n",
			d.CoreDNSActive, d.CaddyActive, d.Port53Bound, d.Port80Bound, d.Port443Bound, d.CorefileExists))
		b.WriteString(fmt.Sprintf("  memory=%dMB restarts=%d log_errors=%d\n",
			d.CoreDNSMemMB, d.CoreDNSRestarts, d.LogErrors))
		b.WriteString(fmt.Sprintf("  resolve: SOA=%v NS=%v(count=%d) wildcard=%v base_A=%v\n",
			d.SOAResolves, d.NSResolves, d.NSRecordCount, d.WildcardResolves, d.BaseAResolves))
		b.WriteString(fmt.Sprintf("  tls: base=%d days, wildcard=%d days\n",
			d.BaseTLSDaysLeft, d.WildTLSDaysLeft))
	}
	return b.String()
}

func buildWireGuardContext(data *ClusterData) string {
	var b strings.Builder
	for host, nd := range data.Nodes {
		if nd.WireGuard == nil {
			continue
		}
		wg := nd.WireGuard
		b.WriteString(fmt.Sprintf("### %s\n", host))
		b.WriteString(fmt.Sprintf("  interface=%v service=%v ip=%s port=%d peers=%d mtu=%d\n",
			wg.InterfaceUp, wg.ServiceActive, wg.WgIP, wg.ListenPort, wg.PeerCount, wg.MTU))
		b.WriteString(fmt.Sprintf("  config=%v perms=%s\n", wg.ConfigExists, wg.ConfigPerms))
		for _, p := range wg.Peers {
			age := "never"
			if p.LatestHandshake > 0 {
				age = fmt.Sprintf("%ds ago", time.Now().Unix()-p.LatestHandshake)
			}
			keyPrefix := p.PublicKey
			if len(keyPrefix) > 8 {
				keyPrefix = keyPrefix[:8] + "..."
			}
			b.WriteString(fmt.Sprintf("  peer %s: allowed=%s handshake=%s rx=%d tx=%d\n",
				keyPrefix, p.AllowedIPs, age, p.TransferRx, p.TransferTx))
		}
	}
	return b.String()
}

func buildSystemContext(data *ClusterData) string {
	var b strings.Builder
	for host, nd := range data.Nodes {
		if nd.System == nil {
			continue
		}
		s := nd.System
		memPct := 0
		if s.MemTotalMB > 0 {
			memPct = s.MemUsedMB * 100 / s.MemTotalMB
		}
		b.WriteString(fmt.Sprintf("### %s\n", host))
		b.WriteString(fmt.Sprintf("  mem=%d%% (%d/%dMB) disk=%d%% load=%s cpus=%d\n",
			memPct, s.MemUsedMB, s.MemTotalMB, s.DiskUsePct, s.LoadAvg, s.CPUCount))
		b.WriteString(fmt.Sprintf("  oom=%d swap=%d/%dMB inodes=%d%% ufw=%v user=%s panics=%d\n",
			s.OOMKills, s.SwapUsedMB, s.SwapTotalMB, s.InodePct, s.UFWActive, s.ProcessUser, s.PanicCount))
		if len(s.FailedUnits) > 0 {
			b.WriteString(fmt.Sprintf("  failed_units: %s\n", strings.Join(s.FailedUnits, ", ")))
		}
	}
	return b.String()
}

func buildNetworkContext(data *ClusterData) string {
	var b strings.Builder
	for host, nd := range data.Nodes {
		if nd.Network == nil {
			continue
		}
		n := nd.Network
		b.WriteString(fmt.Sprintf("### %s\n", host))
		b.WriteString(fmt.Sprintf("  internet=%v default_route=%v wg_route=%v\n",
			n.InternetReachable, n.DefaultRoute, n.WGRouteExists))
		b.WriteString(fmt.Sprintf("  tcp: established=%d time_wait=%d retransmit=%.2f%%\n",
			n.TCPEstablished, n.TCPTimeWait, n.TCPRetransRate))
		if len(n.PingResults) > 0 {
			var failed []string
			for ip, ok := range n.PingResults {
				if !ok {
					failed = append(failed, ip)
				}
			}
			if len(failed) > 0 {
				b.WriteString(fmt.Sprintf("  mesh_ping_failed: %s\n", strings.Join(failed, ", ")))
			} else {
				b.WriteString(fmt.Sprintf("  mesh_ping: all %d peers OK\n", len(n.PingResults)))
			}
		}
	}
	return b.String()
}

func buildNamespaceContext(data *ClusterData) string {
	var b strings.Builder
	for host, nd := range data.Nodes {
		if len(nd.Namespaces) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("### %s (%d namespaces)\n", host, len(nd.Namespaces)))
		for _, ns := range nd.Namespaces {
			b.WriteString(fmt.Sprintf("  ns=%s port_base=%d rqlite=%v(state=%s,ready=%v) olric=%v gateway=%v(status=%d)\n",
				ns.Name, ns.PortBase, ns.RQLiteUp, ns.RQLiteState, ns.RQLiteReady, ns.OlricUp, ns.GatewayUp, ns.GatewayStatus))
		}
	}
	return b.String()
}

func buildAnyoneContext(data *ClusterData) string {
	var b strings.Builder
	for host, nd := range data.Nodes {
		if nd.Anyone == nil {
			continue
		}
		a := nd.Anyone
		if !a.RelayActive && !a.ClientActive {
			continue
		}
		b.WriteString(fmt.Sprintf("### %s\n", host))
		b.WriteString(fmt.Sprintf("  relay=%v client=%v orport=%v socks=%v control=%v\n",
			a.RelayActive, a.ClientActive, a.ORPortListening, a.SocksListening, a.ControlListening))
		if a.RelayActive {
			b.WriteString(fmt.Sprintf("  bootstrap=%d%% fingerprint=%s nickname=%s\n",
				a.BootstrapPct, a.Fingerprint, a.Nickname))
		}
		if len(a.ORPortReachable) > 0 {
			var unreachable []string
			for h, ok := range a.ORPortReachable {
				if !ok {
					unreachable = append(unreachable, h)
				}
			}
			if len(unreachable) > 0 {
				b.WriteString(fmt.Sprintf("  orport_unreachable: %s\n", strings.Join(unreachable, ", ")))
			} else {
				b.WriteString(fmt.Sprintf("  orport: all %d peers reachable\n", len(a.ORPortReachable)))
			}
		}
	}
	return b.String()
}

// OpenRouter API types (OpenAI-compatible)

type openRouterRequest struct {
	Model    string              `json:"model"`
	Messages []openRouterMessage `json:"messages"`
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openRouterResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

func callOpenRouter(model, apiKey, prompt string) (string, error) {
	reqBody := openRouterRequest{
		Model: model,
		Messages: []openRouterMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var orResp openRouterResponse
	if err := json.Unmarshal(body, &orResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if orResp.Error != nil {
		return "", fmt.Errorf("API error: %s", orResp.Error.Message)
	}

	if len(orResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response (raw: %s)", truncate(string(body), 500))
	}

	content := orResp.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("model returned empty response (raw: %s)", truncate(string(body), 500))
	}

	return content, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// PrintAnalysis writes the AI analysis to the output, one section per subsystem.
func PrintAnalysis(result *AnalysisResult, w io.Writer) {
	fmt.Fprintf(w, "\n## AI Analysis (%s)\n", result.Model)
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 70))

	for _, sa := range result.Analyses {
		fmt.Fprintf(w, "\n### %s\n\n", strings.ToUpper(sa.Subsystem))
		if sa.Error != nil {
			fmt.Fprintf(w, "Analysis failed: %v\n", sa.Error)
		} else {
			fmt.Fprintf(w, "%s\n", sa.Analysis)
		}
	}

	fmt.Fprintf(w, "\n%s\n", strings.Repeat("-", 70))
	fmt.Fprintf(w, "(Analysis took %.1fs — %d subsystems analyzed)\n", result.Duration.Seconds(), len(result.Analyses))
}
