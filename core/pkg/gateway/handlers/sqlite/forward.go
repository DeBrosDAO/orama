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
	"go.uber.org/zap"
)

// sqliteGatewayPort is the index gateway the forward calls on the home node's
// overlay address. Tests point it at a local server.
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
	ip, err := h.homeOverlayIP(r.Context(), homeNodeID)
	if err != nil {
		h.logger.Warn("sqlite forward has no overlay address for the home node",
			zap.String("home_node", homeNodeID),
			zap.Error(err),
		)
		return false
	}

	dest := fmt.Sprintf("http://%s:%d%s", ip, sqliteGatewayPort, r.URL.RequestURI())
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
	if authz := r.Header.Get("Authorization"); authz != "" {
		req.Header.Set("Authorization", authz)
	}
	req.Header.Set(forwardHeader, "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.logger.Warn("sqlite forward did not reach the home node",
			zap.String("home_node", homeNodeID),
			zap.String("overlay", ip),
			zap.Error(err),
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

// homeOverlayIP is the WireGuard address of a node, from the registry.
// Only a private address is used, so the hop stays on the overlay.
func (h *SQLiteHandler) homeOverlayIP(ctx context.Context, nodeID string) (string, error) {
	var rows []struct {
		IP string `db:"internal_ip"`
	}
	err := h.db.Query(ctx, &rows,
		`SELECT internal_ip FROM dns_nodes WHERE id = ? AND status = 'active' LIMIT 1`, nodeID)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("node %s has no overlay address", nodeID)
	}
	ip := net.ParseIP(strings.TrimSpace(rows[0].IP))
	// Private covers the overlay. Loopback is not a route to another node, but
	// it is not a public address either; a registry row that says so can only
	// reach this machine, and the forward header stops that hop repeating.
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return "", fmt.Errorf("node %s overlay address is not a private IP", nodeID)
	}
	return ip.String(), nil
}
