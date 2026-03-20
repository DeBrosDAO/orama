package enroll

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
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
// Orchestrates: stop services → redistribute Shamir shares → remove WG peer.
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

	// Step 1: Tell the agent to stop services
	_, _, err := h.proxyToAgent(wgIP, "POST", "/v1/agent/command",
		[]byte(`{"action":"stop"}`))
	if err != nil {
		h.logger.Warn("failed to stop services on leaving node", zap.Error(err))
		// Continue — node may already be down
	}

	// Step 2: Remove WG peer from database
	ctx := r.Context()
	if _, err := h.rqliteClient.Exec(ctx,
		"DELETE FROM wireguard_peers WHERE wg_ip = ?", wgIP); err != nil {
		h.logger.Error("failed to remove WG peer from database", zap.Error(err))
		http.Error(w, "failed to remove peer", http.StatusInternalServerError)
		return
	}

	// Step 3: Remove from local WireGuard interface
	// Get the peer's public key first
	var rows []struct {
		PublicKey string `db:"public_key"`
	}
	_ = h.rqliteClient.Query(ctx, &rows,
		"SELECT public_key FROM wireguard_peers WHERE wg_ip = ?", wgIP)
	// Peer already deleted above, but try to remove from wg0 anyway
	h.removeWGPeerLocally(wgIP)

	h.logger.Info("node removed from cluster", zap.String("wg_ip", wgIP))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "removed",
		"wg_ip":  wgIP,
	})
}

// proxyToAgent sends an HTTP request to the OramaOS agent over WireGuard.
func (h *Handler) proxyToAgent(wgIP, method, path string, body []byte) ([]byte, int, error) {
	url := fmt.Sprintf("http://%s:9998%s", wgIP, path)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var reqBody io.Reader
	if body != nil {
		reqBody = strings.NewReader(string(body))
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, 0, err
	}
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

// removeWGPeerLocally removes a peer from the local wg0 interface by its allowed IP.
func (h *Handler) removeWGPeerLocally(wgIP string) {
	// Find peer public key by allowed IP
	out, err := exec.Command("wg", "show", "wg0", "dump").Output()
	if err != nil {
		log.Printf("failed to get wg dump: %v", err)
		return
	}

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 4 && strings.Contains(fields[3], wgIP) {
			pubKey := fields[0]
			exec.Command("wg", "set", "wg0", "peer", pubKey, "remove").Run()
			log.Printf("removed WG peer %s (%s)", pubKey[:8]+"...", wgIP)
			return
		}
	}
}
