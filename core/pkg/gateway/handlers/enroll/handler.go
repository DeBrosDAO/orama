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
	"context"
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/overlay"
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

	// node_ip is stored in wireguard_peers.public_ip, from where it is rendered
	// into `Endpoint = %s` in the wg0.conf of every OTHER node in the fleet. An
	// unvalidated string there is a WireGuard config injection: a newline lets
	// the caller append arbitrary directives — its own PublicKey, an
	// AllowedIPs of 0.0.0.0/0 — to configs generated from then on. The join
	// handler has always parsed its equivalent field; this one checked only
	// that it was non-empty.
	//
	// The parsed form is stored rather than the raw string, so no alternative
	// spelling of the same address can round-trip.
	nodeIP := net.ParseIP(req.NodeIP)
	if nodeIP == nil || nodeIP.To4() == nil {
		http.Error(w, "node_ip must be a valid IPv4 address", http.StatusBadRequest)
		return
	}
	req.NodeIP = nodeIP.String()

	ctx := r.Context()

	// 1. Validate invite token (single-use, same as join handler)
	if err := h.consumeToken(ctx, req.Token, req.NodeIP); err != nil {
		h.logger.Warn("enroll token validation failed", zap.Error(err))
		http.Error(w, "unauthorized: invalid or expired token", http.StatusUnauthorized)
		return
	}

	// The registration code is not verified here, and is deliberately not
	// fetched from the node. It used to be: a GET on the node's :9999 returned
	// the code to whoever asked, which published the one secret the operator
	// carried and let anyone race them for it. The code is proved to the node
	// instead, in step 10 — a wrong one fails to decrypt there, and nothing is
	// configured.

	// 3. Generate WG keypair for the OramaOS node
	wgPrivKey, wgPubKey, err := generateWGKeypair()
	if err != nil {
		h.logger.Error("failed to generate WG keypair", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 4. Allocate an overlay address and register the peer.
	//
	// Through pkg/overlay, the same path the join handler uses. This handler
	// had its own copy which read the table and then wrote it with
	// INSERT OR REPLACE — so a concurrent enrolment silently deleted the
	// winner's row and took its address — and which allocated max+1 and rolled
	// explicitly into 10.0.1.x once .254 was used, outside the /24 the wg0
	// PostUp rule and the internal-auth check accept.
	//
	// An enrolling node has no libp2p identity yet, so the id is left to
	// overlay to synthesise. It used to be a third shape, `orama-node-10-0-0-N`,
	// that nothing else in the system recognised.
	wgIP, err := overlay.Register(ctx, h.rqliteClient, overlay.Peer{
		PublicKey: wgPubKey,
		PublicIP:  req.NodeIP,
	})
	if err != nil {
		h.logger.Error("failed to register WG peer", zap.Error(err))
		http.Error(w, "failed to register peer", http.StatusInternalServerError)
		return
	}
	nodeID := fmt.Sprintf("node-%s", wgIP)

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

	agentToken, err := h.pushConfigToNode(req.NodeIP, req.Code, &enrollResp)
	if err != nil {
		h.logger.Error("failed to push config to node", zap.Error(err))
		http.Error(w, "failed to configure node: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// The node minted its own credential and handed it back. Every command
	// this gateway later sends it has to carry it, so losing it here means the
	// node can never be commanded again — it is not best-effort.
	if err := h.storeAgentToken(ctx, nodeID, agentToken); err != nil {
		h.logger.Error("the node was configured but its agent token could not be stored; "+
			"it will accept no commands from this gateway",
			zap.String("node_id", nodeID), zap.Error(err))
		http.Error(w, "failed to store the node's agent credential", http.StatusInternalServerError)
		return
	}

	// The node has its config and is coming up, which is what confirmed_at
	// records. Without this an enrolled node has no writer that ever confirms
	// it — no join handshake, no orama-node self-registration — so the
	// membership reconciler would collect its mesh row as an unfinished join
	// 30 minutes later.
	if _, err := h.rqliteClient.Exec(ctx,
		`UPDATE wireguard_peers SET confirmed_at = CURRENT_TIMESTAMP
		  WHERE node_id = ? AND confirmed_at IS NULL`, nodeID); err != nil {
		h.logger.Error("enrolled node was configured but its peer row could not be confirmed; "+
			"it will be collected as an unfinished join unless this is corrected",
			zap.String("node_id", nodeID), zap.Error(err))
		http.Error(w, "failed to confirm node registration", http.StatusInternalServerError)
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
		usedByIP, operator.HashInviteToken(token))
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

// pushConfigToNode sends cluster configuration to the OramaOS node, sealed
// under the registration code the operator carried from its console, and
// returns the credential the node mints for this gateway.
//
// The payload carries the cluster secret, the swarm key and the node's
// WireGuard configuration. It used to be plaintext JSON over HTTP on the node's
// public IP, to an endpoint that accepted any POST at all — so it could be read
// by anyone on the path and written by anyone who got there first.
func (h *Handler) pushConfigToNode(nodeIP, code string, config *EnrollResponse) (string, error) {
	return h.pushConfigTo(fmt.Sprintf("http://%s:9999/v1/agent/enroll/complete", nodeIP), code, config)
}

// pushConfigTo is pushConfigToNode against an explicit endpoint, so the
// exchange can be exercised against a stand-in for the agent rather than
// against a hardcoded port.
func (h *Handler) pushConfigTo(endpoint, code string, config *EnrollResponse) (string, error) {
	body, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	sealed, err := Seal(code, body)
	if err != nil {
		return "", fmt.Errorf("could not seal the enrollment payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(sealed))
	if err != nil {
		return "", err
	}
	req.Header.Set(HeaderEnrollmentCode, code)
	req.Header.Set("Content-Type", "text/plain")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to push config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("the node rejected the registration code: check the code " +
			"shown on its console")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("node returned status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("could not read the node's response: %w", err)
	}
	plaintext, err := Open(code, string(raw))
	if err != nil {
		return "", fmt.Errorf("the node's response did not decrypt, so this is not the node "+
			"the operator read the code from: %w", err)
	}

	var completion struct {
		AgentToken string `json:"agent_token"`
	}
	if err := json.Unmarshal(plaintext, &completion); err != nil {
		return "", fmt.Errorf("the node's response was not valid JSON: %w", err)
	}
	if strings.TrimSpace(completion.AgentToken) == "" {
		return "", fmt.Errorf("the node returned no agent token, so it could never be commanded")
	}
	return completion.AgentToken, nil
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
