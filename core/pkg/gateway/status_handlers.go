package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/anonproxy"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// Build info (set via -ldflags at build time; defaults for dev)
var (
	BuildVersion = "dev"
	BuildCommit  = ""
	BuildTime    = ""
)

// checkResult holds the result of a single subsystem health check.
type checkResult struct {
	Status  string `json:"status"`            // "ok", "error", "unavailable"
	Latency string `json:"latency,omitempty"` // e.g. "1.2ms"
	Error   string `json:"error,omitempty"`   // set when Status == "error"
	Peers   int    `json:"peers,omitempty"`   // libp2p peer count
}

// cachedHealthResult caches the aggregate health response for 5 seconds.
type cachedHealthResult struct {
	response   map[string]any
	httpStatus int
	cachedAt   time.Time
}

const healthCacheTTL = 5 * time.Second

// The health and status endpoints are open: DNS membership, load balancers
// and `orama monitor` read them with no credential. What an open endpoint
// shows is status — healthy, degraded, starting — and nothing an attacker
// would want mapped: /v1/health used to list every namespace hosted on the
// node with its internal ports, and /v1/status every peer's id and addresses.
// The detail is still produced, and an operator reads it at
// /v1/operator/health (operatorHealthHandler).

// healthHandler serves the unauthenticated /health and /v1/health.
func (g *Gateway) healthHandler(w http.ResponseWriter, r *http.Request) {
	httpStatus, body := g.healthReport(r.Context())
	writeJSON(w, httpStatus, publicHealth(body))
}

// operatorHealthHandler serves /v1/operator/health: the same report with every
// check's detail and the health of each namespace hosted here.
func (g *Gateway) operatorHealthHandler(w http.ResponseWriter, r *http.Request) {
	if g.operatorHandler == nil {
		writeError(w, http.StatusServiceUnavailable, "this gateway cannot check the operator list")
		return
	}
	if _, ok := g.operatorHandler.Authorize(w, r); !ok {
		return
	}
	httpStatus, body := g.healthReport(r.Context())
	writeJSON(w, httpStatus, body)
}

// publicHealth is the part of a health report an anonymous caller sees: the
// overall status and each check's status. The readiness body a starting
// gateway returns is public already (publicReadiness).
func publicHealth(body map[string]any) map[string]any {
	checks, ok := body["checks"].(map[string]checkResult)
	if !ok {
		return body
	}
	statuses := make(map[string]map[string]string, len(checks))
	for name, c := range checks {
		statuses[name] = map[string]string{"status": c.Status}
	}
	return map[string]any{"status": body["status"], "server": body["server"], "checks": statuses}
}

// healthReport is the full health report and the HTTP status it answers with.
func (g *Gateway) healthReport(ctx context.Context) (int, map[string]any) {
	// A gateway that has not finished starting reports that, and why, instead
	// of the subsystem fan-out below. The checks would mostly pass — the
	// process is up, the port answers — which is exactly the misleading
	// "healthy" that let a gateway with no usable schema stay in rotation.
	// It reports the reason code, and the error behind it stays in the log.
	if state, code, _, since := g.ready.snapshot(); state != ReadinessReady {
		body := publicReadiness(state, code, since)
		body["server"] = g.serverInfo()
		return http.StatusServiceUnavailable, body
	}

	// Serve from cache if fresh
	g.healthCacheMu.RLock()
	cached := g.healthCache
	g.healthCacheMu.RUnlock()
	if cached != nil && time.Since(cached.cachedAt) < healthCacheTTL {
		return cached.httpStatus, cached.response
	}

	checks := g.runHealthChecks(ctx)
	overallStatus := aggregateHealthStatus(checks)
	httpStatus := http.StatusOK
	if overallStatus != "healthy" {
		httpStatus = http.StatusServiceUnavailable
	}
	resp := map[string]any{
		"status": overallStatus,
		"server": g.serverInfo(),
		"checks": checks,
	}
	// Include namespace health if available (populated by namespace health loop)
	if nsHealth := g.getNamespaceHealth(); nsHealth != nil {
		resp["namespaces"] = nsHealth
	}

	g.healthCacheMu.Lock()
	g.healthCache = &cachedHealthResult{response: resp, httpStatus: httpStatus, cachedAt: time.Now()}
	g.healthCacheMu.Unlock()
	return httpStatus, resp
}

// serverInfo is when this gateway started and how long it has been up.
func (g *Gateway) serverInfo() map[string]any {
	return map[string]any{
		"started_at": g.startedAt,
		"uptime":     time.Since(g.startedAt).String(),
	}
}

// runHealthChecks runs every subsystem check in parallel under one 5s budget.
func (g *Gateway) runHealthChecks(parent context.Context) map[string]checkResult {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	type namedResult struct {
		name   string
		result checkResult
	}
	const numChecks = 7
	ch := make(chan namedResult, numChecks)

	// RQLite
	go func() {
		nr := namedResult{name: "rqlite"}
		if g.sqlDB == nil {
			nr.result = checkResult{Status: "unavailable"}
		} else {
			start := time.Now()
			if err := g.sqlDB.PingContext(ctx); err != nil {
				nr.result = g.failedCheck("rqlite", start, err)
			} else {
				nr.result = checkResult{Status: "ok", Latency: time.Since(start).String()}
			}
		}
		ch <- nr
	}()

	// Olric (thread-safe: can be nil or reconnected in background)
	go func() {
		nr := namedResult{name: "olric"}
		g.olricMu.RLock()
		oc := g.olricClient
		g.olricMu.RUnlock()
		if oc == nil {
			nr.result = checkResult{Status: "unavailable"}
		} else {
			start := time.Now()
			if err := oc.Health(ctx); err != nil {
				nr.result = g.failedCheck("olric", start, err)
			} else {
				nr.result = checkResult{Status: "ok", Latency: time.Since(start).String()}
			}
		}
		ch <- nr
	}()

	// IPFS
	go func() {
		nr := namedResult{name: "ipfs"}
		if g.ipfsClient == nil {
			nr.result = checkResult{Status: "unavailable"}
		} else {
			start := time.Now()
			if err := g.ipfsClient.Health(ctx); err != nil {
				nr.result = g.failedCheck("ipfs", start, err)
			} else {
				nr.result = checkResult{Status: "ok", Latency: time.Since(start).String()}
			}
		}
		ch <- nr
	}()

	// LibP2P
	go func() {
		nr := namedResult{name: "libp2p"}
		if g.client == nil {
			nr.result = checkResult{Status: "unavailable"}
		} else if h := g.client.Host(); h == nil {
			nr.result = checkResult{Status: "unavailable"}
		} else {
			peers := len(h.Network().Peers())
			nr.result = checkResult{Status: "ok", Peers: peers}
		}
		ch <- nr
	}()

	// Anonymity proxy: the node's Tor SOCKS port.
	go func() {
		ch <- namedResult{name: anonProxyCheckName, result: anonProxyCheck(anonproxy.Running)}
	}()

	// Vault Guardian: a TCP connect to its port on this node's WireGuard
	// address, the only address it binds. There is no localhost to try instead.
	go func() {
		nr := namedResult{name: "vault"}
		start := time.Now()
		if g.localWireGuardIP == "" {
			nr.result = g.failedCheck("vault", start, errors.New("this gateway has no WireGuard address to reach vault-guardian on"))
			ch <- nr
			return
		}
		vaultAddr := net.JoinHostPort(g.localWireGuardIP, strconv.Itoa(constants.VaultHTTPPort))
		conn, err := net.DialTimeout("tcp", vaultAddr, vaultProbeTimeout)
		if err != nil {
			nr.result = g.failedCheck("vault", start, err)
		} else {
			conn.Close()
			nr.result = checkResult{Status: "ok", Latency: time.Since(start).String()}
		}
		ch <- nr
	}()

	// WireGuard (check wg0 interface exists and has an IP)
	go func() {
		nr := namedResult{name: "wireguard"}
		iface, err := net.InterfaceByName("wg0")
		if err != nil {
			nr.result = checkResult{Status: "error", Error: "wg0 interface not found"}
		} else if addrs, err := iface.Addrs(); err != nil || len(addrs) == 0 {
			nr.result = checkResult{Status: "error", Error: "wg0 has no addresses"}
		} else {
			nr.result = checkResult{Status: "ok"}
		}
		ch <- nr
	}()

	// Collect
	checks := make(map[string]checkResult, numChecks)
	for i := 0; i < numChecks; i++ {
		nr := <-ch
		checks[nr.name] = nr.result
	}
	return checks
}

// vaultProbeTimeout bounds the vault-guardian TCP probe.
const vaultProbeTimeout = 2 * time.Second

// failedCheck records a failed health check. The error is kept: an operator
// reads this report at /v1/operator/health. The unauthenticated /health is
// publicHealth, which keeps each check's status and drops the error.
func (g *Gateway) failedCheck(name string, start time.Time, err error) checkResult {
	g.logger.ComponentWarn(logging.ComponentGeneral, "health check failed",
		zap.String("check", name), zap.Error(err))
	return checkResult{Status: "error", Latency: time.Since(start).String(), Error: err.Error()}
}

// anonProxyCheckName is the /v1/health key that reports the Tor SOCKS port
// behind /v1/proxy/anon, /v1/proxy/tunnel and the anon_fetch host function.
const anonProxyCheckName = "anon_proxy"

// anonProxyCheck reports the Tor SOCKS port. An unreachable port is
// "unavailable", not "error": /v1/health decides DNS membership, and a node
// whose Tor client is down still serves everything but the anonymity proxy.
// orama monitor and the inspector alert on a stopped Tor client instead.
func anonProxyCheck(running func() bool) checkResult {
	start := time.Now()
	if !running() {
		return checkResult{Status: "unavailable"}
	}
	return checkResult{Status: "ok", Latency: time.Since(start).String()}
}

// pingHandler is a lightweight internal endpoint used for peer-to-peer
// health probing over the WireGuard mesh. No subsystem checks — just
// confirms the gateway process is alive.
//
// The prober reads the status code and nothing else. It used to return the
// node's peer id as well, to anyone: the route is open and Caddy proxies every
// path to it from the internet.
func (g *Gateway) pingHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// statusHandler serves the unauthenticated /status and /v1/status: that the
// gateway is up, and since when. It used to embed the network status — this
// node's peer id, its peers, its IPFS and IPFS Cluster peer ids and swarm
// addresses — which is a map of the cluster for anyone who asks. That is at
// /v1/network/status now, for operators and for nodes (networkStatusHandler).
func (g *Gateway) statusHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"server": g.serverInfo(),
	})
}

// versionHandler returns gateway build/runtime information
func (g *Gateway) versionHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    BuildVersion,
		"commit":     BuildCommit,
		"build_time": BuildTime,
		"started_at": g.startedAt,
		"uptime":     time.Since(g.startedAt).String(),
	})
}

// aggregateHealthStatus determines the overall health status from individual checks.
// Critical: rqlite or vault down → "unhealthy"
// Non-critical (olric, ipfs, libp2p, anon_proxy, wireguard) error → "degraded"
// "unavailable" means the client was never configured — not an error.
func aggregateHealthStatus(checks map[string]checkResult) string {
	// Critical services — any error means unhealthy
	for _, name := range []string{"rqlite", "vault"} {
		if c := checks[name]; c.Status == "error" {
			return "unhealthy"
		}
	}
	// Non-critical services — any error means degraded
	for name, c := range checks {
		if name == "rqlite" || name == "vault" {
			continue
		}
		if c.Status == "error" {
			return "degraded"
		}
	}
	return "healthy"
}

// tlsCheckHandler validates if a domain should receive a TLS certificate
// Used by Caddy's on-demand TLS feature to prevent abuse
func (g *Gateway) tlsCheckHandler(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		http.Error(w, "domain parameter required", http.StatusBadRequest)
		return
	}

	baseDomain := g.cfg.BaseDomain

	// Allow any subdomain of our base domain
	if strings.HasSuffix(domain, "."+baseDomain) || domain == baseDomain {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Domain not allowed - only allow subdomains of our base domain
	// Custom domains would need to be verified separately
	http.Error(w, "domain not allowed", http.StatusForbidden)
}
