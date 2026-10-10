package enroll

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// HandleNodeStatus proxies GET /v1/node/status to the agent over WireGuard.
// Query param: ?node_id=<node_id> or ?wg_ip=<wg_ip>
func (h *Handler) HandleNodeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	wgIP, err := h.resolveNodeIP(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Proxy to agent's status endpoint
	body, statusCode, err := h.proxyToAgent(wgIP, "GET", "/v1/agent/status", nil)
	if err != nil {
		h.logger.Warn("failed to proxy status request", zap.String("wg_ip", wgIP), zap.Error(err))
		http.Error(w, "node unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	w.Write(body)
}

// HandleNodeCommand proxies POST /v1/node/command to the agent over WireGuard.
func (h *Handler) HandleNodeCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	wgIP, err := h.resolveNodeIP(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Read command body
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	cmdBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Proxy to agent's command endpoint
	body, statusCode, err := h.proxyToAgent(wgIP, "POST", "/v1/agent/command", cmdBody)
	if err != nil {
		h.logger.Warn("failed to proxy command", zap.String("wg_ip", wgIP), zap.Error(err))
		http.Error(w, "node unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	w.Write(body)
}

// HandleNodeLogs proxies GET /v1/node/logs to the agent over WireGuard.
// Query params: ?node_id=<id>&service=<name>&lines=<n>
func (h *Handler) HandleNodeLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	wgIP, err := h.resolveNodeIP(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Build query string for agent
	service := r.URL.Query().Get("service")
	lines := r.URL.Query().Get("lines")
	agentPath := "/v1/agent/logs"
	params := []string{}
	if service != "" {
		params = append(params, "service="+service)
	}
	if lines != "" {
		params = append(params, "lines="+lines)
	}
	if len(params) > 0 {
		agentPath += "?" + strings.Join(params, "&")
	}

	body, statusCode, err := h.proxyToAgent(wgIP, "GET", agentPath, nil)
	if err != nil {
		h.logger.Warn("failed to proxy logs request", zap.String("wg_ip", wgIP), zap.Error(err))
		http.Error(w, "node unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	w.Write(body)
}

// HandleNodeLeave handles POST /v1/node/leave — graceful node departure.
// Stops services on the leaving node and removes it from the WireGuard mesh.
// Shamir redistribution is not implemented (guardian clustering is unwired).
func (h *Handler) HandleNodeLeave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		NodeID string `json:"node_id"`
		WGIP   string `json:"wg_ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	wgIP := req.WGIP
	if wgIP == "" && req.NodeID != "" {
		resolved, err := h.nodeIDToWGIP(r.Context(), req.NodeID)
		if err != nil {
			http.Error(w, "node not found: "+err.Error(), http.StatusNotFound)
			return
		}
		wgIP = resolved
	}
	if wgIP == "" {
		http.Error(w, "node_id or wg_ip is required", http.StatusBadRequest)
		return
	}

	h.logger.Info("node leave requested", zap.String("wg_ip", wgIP))

	ctx := r.Context()

	_, status, err := h.proxyToAgent(wgIP, "POST", "/v1/agent/command",
		[]byte(`{"action":"stop"}`))
	if err != nil {
		h.logger.Warn("failed to stop services on leaving node", zap.Error(err))
	} else if status >= 400 {
		h.logger.Warn("leaving node refused stop", zap.Int("status", status))
	}

	if _, err := h.rqliteClient.Exec(ctx,
		"DELETE FROM wireguard_peers WHERE wg_ip = ?", wgIP); err != nil {
		h.logger.Error("failed to remove WG peer from database", zap.Error(err))
		http.Error(w, "failed to remove peer", http.StatusInternalServerError)
		return
	}

	h.removeWGPeerLocally(wgIP)

	h.logger.Info("node removed from cluster", zap.String("wg_ip", wgIP))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "removed",
		"wg_ip":  wgIP,
	})
}

// proxyToAgent sends an HTTP request to the OramaOS agent over WireGuard,
// carrying the credential that node minted for this gateway at enrollment.
//
// Being on the mesh is not the credential: every namespace's services are on
// that mesh, and one of them being compromised must not mean every node's
// services can be restarted. A node with no stored token cannot be commanded,
// and this says so rather than sending a request the agent will refuse.
func (h *Handler) proxyToAgent(wgIP, method, path string, body []byte) ([]byte, int, error) {
	url := fmt.Sprintf("http://%s:9998%s", wgIP, path)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	token, err := h.AgentToken(ctx, nodeIDForOverlayIP(wgIP))
	if err != nil {
		return nil, 0, err
	}

	var reqBody io.Reader
	if body != nil {
		reqBody = strings.NewReader(string(body))
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request to agent failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read agent response: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

// resolveNodeIP extracts the WG IP from query parameters.
func (h *Handler) resolveNodeIP(r *http.Request) (string, error) {
	wgIP := r.URL.Query().Get("wg_ip")
	if wgIP != "" {
		return wgIP, nil
	}

	nodeID := r.URL.Query().Get("node_id")
	if nodeID != "" {
		return h.nodeIDToWGIP(r.Context(), nodeID)
	}

	return "", fmt.Errorf("wg_ip or node_id query parameter is required")
}

// nodeIDToWGIP resolves a node_id to its WireGuard IP.
func (h *Handler) nodeIDToWGIP(ctx context.Context, nodeID string) (string, error) {
	var rows []struct {
		WGIP string `db:"wg_ip"`
	}
	if err := h.rqliteClient.Query(ctx, &rows,
		"SELECT wg_ip FROM wireguard_peers WHERE node_id = ?", nodeID); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("no node found with id %s", nodeID)
	}
	return rows[0].WGIP, nil
}

// removeWGPeerLocally removes the peer holding wgIP from wg0 and wg0.conf,
// through orama-privhelper. It used to run `wg show`/`wg set` directly, which
// the unprivileged gateway cannot, and matched the address with
// strings.Contains, so removing 10.0.0.1 also removed 10.0.0.12.
func (h *Handler) removeWGPeerLocally(wgIP string) {
	if err := privhelper.RemoveWireGuardPeer(wgIP + "/32"); err != nil {
		log.Printf("failed to remove WG peer %s: %v", wgIP, err)
		return
	}
	log.Printf("removed WG peer %s", wgIP)
}

// nodeIDForOverlayIP is the node id an enrolled OramaOS node was given.
//
// HandleEnroll builds it as "node-<overlay ip>", and the agent token is stored
// against it, so the proxy has to spell it the same way. Phase 1 replaces both
// with a real node principal.
func nodeIDForOverlayIP(wgIP string) string {
	return "node-" + wgIP
}
