package namespace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	olriclib "github.com/olric-data/olric"
	"github.com/olric-data/olric/config"
)

// Readiness probes for tenant services.
//
// A namespace used to be marked `ready` once three systemd units reported
// active. For Type=simple units "active" means the binary was exec'd — it says
// nothing about whether rqlite elected a leader, whether Olric's memberlist
// converged, or whether the gateway can reach either. The gap was papered over
// with time.Sleep(5s), which is a guess that is either too long or, on a slow
// node, wrong.
//
// These probe the thing itself. Each is bounded, cancellable, and reports what
// it last saw rather than only that it gave up — "still Candidate after 60s" and
// "connection refused" need different responses from an operator.

const (
	// readyTimeout bounds a single service's readiness wait. Long enough for a
	// raft election over the WireGuard overlay plus a slow disk, short enough
	// that a provisioning request fails rather than hangs.
	readyTimeout = 60 * time.Second

	// readyPoll is how often a probe re-checks.
	readyPoll = time.Second

	// probeTimeout bounds one HTTP attempt, so a hung connection cannot eat the
	// whole window.
	probeTimeout = 3 * time.Second
)

// awaitReady polls probe until it succeeds or budget elapses, and reports the
// last failure it saw.
//
// The caller's context still cuts it short, but the budget is explicit rather
// than a package constant so a test does not have to wait out a production
// timeout, and so the error names the budget that was actually applied.
func awaitReady(ctx context.Context, budget time.Duration, what string, probe func(context.Context) error) error {
	if budget <= 0 {
		budget = readyTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	var lastErr error
	for {
		// Check the budget BEFORE probing. Probing with an expired context
		// yields "context deadline exceeded", which would then replace the
		// real reason — "still Candidate", "olric unavailable" — with a message
		// that tells an operator nothing.
		if err := ctx.Err(); err != nil {
			if lastErr == nil {
				lastErr = err
			}
			return fmt.Errorf("%s not ready after %s (last: %v)", what, budget, lastErr)
		}

		err := probe(ctx)
		if err == nil {
			return nil
		}
		// Keep the last DIAGNOSTIC failure. Once the budget is nearly spent
		// every probe fails with "context deadline exceeded", which would
		// otherwise overwrite the reason an operator actually needs — "still
		// Candidate", "olric unavailable".
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			lastErr = err
		} else if lastErr == nil {
			lastErr = err
		}

		select {
		case <-ctx.Done():
		case <-time.After(readyPoll):
		}
	}
}

// rqliteReady reports whether the tenant rqlite at ep is participating in raft
// AND can serve a read.
//
// Both halves are needed. The raft state alone can be Leader on a node that
// cannot yet serve — and a query alone cannot distinguish "no leader yet" from
// "wrong port", because rqlite binds its HTTP listener long before it has
// elected anything.
func rqliteReady(ctx context.Context, ep rqlite.Endpoint) error {
	hostPort := ep.HostPort()
	state, err := rqlite.RaftState(ctx, ep)
	if err != nil {
		return fmt.Errorf("read raft state from %s: %w", hostPort, err)
	}
	switch strings.ToLower(state) {
	case "leader", "follower":
	default:
		return fmt.Errorf("%s is in raft state %q, want Leader or Follower", hostPort, state)
	}

	// The credentials ride in the URL; net/http strips them from its errors.
	body, err := httpGet(ctx, ep.CredentialedURL()+"/db/query?q=SELECT%201")
	if err != nil {
		return fmt.Errorf("query %s: %w", hostPort, err)
	}

	// rqlite answers HTTP 200 with the failure inside the body, so the status
	// code says nothing on its own.
	var resp struct {
		Results []struct {
			Error string `json:"error"`
		} `json:"results"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("decode query response from %s: %w", hostPort, err)
	}
	if resp.Error != "" {
		return fmt.Errorf("%s cannot serve a read: %s", hostPort, resp.Error)
	}
	for _, r := range resp.Results {
		if r.Error != "" {
			return fmt.Errorf("%s cannot serve a read: %s", hostPort, r.Error)
		}
	}
	return nil
}

// olricReady reports whether the tenant Olric at hostPort is answering.
//
// hostPort is the client bind port (server.bindPort). Olric 0.7 speaks its own
// protocol there. It has no HTTP server, so GET /api/v1/stats is answered with
// a protocol error whose text is not an HTTP status line — provisioning then
// waits out the minute and rolls the namespace back. Membership is deliberately
// not asserted: a one-node namespace has one member, and a multi-node namespace
// converges after the processes are up.
func olricReady(ctx context.Context, hostPort string) error {
	cfg := config.NewClient()
	cfg.DialTimeout = probeTimeout
	cfg.ReadTimeout = probeTimeout
	cfg.WriteTimeout = probeTimeout
	cfg.MaxRetries = -1
	client, err := olriclib.NewClusterClient([]string{hostPort},
		olriclib.WithLogger(log.New(io.Discard, "", 0)),
		olriclib.WithConfig(cfg),
	)
	if err != nil {
		return fmt.Errorf("olric at %s: %w", hostPort, err)
	}
	defer client.Close(ctx)
	if _, err := client.Stats(ctx, hostPort); err != nil {
		return fmt.Errorf("olric at %s: %w", hostPort, err)
	}
	return nil
}

// gatewayReady reports whether the tenant gateway at hostPort can reach its own
// dependencies.
//
// This is the probe that makes `ready` mean something: the gateway is the only
// component that talks to both rqlite and Olric, so its health endpoint is the
// first place the three are known to work together.
func gatewayReady(ctx context.Context, hostPort string) error {
	body, err := httpGet(ctx, fmt.Sprintf("http://%s/v1/health", hostPort))
	if err != nil {
		return fmt.Errorf("gateway at %s: %w", hostPort, err)
	}

	var health struct {
		Status   string            `json:"status"`
		Services map[string]string `json:"services"`
	}
	if err := json.Unmarshal(body, &health); err != nil {
		return fmt.Errorf("decode health from %s: %w", hostPort, err)
	}

	var unhealthy []string
	for name, state := range health.Services {
		if !healthyState(state) {
			unhealthy = append(unhealthy, fmt.Sprintf("%s=%s", name, state))
		}
	}
	if len(unhealthy) > 0 {
		return fmt.Errorf("gateway at %s reports %s", hostPort, strings.Join(unhealthy, ", "))
	}
	if health.Status != "" && !healthyState(health.Status) {
		return fmt.Errorf("gateway at %s reports status %q", hostPort, health.Status)
	}
	return nil
}

// gatewayServingBudget bounds how long a restarted gateway gets to start
// answering its health endpoint.
const gatewayServingBudget = 30 * time.Second

// awaitGatewayServing waits until this node's copy of the namespace gateway has
// finished starting, so a restart that reports success has left a gateway that
// serves.
//
// /v1/health answers 503 for a gateway that is merely degraded (a non-critical
// subsystem in error) as well as for one that is starting or blocked. Only the
// second kind is a restart that has not finished; the body tells them apart, so
// a degraded gateway counts as serving and aggregate health stays the health
// monitor's concern.
func awaitGatewayServing(ctx context.Context, namespace string, port int, budget time.Duration) error {
	url := namespaceGatewayHealthURL(namespace, port)
	return awaitReady(ctx, budget, fmt.Sprintf("restarted %s gateway at %s", namespace, url), func(ctx context.Context) error {
		code, body, err := httpGetAny(ctx, url)
		if err != nil {
			return err
		}
		if code >= 200 && code < 300 {
			return nil
		}
		var report struct {
			Status string `json:"status"`
		}
		if code == http.StatusServiceUnavailable && json.Unmarshal(body, &report) == nil {
			switch report.Status {
			case "degraded", "unhealthy":
				return nil
			}
		}
		return fmt.Errorf("HTTP %d status %q", code, report.Status)
	})
}

// healthyState reads the vocabulary the health endpoint uses.
func healthyState(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ok", "healthy", "up", "ready", "connected":
		return true
	}
	return false
}

// httpGet performs one bounded GET and returns the body, treating any non-2xx
// as a failure.
func httpGet(ctx context.Context, url string) ([]byte, error) {
	code, body, err := httpGetAny(ctx, url)
	if err != nil {
		return nil, err
	}
	if code < 200 || code >= 300 {
		return nil, fmt.Errorf("HTTP %d", code)
	}
	return body, nil
}

// httpGetAny performs one bounded GET and returns the status code and body
// whatever the code is.
func httpGetAny(ctx context.Context, url string) (int, []byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := tlsutil.NewHTTPClient(probeTimeout).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// nodeInternalIP resolves a node's WireGuard overlay address.
//
// Looked up rather than passed in because ServiceDriver.Ready is given a node
// id, and the overlay address is what a probe has to dial: tenant service ports
// are reachable only there.
func (cm *ClusterManager) nodeInternalIP(nodeID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	var rows []struct {
		InternalIP string `db:"internal_ip"`
	}
	if err := cm.db.Query(ctx, &rows,
		`SELECT COALESCE(internal_ip, '') AS internal_ip FROM dns_nodes WHERE id = ?`, nodeID); err != nil {
		return "", fmt.Errorf("look up the overlay address of %s: %w", nodeID, err)
	}
	if len(rows) == 0 || rows[0].InternalIP == "" {
		return "", fmt.Errorf("node %s has no overlay address recorded, so nothing can probe it", nodeID)
	}
	return rows[0].InternalIP, nil
}

// probeTCP reports whether something is accepting connections at addr.
//
// The weakest useful check, and only appropriate where there is no protocol to
// speak — a media port, say. Service readiness uses the probes above instead:
// a listening socket says nothing about whether the service behind it works.
func probeTCP(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}
