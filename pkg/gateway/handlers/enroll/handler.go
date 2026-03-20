// Package enroll implements the OramaOS node enrollment endpoint.
//
// Flow:
//  1. Operator's CLI sends POST /v1/node/enroll with code + token + node_ip
//  2. Gateway validates invite token (single-use)
//  3. Gateway assigns WG IP, registers peer, reads secrets
//  4. Gateway pushes cluster config to OramaOS node at node_ip:9999
//  5. OramaOS node configures WG, encrypts data partition, starts services
package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// EnrollRequest is the request from the CLI.
type EnrollRequest struct {
	Code   string `json:"code"`
	Token  string `json:"token"`
	NodeIP string `json:"node_ip"`
}

// EnrollResponse is the configuration pushed to the OramaOS node.
type EnrollResponse struct {
	NodeID          string     `json:"node_id"`
	WireGuardConfig string     `json:"wireguard_config"`
	ClusterSecret   string     `json:"cluster_secret"`
	Peers           []PeerInfo `json:"peers"`
}

// PeerInfo describes a cluster peer for LUKS key distribution.
type PeerInfo struct {
	WGIP   string `json:"wg_ip"`
	NodeID string `json:"node_id"`
}

// Handler handles OramaOS node enrollment.
type Handler struct {
	logger       *zap.Logger
	rqliteClient rqlite.Client
	oramaDir     string
}

// NewHandler creates a new enrollment handler.
func NewHandler(logger *zap.Logger, rqliteClient rqlite.Client, oramaDir string) *Handler {
	return &Handler{
		logger:       logger,
		rqliteClient: rqliteClient,
		oramaDir:     oramaDir,
	}
}

// HandleEnroll handles POST /v1/node/enroll.
func (h *Handler) HandleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req EnrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Code == "" || req.Token == "" || req.NodeIP == "" {
		http.Error(w, "code, token, and node_ip are required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 1. Validate invite token (single-use, same as join handler)
	if err := h.consumeToken(ctx, req.Token, req.NodeIP); err != nil {
		h.logger.Warn("enroll token validation failed", zap.Error(err))
		http.Error(w, "unauthorized: invalid or expired token", http.StatusUnauthorized)
		return
	}

	// 2. Verify registration code against the OramaOS node
	if err := h.verifyCode(req.NodeIP, req.Code); err != nil {
		h.logger.Warn("registration code verification failed", zap.Error(err))
		http.Error(w, "code verification failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 3. Generate WG keypair for the OramaOS node
	wgPrivKey, wgPubKey, err := generateWGKeypair()
	if err != nil {
		h.logger.Error("failed to generate WG keypair", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 4. Assign WG IP
	wgIP, err := h.assignWGIP(ctx)
	if err != nil {
		h.logger.Error("failed to assign WG IP", zap.Error(err))
		http.Error(w, "failed to assign WG IP", http.StatusInternalServerError)
		return
	}

	nodeID := fmt.Sprintf("orama-node-%s", strings.ReplaceAll(wgIP, ".", "-"))

	// 5. Register WG peer in database
	if _, err := h.rqliteClient.Exec(ctx,
		"INSERT OR REPLACE INTO wireguard_peers (node_id, wg_ip, public_key, public_ip, wg_port) VALUES (?, ?, ?, ?, ?)",
		nodeID, wgIP, wgPubKey, req.NodeIP, 51820); err != nil {
		h.logger.Error("failed to register WG peer", zap.Error(err))
		http.Error(w, "failed to register peer", http.StatusInternalServerError)
		return
	}

	// 6. Add peer to local WireGuard interface
	if err := h.addWGPeerLocally(wgPubKey, req.NodeIP, wgIP); err != nil {
		h.logger.Warn("failed to add WG peer to local interface", zap.Error(err))
	}

	// 7. Read secrets
	clusterSecret, err := os.ReadFile(h.oramaDir + "/secrets/cluster-secret")
	if err != nil {
		h.logger.Error("failed to read cluster secret", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 8. Build WireGuard config for the OramaOS node
	wgConfig, err := h.buildWGConfig(ctx, wgPrivKey, wgIP)
	if err != nil {
		h.logger.Error("failed to build WG config", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 9. Get all peer WG IPs for LUKS key distribution
	peers, err := h.getPeerList(ctx, wgIP)
	if err != nil {
		h.logger.Error("failed to get peer list", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 10. Push config to OramaOS node
	enrollResp := EnrollResponse{
		NodeID:          nodeID,
		WireGuardConfig: wgConfig,
		ClusterSecret:   strings.TrimSpace(string(clusterSecret)),
		Peers:           peers,
	}

	if err := h.pushConfigToNode(req.NodeIP, &enrollResp); err != nil {
		h.logger.Error("failed to push config to node", zap.Error(err))
		http.Error(w, "failed to configure node: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.logger.Info("OramaOS node enrolled",
		zap.String("node_id", nodeID),
		zap.String("wg_ip", wgIP),
		zap.String("public_ip", req.NodeIP))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "enrolled",
		"node_id": nodeID,
		"wg_ip":   wgIP,
	})
}

// consumeToken validates and marks an invite token as used.
func (h *Handler) consumeToken(ctx context.Context, token, usedByIP string) error {
	result, err := h.rqliteClient.Exec(ctx,
		"UPDATE invite_tokens SET used_at = datetime('now'), used_by_ip = ? WHERE token = ? AND used_at IS NULL AND expires_at > datetime('now')",
		usedByIP, token)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check result: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("token invalid, expired, or already used")
	}

	return nil
}

// verifyCode checks that the OramaOS node has the expected registration code.
func (h *Handler) verifyCode(nodeIP, expectedCode string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s:9999/", nodeIP))
	if err != nil {
		return fmt.Errorf("cannot reach node at %s:9999: %w", nodeIP, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		return fmt.Errorf("node already served its registration code")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("node returned status %d", resp.StatusCode)
	}

	var result struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("invalid response from node: %w", err)
	}

	if result.Code != expectedCode {
		return fmt.Errorf("registration code mismatch")
	}

	return nil
}

// pushConfigToNode sends cluster configuration to the OramaOS node.
func (h *Handler) pushConfigToNode(nodeIP string, config *EnrollResponse) error {
	body, err := json.Marshal(config)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(
		fmt.Sprintf("http://%s:9999/v1/agent/enroll/complete", nodeIP),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("failed to push config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("node returned status %d", resp.StatusCode)
	}

	return nil
}

// generateWGKeypair generates a WireGuard private/public keypair.
func generateWGKeypair() (privKey, pubKey string, err error) {
	privOut, err := exec.Command("wg", "genkey").Output()
	if err != nil {
		return "", "", fmt.Errorf("wg genkey failed: %w", err)
	}
	privKey = strings.TrimSpace(string(privOut))

	cmd := exec.Command("wg", "pubkey")
	cmd.Stdin = strings.NewReader(privKey)
	pubOut, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("wg pubkey failed: %w", err)
	}
	pubKey = strings.TrimSpace(string(pubOut))

	return privKey, pubKey, nil
}

// assignWGIP finds the next available WG IP.
func (h *Handler) assignWGIP(ctx context.Context) (string, error) {
	var rows []struct {
		WGIP string `db:"wg_ip"`
	}
	if err := h.rqliteClient.Query(ctx, &rows, "SELECT wg_ip FROM wireguard_peers"); err != nil {
		return "", fmt.Errorf("failed to query WG IPs: %w", err)
	}

	if len(rows) == 0 {
		return "10.0.0.2", nil
	}

	maxD := 0
	maxC := 0
	for _, row := range rows {
		var a, b, c, d int
		if _, err := fmt.Sscanf(row.WGIP, "%d.%d.%d.%d", &a, &b, &c, &d); err != nil {
			continue
		}
		if c > maxC || (c == maxC && d > maxD) {
			maxC, maxD = c, d
		}
	}

	maxD++
	if maxD > 254 {
		maxC++
		maxD = 1
	}

	return fmt.Sprintf("10.0.%d.%d", maxC, maxD), nil
}

// addWGPeerLocally adds a peer to the local wg0 interface.
func (h *Handler) addWGPeerLocally(pubKey, publicIP, wgIP string) error {
	cmd := exec.Command("wg", "set", "wg0",
		"peer", pubKey,
		"endpoint", fmt.Sprintf("%s:51820", publicIP),
		"allowed-ips", fmt.Sprintf("%s/32", wgIP),
		"persistent-keepalive", "25")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("wg set failed: %w\n%s", err, string(output))
	}
	return nil
}

// buildWGConfig generates a wg0.conf for the OramaOS node.
func (h *Handler) buildWGConfig(ctx context.Context, privKey, nodeWGIP string) (string, error) {
	// Get this node's public key and WG IP
	myPubKey, err := exec.Command("wg", "show", "wg0", "public-key").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get local WG public key: %w", err)
	}

	myWGIP, err := h.getMyWGIP()
	if err != nil {
		return "", fmt.Errorf("failed to get local WG IP: %w", err)
	}

	myPublicIP, err := h.getMyPublicIP(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get local public IP: %w", err)
	}

	var config strings.Builder
	config.WriteString("[Interface]\n")
	config.WriteString(fmt.Sprintf("PrivateKey = %s\n", privKey))
	config.WriteString(fmt.Sprintf("Address = %s/24\n", nodeWGIP))
	config.WriteString("ListenPort = 51820\n")
	config.WriteString("\n")

	// Add this gateway node as a peer
	config.WriteString("[Peer]\n")
	config.WriteString(fmt.Sprintf("PublicKey = %s\n", strings.TrimSpace(string(myPubKey))))
	config.WriteString(fmt.Sprintf("Endpoint = %s:51820\n", myPublicIP))
	config.WriteString(fmt.Sprintf("AllowedIPs = %s/32\n", myWGIP))
	config.WriteString("PersistentKeepalive = 25\n")

	// Add all existing peers
	type peerRow struct {
		WGIP      string `db:"wg_ip"`
		PublicKey string `db:"public_key"`
		PublicIP  string `db:"public_ip"`
	}
	var peers []peerRow
	if err := h.rqliteClient.Query(ctx, &peers,
		"SELECT wg_ip, public_key, public_ip FROM wireguard_peers WHERE wg_ip != ?", nodeWGIP); err != nil {
		h.logger.Warn("failed to query peers for WG config", zap.Error(err))
	}

	for _, p := range peers {
		if p.PublicKey == strings.TrimSpace(string(myPubKey)) {
			continue // already added above
		}
		config.WriteString(fmt.Sprintf("\n[Peer]\nPublicKey = %s\nEndpoint = %s:51820\nAllowedIPs = %s/32\nPersistentKeepalive = 25\n",
			p.PublicKey, p.PublicIP, p.WGIP))
	}

	return config.String(), nil
}

// getPeerList returns all cluster peers for LUKS key distribution.
func (h *Handler) getPeerList(ctx context.Context, excludeWGIP string) ([]PeerInfo, error) {
	type peerRow struct {
		NodeID string `db:"node_id"`
		WGIP   string `db:"wg_ip"`
	}
	var rows []peerRow
	if err := h.rqliteClient.Query(ctx, &rows,
		"SELECT node_id, wg_ip FROM wireguard_peers WHERE wg_ip != ?", excludeWGIP); err != nil {
		return nil, err
	}

	peers := make([]PeerInfo, 0, len(rows))
	for _, row := range rows {
		peers = append(peers, PeerInfo{
			WGIP:   row.WGIP,
			NodeID: row.NodeID,
		})
	}
	return peers, nil
}

// getMyWGIP gets this node's WireGuard IP.
func (h *Handler) getMyWGIP() (string, error) {
	out, err := exec.Command("ip", "-4", "addr", "show", "wg0").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get wg0 info: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return strings.Split(parts[1], "/")[0], nil
			}
		}
	}
	return "", fmt.Errorf("could not find wg0 IP")
}

// getMyPublicIP reads this node's public IP from the database.
func (h *Handler) getMyPublicIP(ctx context.Context) (string, error) {
	myWGIP, err := h.getMyWGIP()
	if err != nil {
		return "", err
	}
	var rows []struct {
		PublicIP string `db:"public_ip"`
	}
	if err := h.rqliteClient.Query(ctx, &rows,
		"SELECT public_ip FROM wireguard_peers WHERE wg_ip = ?", myWGIP); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("no peer entry for WG IP %s", myWGIP)
	}
	return rows[0].PublicIP, nil
}
