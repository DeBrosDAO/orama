package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// NamespaceServiceHealth represents the health of a single namespace service.
type NamespaceServiceHealth struct {
	Status  string `json:"status"`
	Port    int    `json:"port"`
	Latency string `json:"latency,omitempty"`
	Error   string `json:"error,omitempty"`
}

// NamespaceHealth represents the health of a namespace on this node.
type NamespaceHealth struct {
	Status   string                            `json:"status"` // "healthy", "degraded", "unhealthy"
	Services map[string]NamespaceServiceHealth `json:"services"`
}

// namespaceHealthState holds the cached namespace health data.
type namespaceHealthState struct {
	mu    sync.RWMutex
	cache map[string]*NamespaceHealth // namespace_name → health
	// healthyStreak counts consecutive healthy local probes per namespace. Used
	// to damp the DNS reclaim so a flapping service cannot flap DNS.
	healthyStreak map[string]int
}

// healthyProbesBeforeDNSReclaim is how many consecutive healthy 30s probes a
// namespace must pass before this node re-advertises itself in DNS. Three
// probes (~90s) is long enough that a service restarting mid-probe does not
// trigger a reclaim, short enough to recover well inside a human response time.
const healthyProbesBeforeDNSReclaim = 3

// startNamespaceHealthLoop runs two periodic tasks:
//  1. Every 30s: probe local namespace services and cache health state
//  2. Every 1h: (leader-only) check for under-provisioned namespaces and trigger repair
func (g *Gateway) startNamespaceHealthLoop(ctx context.Context) {
	g.nsHealth = &namespaceHealthState{
		cache: make(map[string]*NamespaceHealth),
	}

	probeTicker := time.NewTicker(30 * time.Second)
	reconcileTicker := time.NewTicker(5 * time.Minute)
	defer probeTicker.Stop()
	defer reconcileTicker.Stop()

	// Initial probe after a short delay (let services start)
	time.Sleep(5 * time.Second)
	g.probeLocalNamespaces(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-probeTicker.C:
			g.probeLocalNamespaces(ctx)
		case <-reconcileTicker.C:
			g.reconcileNamespaces(ctx)
		}
	}
}

// getNamespaceHealth returns the cached namespace health for the /v1/health response.
func (g *Gateway) getNamespaceHealth() map[string]*NamespaceHealth {
	if g.nsHealth == nil {
		return nil
	}
	g.nsHealth.mu.RLock()
	defer g.nsHealth.mu.RUnlock()

	if len(g.nsHealth.cache) == 0 {
		return nil
	}

	// Return a copy to avoid data races
	result := make(map[string]*NamespaceHealth, len(g.nsHealth.cache))
	for k, v := range g.nsHealth.cache {
		result[k] = v
	}
	return result
}

// probeLocalNamespaces discovers which namespaces this node hosts and checks their services.
func (g *Gateway) probeLocalNamespaces(ctx context.Context) {
	if g.sqlDB == nil || g.nodePeerID == "" {
		return
	}

	query := `
		SELECT nc.namespace_name, npa.rqlite_http_port, npa.olric_http_port, npa.gateway_http_port
		FROM namespace_port_allocations npa
		JOIN namespace_clusters nc ON npa.namespace_cluster_id = nc.id
		WHERE npa.node_id = ? AND nc.status = 'ready'
	`
	rows, err := g.sqlDB.QueryContext(ctx, query, g.nodePeerID)
	if err != nil {
		g.logger.ComponentWarn(logging.ComponentGeneral, "Failed to query local namespace allocations",
			zap.Error(err))
		return
	}
	defer rows.Close()

	health := make(map[string]*NamespaceHealth)
	for rows.Next() {
		var name string
		var rqlitePort, olricPort, gatewayPort int
		if err := rows.Scan(&name, &rqlitePort, &olricPort, &gatewayPort); err != nil {
			continue
		}

		nsHealth := &NamespaceHealth{
			Services: make(map[string]NamespaceServiceHealth),
		}

		// Probe RQLite (HTTP on localhost)
		nsHealth.Services["rqlite"] = probeTCP("127.0.0.1", rqlitePort)

		// Probe Olric HTTP API (binds to WireGuard IP)
		olricHost := g.localWireGuardIP
		if olricHost == "" {
			olricHost = "127.0.0.1"
		}
		nsHealth.Services["olric"] = probeTCP(olricHost, olricPort)

		// Probe Gateway (HTTP on all interfaces)
		nsHealth.Services["gateway"] = probeTCP("127.0.0.1", gatewayPort)

		// Aggregate status
		nsHealth.Status = "healthy"
		for _, svc := range nsHealth.Services {
			if svc.Status == "error" {
				nsHealth.Status = "unhealthy"
				break
			}
		}

		health[name] = nsHealth
	}

	g.nsHealth.mu.Lock()
	g.nsHealth.cache = health
	g.nsHealth.mu.Unlock()

	g.reclaimStaleNamespaceDNS(ctx, health)
}

// reclaimStaleNamespaceDNS re-advertises this node in the `ns-<ns>` round-robin
// when its own record was soft-disabled and nothing has refreshed that decision
// since.
//
// DisableNamespaceRecord writes durable state, but the only re-enable path
// (HandleSuspectRecovery) fires on an IN-MEMORY suspect→healthy transition in
// the node health monitor. A restart clears that map, so the transition never
// happens and the row stays disabled permanently — a healthy gateway silently
// removed from DNS with no path back. Reclaiming here is the level-triggered
// counterpart to that edge.
//
// Deliberately conservative:
//   - only namespaces this node currently serves, and only when the local
//     probe says every one of their services is healthy;
//   - only after healthyProbesBeforeDNSReclaim consecutive healthy probes, so a
//     flapping service does not flap DNS;
//   - only this node's own row, and only when the disable is already stale, so
//     a live peer verdict is never overridden (see ReclaimStaleNamespaceHostRecord).
func (g *Gateway) reclaimStaleNamespaceDNS(ctx context.Context, health map[string]*NamespaceHealth) {
	if g.sqlDB == nil || g.nodePeerID == "" || g.nsHealth == nil {
		return
	}

	g.nsHealth.mu.Lock()
	if g.nsHealth.healthyStreak == nil {
		g.nsHealth.healthyStreak = make(map[string]int)
	}
	ready := make([]string, 0, len(health))
	for name, h := range health {
		if h == nil || h.Status != "healthy" {
			delete(g.nsHealth.healthyStreak, name)
			continue
		}
		g.nsHealth.healthyStreak[name]++
		if g.nsHealth.healthyStreak[name] >= healthyProbesBeforeDNSReclaim {
			ready = append(ready, name)
		}
	}
	// Drop streaks for namespaces this node no longer serves.
	for name := range g.nsHealth.healthyStreak {
		if _, still := health[name]; !still {
			delete(g.nsHealth.healthyStreak, name)
		}
	}
	g.nsHealth.mu.Unlock()

	if len(ready) == 0 {
		return
	}

	publicIP, err := g.localNodePublicIP(ctx)
	if err != nil || publicIP == "" {
		// Not fatal and not worth a warning every 30s — the next probe retries,
		// which is the whole point of being level-triggered.
		g.logger.ComponentDebug(logging.ComponentGeneral,
			"Skipping namespace DNS reclaim: local public IP unavailable",
			zap.Error(err))
		return
	}

	baseDomain := g.cfg.BaseDomain
	if baseDomain == "" {
		return // without the zone we cannot name the record; nothing safe to do
	}

	now := time.Now()
	cutoff := now.Add(-staleDisableReclaimAfter)
	for _, name := range ready {
		for _, fqdn := range []string{
			fmt.Sprintf("ns-%s.%s.", name, baseDomain),
			fmt.Sprintf("*.ns-%s.%s.", name, baseDomain),
		} {
			res, rerr := g.sqlDB.ExecContext(ctx, reclaimStaleNamespaceHostRecordSQL, now, fqdn, publicIP, cutoff)
			if rerr != nil {
				g.logger.ComponentWarn(logging.ComponentGeneral,
					"Failed to reclaim stale namespace DNS record",
					zap.String("namespace", name), zap.String("fqdn", fqdn), zap.Error(rerr))
				continue
			}
			if n, aerr := res.RowsAffected(); aerr == nil && n > 0 {
				g.logger.ComponentInfo(logging.ComponentGeneral,
					"Re-enabled this node's namespace DNS record after a stale disable",
					zap.String("namespace", name),
					zap.String("fqdn", fqdn),
					zap.String("ip", publicIP))
			}
		}
	}
}

// staleDisableReclaimAfter is how long a soft-disabled namespace-host record
// must have sat untouched before a locally-healthy node may re-enable its own
// row. It comfortably exceeds the health monitor's suspect cadence (3 missed
// probes at 10s) so a LIVE verdict is never overridden: a monitor that still
// considers this node suspect keeps refreshing updated_at, holding the row
// outside this window indefinitely.
const staleDisableReclaimAfter = 10 * time.Minute

// reclaimStaleNamespaceHostRecordSQL re-activates exactly one node's own
// namespace-host A record, and only when the disable has gone stale.
//
// The is_active=0 and updated_at guards are what make this safe to run
// unattended on every node: a no-op on an already-active row, and a no-op on a
// row some live health monitor is actively holding down.
const reclaimStaleNamespaceHostRecordSQL = `UPDATE dns_records
	SET is_active = 1, updated_at = ?
	WHERE fqdn = ? AND record_type = 'A' AND value = ?
	  AND is_active = 0
	  AND updated_at < ?`

// localNodePublicIP resolves this node's public IP from dns_nodes — the value
// that appears in the A records.
func (g *Gateway) localNodePublicIP(ctx context.Context) (string, error) {
	var ip string
	row := g.sqlDB.QueryRowContext(ctx, `SELECT ip_address FROM dns_nodes WHERE id = ?`, g.nodePeerID)
	if err := row.Scan(&ip); err != nil {
		return "", err
	}
	return ip, nil
}

// reconcileNamespaces checks all namespaces for under-provisioning and triggers repair.
// Only runs on the RQLite leader to avoid duplicate repairs.
func (g *Gateway) reconcileNamespaces(ctx context.Context) {
	if g.sqlDB == nil || g.nodeRecoverer == nil {
		return
	}

	// Only the leader should run reconciliation
	if !g.isRQLiteLeader(ctx) {
		return
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "Running namespace reconciliation check")

	// Query all ready namespaces with their expected and actual node counts
	query := `
		SELECT nc.namespace_name,
			nc.rqlite_node_count + nc.olric_node_count + nc.gateway_node_count AS expected_services,
			(SELECT COUNT(*) FROM namespace_cluster_nodes ncn
			 WHERE ncn.namespace_cluster_id = nc.id AND ncn.status = 'running') AS actual_services
		FROM namespace_clusters nc
		WHERE nc.status = 'ready' AND nc.namespace_name != 'default'
	`
	rows, err := g.sqlDB.QueryContext(ctx, query)
	if err != nil {
		g.logger.ComponentWarn(logging.ComponentGeneral, "Failed to query namespaces for reconciliation",
			zap.Error(err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var expected, actual int
		if err := rows.Scan(&name, &expected, &actual); err != nil {
			continue
		}

		if actual < expected {
			g.logger.ComponentWarn(logging.ComponentGeneral, "Namespace under-provisioned, triggering repair",
				zap.String("namespace", name),
				zap.Int("expected_services", expected),
				zap.Int("actual_services", actual),
			)
			if err := g.nodeRecoverer.RepairCluster(ctx, name); err != nil {
				g.logger.ComponentError(logging.ComponentGeneral, "Namespace repair failed",
					zap.String("namespace", name),
					zap.Error(err),
				)
			} else {
				g.logger.ComponentInfo(logging.ComponentGeneral, "Namespace repair completed",
					zap.String("namespace", name),
				)
			}
		}
	}
}

// isRQLiteLeader checks whether this node is the current Raft leader.
func (g *Gateway) isRQLiteLeader(ctx context.Context) bool {
	dsn := g.cfg.RQLiteDSN
	if dsn == "" {
		dsn = "http://localhost:5001"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dsn+"/status", nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var status struct {
		Store struct {
			Raft struct {
				State string `json:"state"`
			} `json:"raft"`
		} `json:"store"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return false
	}

	return status.Store.Raft.State == "Leader"
}

// probeTCP checks if a port is listening by attempting a TCP connection.
func probeTCP(host string, port int) NamespaceServiceHealth {
	start := time.Now()
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	latency := time.Since(start)

	if err != nil {
		return NamespaceServiceHealth{
			Status:  "error",
			Port:    port,
			Latency: latency.String(),
			Error:   "port not reachable",
		}
	}
	conn.Close()

	return NamespaceServiceHealth{
		Status:  "ok",
		Port:    port,
		Latency: latency.String(),
	}
}
