package sqlite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"go.uber.org/zap"
)

// sqliteGatewayPort is the index gateway port the forward calls on the home
// node's overlay address when this gateway is the index gateway. A namespace
// gateway forwards to that namespace's own gateway port instead. Tests point it
// at a local server.
var sqliteGatewayPort = constants.GatewayAPIPort

// forwardHeader marks a request this gateway already sent to the database's
// home node. The home serves it and does not forward it again.
const forwardHeader = "X-Orama-Forwarded"

// forwardToHome sends the original request to the node that holds the
// database file and writes that node's answer back.
//
// It returns false when this request was already forwarded, or when the
// registry has no private address for the home node. The caller then says
// the database is elsewhere. A home that does not answer is written here:
// serving the call from the local disk would be a different database.
func (h *SQLiteHandler) forwardToHome(w http.ResponseWriter, r *http.Request, body []byte, homeNodeID string) bool {
	if r.Header.Get(forwardHeader) != "" {
		return false
	}
	ip, port, err := h.homeGateway(r.Context(), homeNodeID, namespaceFromRequest(r))
	if err != nil {
		h.logger.Warn("sqlite forward has no gateway address for the home node",
			zap.String("home_node", homeNodeID),
			zap.Error(err),
		)
		return false
	}

	dest := fmt.Sprintf("http://%s:%d%s", ip, port, r.URL.RequestURI())
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, r.Method, dest, bytes.NewReader(body))
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "Database home node could not be asked")
		return true
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	// Every credential form the home gateway authenticates travels with the
	// request: it re-authenticates the caller, so one that came with a key
	// and arrived without it was refused there (401) for a request this node
	// had accepted. A ?api_key= parameter travels in the request URI.
	for _, name := range forwardedCredentialHeaders {
		if v := r.Header.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}
	req.Header.Set(forwardHeader, "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.logger.Warn("sqlite forward did not reach the home node",
			zap.String("home_node", homeNodeID),
			zap.String("overlay", ip),
			zap.String("error", httputil.FailureReason(err)),
		)
		writeJSONError(w, http.StatusBadGateway, "Database home node did not answer")
		return true
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("X-Orama-Home-Node", homeNodeID)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return true
}

// namespaceFromRequest is the namespace the auth middleware resolved.
func namespaceFromRequest(r *http.Request) string {
	ns, _ := r.Context().Value(ctxkeys.NamespaceOverride).(string)
	return ns
}

// forwardedCredentialHeaders are the credential headers a forward to the home
// node carries.
var forwardedCredentialHeaders = []string{"Authorization", "X-API-Key"}

// homeGateway is the overlay address and port of the gateway that serves this
// namespace on the home node.
//
// The registry is the main cluster's rqlite (h.registry): on a namespace
// gateway h.db is the namespace's own rqlite, whose dns_nodes is permanently
// empty. A namespace gateway serves its database routes on a port of its own
// on each node (namespace_port_allocations), not on the index gateway, which
// holds none of the namespace's databases. Only a private address is used, so
// the hop stays on the overlay.
func (h *SQLiteHandler) homeGateway(ctx context.Context, nodeID, namespace string) (string, int, error) {
	if h.registry == nil {
		return "", 0, fmt.Errorf("this gateway has no cluster registry handle to find node %s in", nodeID)
	}
	var rows []struct {
		IP   string `db:"internal_ip"`
		Port int    `db:"gateway_port"`
	}
	var err error
	port := sqliteGatewayPort
	if h.namespaceGateway {
		err = h.registry.Query(ctx, &rows, namespaceGatewayOnNodeQuery, namespace, nodeID)
	} else {
		err = h.registry.Query(ctx, &rows,
			`SELECT COALESCE(internal_ip, ip_address) AS internal_ip, 0 AS gateway_port FROM dns_nodes WHERE id = ? AND status = 'active' LIMIT 1`, nodeID)
	}
	if err != nil {
		return "", 0, fmt.Errorf("read the home node's gateway address: %w", err)
	}
	if len(rows) == 0 {
		return "", 0, fmt.Errorf("node %s has no active gateway for namespace %q in the registry", nodeID, namespace)
	}
	if h.namespaceGateway {
		port = rows[0].Port
	}
	ip := net.ParseIP(strings.TrimSpace(rows[0].IP))
	// Private covers the overlay. Loopback is not a route to another node, but
	// it is not a public address either; a registry row that says so can only
	// reach this machine, and the forward header stops that hop repeating.
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return "", 0, fmt.Errorf("node %s overlay address is not a private IP", nodeID)
	}
	if port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("node %s has no gateway port for namespace %q", nodeID, namespace)
	}
	return ip.String(), port, nil
}

// namespaceGatewayOnNodeQuery finds the namespace gateway a namespace runs on
// one node. It selects the same live-gateway rows the namespace proxy does
// (gateway/middleware.go namespaceGatewayTargetsQuery), narrowed to one node.
const namespaceGatewayOnNodeQuery = `
	SELECT COALESCE(dn.internal_ip, dn.ip_address) AS internal_ip, npa.gateway_http_port AS gateway_port
	FROM namespace_port_allocations npa
	JOIN namespace_clusters nc ON npa.namespace_cluster_id = nc.id
	JOIN dns_nodes dn ON npa.node_id = dn.id
	JOIN namespace_cluster_nodes ncn
	  ON ncn.namespace_cluster_id = nc.id
	 AND ncn.node_id = npa.node_id
	 AND ncn.role = 'gateway'
	WHERE nc.namespace_name = ?
	  AND npa.node_id = ?
	  AND nc.status IN ('ready', 'degraded')
	  AND ncn.status = 'running'
	  AND dn.status = 'active'
	LIMIT 1`
