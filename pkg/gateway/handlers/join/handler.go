package join

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"
)

// JoinRequest is the request body for node join
type JoinRequest struct {
	Token       string `json:"token"`
	WGPublicKey string `json:"wg_public_key"`
	PublicIP    string `json:"public_ip"`
}

// JoinResponse contains everything a joining node needs
type JoinResponse struct {
	// WireGuard
	WGIP    string       `json:"wg_ip"`
	WGPeers []WGPeerInfo `json:"wg_peers"`

	// Secrets
	ClusterSecret string `json:"cluster_secret"`
	SwarmKey      string `json:"swarm_key"`

	// Cluster join info (all using WG IPs)
	RQLiteJoinAddress string   `json:"rqlite_join_address"`
	IPFSPeer          PeerInfo `json:"ipfs_peer"`
	IPFSClusterPeer   PeerInfo `json:"ipfs_cluster_peer"`
	BootstrapPeers    []string `json:"bootstrap_peers"`

	// Olric seed peers (WG IP:port for memberlist)
	OlricPeers []string `json:"olric_peers,omitempty"`

	// Domain
	BaseDomain string `json:"base_domain"`
}

// WGPeerInfo represents a WireGuard peer
type WGPeerInfo struct {
	PublicKey string `json:"public_key"`
	Endpoint  string `json:"endpoint"`
	AllowedIP string `json:"allowed_ip"`
}

// PeerInfo represents an IPFS/Cluster peer
type PeerInfo struct {
	ID    string   `json:"id"`
	Addrs []string `json:"addrs"`
}

// Handler handles the node join endpoint
type Handler struct {
	logger       *zap.Logger
	rqliteClient rqlite.Client
	oramaDir     string // e.g., /opt/orama/.orama
}

// NewHandler creates a new join handler
func NewHandler(logger *zap.Logger, rqliteClient rqlite.Client, oramaDir string) *Handler {
	return &Handler{
		logger:       logger,
		rqliteClient: rqliteClient,
		oramaDir:     oramaDir,
	}
}

// HandleJoin handles POST /v1/internal/join
func (h *Handler) HandleJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req JoinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Token == "" || req.WGPublicKey == "" || req.PublicIP == "" {
		http.Error(w, "token, wg_public_key, and public_ip are required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 1. Validate and consume the invite token (atomic single-use)
	if err := h.consumeToken(ctx, req.Token, req.PublicIP); err != nil {
		h.logger.Warn("join token validation failed", zap.Error(err))
		http.Error(w, "unauthorized: invalid or expired token", http.StatusUnauthorized)
		return
	}

	// 2. Assign WG IP with retry on conflict
	wgIP, err := h.assignWGIP(ctx)
	if err != nil {
		h.logger.Error("failed to assign WG IP", zap.Error(err))
		http.Error(w, "failed to assign WG IP", http.StatusInternalServerError)
		return
	}

	// 3. Register WG peer in database
	nodeID := fmt.Sprintf("node-%s", wgIP) // temporary ID based on WG IP
	_, err = h.rqliteClient.Exec(ctx,
		"INSERT OR REPLACE INTO wireguard_peers (node_id, wg_ip, public_key, public_ip, wg_port) VALUES (?, ?, ?, ?, ?)",
		nodeID, wgIP, req.WGPublicKey, req.PublicIP, 51820)
	if err != nil {
		h.logger.Error("failed to register WG peer", zap.Error(err))
		http.Error(w, "failed to register peer", http.StatusInternalServerError)
		return
	}

	// 4. Add peer to local WireGuard interface immediately
	if err := h.addWGPeerLocally(req.WGPublicKey, req.PublicIP, wgIP); err != nil {
		h.logger.Warn("failed to add WG peer to local interface", zap.Error(err))
		// Non-fatal: the sync loop will pick it up
	}

	// 5. Read secrets from disk
	clusterSecret, err := os.ReadFile(h.oramaDir + "/secrets/cluster-secret")
	if err != nil {
		h.logger.Error("failed to read cluster secret", zap.Error(err))
		http.Error(w, "internal error reading secrets", http.StatusInternalServerError)
		return
	}

	swarmKey, err := os.ReadFile(h.oramaDir + "/secrets/swarm.key")
	if err != nil {
		h.logger.Error("failed to read swarm key", zap.Error(err))
		http.Error(w, "internal error reading secrets", http.StatusInternalServerError)
		return
	}

	// 6. Get all WG peers
	wgPeers, err := h.getWGPeers(ctx, req.WGPublicKey)
	if err != nil {
		h.logger.Error("failed to list WG peers", zap.Error(err))
		http.Error(w, "failed to list peers", http.StatusInternalServerError)
		return
	}

	// 7. Get this node's WG IP
	myWGIP, err := h.getMyWGIP()
	if err != nil {
		h.logger.Error("failed to get local WG IP", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 8. Query IPFS and IPFS Cluster peer info
	ipfsPeer := h.queryIPFSPeerInfo(myWGIP)
	ipfsClusterPeer := h.queryIPFSClusterPeerInfo(myWGIP)

	// 9. Get this node's libp2p peer ID for bootstrap peers
	bootstrapPeers := h.buildBootstrapPeers(myWGIP, ipfsPeer.ID)

	// 10. Read base domain from config
	baseDomain := h.readBaseDomain()

	// Build Olric seed peers from all existing WG peer IPs (memberlist port 3322)
	var olricPeers []string
	for _, p := range wgPeers {
		peerIP := strings.TrimSuffix(p.AllowedIP, "/32")
		olricPeers = append(olricPeers, fmt.Sprintf("%s:3322", peerIP))
	}
	// Include this node too
	olricPeers = append(olricPeers, fmt.Sprintf("%s:3322", myWGIP))

	resp := JoinResponse{
		WGIP:              wgIP,
		WGPeers:           wgPeers,
		ClusterSecret:     strings.TrimSpace(string(clusterSecret)),
		SwarmKey:          strings.TrimSpace(string(swarmKey)),
		RQLiteJoinAddress: fmt.Sprintf("%s:7001", myWGIP),
		IPFSPeer:          ipfsPeer,
		IPFSClusterPeer:   ipfsClusterPeer,
		BootstrapPeers:    bootstrapPeers,
		OlricPeers:        olricPeers,
		BaseDomain:        baseDomain,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)

	h.logger.Info("node joined cluster",
		zap.String("wg_ip", wgIP),
		zap.String("public_ip", req.PublicIP))
}

// consumeToken validates and marks an invite token as used (atomic single-use)
func (h *Handler) consumeToken(ctx context.Context, token, usedByIP string) error {
	// Atomically mark as used — only succeeds if token exists, is unused, and not expired
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

// assignWGIP finds the next available 10.0.0.x IP by querying all peers and
// finding the numerically highest IP. This avoids lexicographic comparison issues
// where MAX("10.0.0.9") > MAX("10.0.0.10") in SQL string comparison.
func (h *Handler) assignWGIP(ctx context.Context) (string, error) {
	var rows []struct {
		WGIP string `db:"wg_ip"`
	}

	err := h.rqliteClient.Query(ctx, &rows, "SELECT wg_ip FROM wireguard_peers")
	if err != nil {
		return "", fmt.Errorf("failed to query WG IPs: %w", err)
	}

	if len(rows) == 0 {
		return "10.0.0.2", nil // 10.0.0.1 is genesis
	}

	// Find the numerically highest IP
	maxA, maxB, maxC, maxD := 0, 0, 0, 0
	for _, row := range rows {
		var a, b, c, d int
		if _, err := fmt.Sscanf(row.WGIP, "%d.%d.%d.%d", &a, &b, &c, &d); err != nil {
			continue
		}
		if c > maxC || (c == maxC && d > maxD) {
			maxA, maxB, maxC, maxD = a, b, c, d
		}
	}

	if maxA == 0 {
		return "10.0.0.2", nil
	}

	maxD++
	if maxD > 254 {
		maxC++
		maxD = 1
		if maxC > 255 {
			return "", fmt.Errorf("WireGuard IP space exhausted")
		}
	}

	return fmt.Sprintf("%d.%d.%d.%d", maxA, maxB, maxC, maxD), nil
}

// addWGPeerLocally adds a peer to the local wg0 interface and persists to config
func (h *Handler) addWGPeerLocally(pubKey, publicIP, wgIP string) error {
	// Add to running interface with persistent-keepalive
	cmd := exec.Command("wg", "set", "wg0",
		"peer", pubKey,
		"endpoint", fmt.Sprintf("%s:51820", publicIP),
		"allowed-ips", fmt.Sprintf("%s/32", wgIP),
		"persistent-keepalive", "25")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("wg set failed: %w\n%s", err, string(output))
	}

	// Persist to wg0.conf so peer survives wg-quick restart.
	// Read current config, append peer section, write back.
	confPath := "/etc/wireguard/wg0.conf"
	data, err := os.ReadFile(confPath)
	if err != nil {
		h.logger.Warn("could not read wg0.conf for persistence", zap.Error(err))
		return nil // non-fatal: runtime peer is added
	}

	// Check if peer already in config
	if strings.Contains(string(data), pubKey) {
		return nil // already persisted
	}

	peerSection := fmt.Sprintf("\n[Peer]\nPublicKey = %s\nEndpoint = %s:51820\nAllowedIPs = %s/32\nPersistentKeepalive = 25\n",
		pubKey, publicIP, wgIP)

	newConf := string(data) + peerSection
	writeCmd := exec.Command("tee", confPath)
	writeCmd.Stdin = strings.NewReader(newConf)
	if output, err := writeCmd.CombinedOutput(); err != nil {
		h.logger.Warn("could not persist peer to wg0.conf", zap.Error(err), zap.String("output", string(output)))
	}

	return nil
}

// getWGPeers returns all WG peers except the requesting node
func (h *Handler) getWGPeers(ctx context.Context, excludePubKey string) ([]WGPeerInfo, error) {
	type peerRow struct {
		WGIP      string `db:"wg_ip"`
		PublicKey string `db:"public_key"`
		PublicIP  string `db:"public_ip"`
		WGPort    int    `db:"wg_port"`
	}

	var rows []peerRow
	err := h.rqliteClient.Query(ctx, &rows,
		"SELECT wg_ip, public_key, public_ip, wg_port FROM wireguard_peers ORDER BY wg_ip")
	if err != nil {
		return nil, err
	}

	var peers []WGPeerInfo
	for _, row := range rows {
		if row.PublicKey == excludePubKey {
			continue // don't include the requesting node itself
		}
		port := row.WGPort
		if port == 0 {
			port = 51820
		}
		peers = append(peers, WGPeerInfo{
			PublicKey: row.PublicKey,
			Endpoint:  fmt.Sprintf("%s:%d", row.PublicIP, port),
			AllowedIP: fmt.Sprintf("%s/32", row.WGIP),
		})
	}

	return peers, nil
}

// getMyWGIP gets this node's WireGuard IP from the wg0 interface
func (h *Handler) getMyWGIP() (string, error) {
	out, err := exec.Command("ip", "-4", "addr", "show", "wg0").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get wg0 info: %w", err)
	}

	// Parse "inet 10.0.0.1/32" from output
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				ip := strings.Split(parts[1], "/")[0]
				return ip, nil
			}
		}
	}

	return "", fmt.Errorf("could not find wg0 IP address")
}

// queryIPFSPeerInfo gets the local IPFS node's peer ID and builds addrs with WG IP
func (h *Handler) queryIPFSPeerInfo(myWGIP string) PeerInfo {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://localhost:4501/api/v0/id", "", nil)
	if err != nil {
		h.logger.Warn("failed to query IPFS peer info", zap.Error(err))
		return PeerInfo{}
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"ID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		h.logger.Warn("failed to decode IPFS peer info", zap.Error(err))
		return PeerInfo{}
	}

	return PeerInfo{
		ID: result.ID,
		Addrs: []string{
			fmt.Sprintf("/ip4/%s/tcp/4101/p2p/%s", myWGIP, result.ID),
		},
	}
}

// queryIPFSClusterPeerInfo gets the local IPFS Cluster peer ID and builds addrs with WG IP
func (h *Handler) queryIPFSClusterPeerInfo(myWGIP string) PeerInfo {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:9094/id")
	if err != nil {
		h.logger.Warn("failed to query IPFS Cluster peer info", zap.Error(err))
		return PeerInfo{}
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		h.logger.Warn("failed to decode IPFS Cluster peer info", zap.Error(err))
		return PeerInfo{}
	}

	return PeerInfo{
		ID: result.ID,
		Addrs: []string{
			fmt.Sprintf("/ip4/%s/tcp/9100/p2p/%s", myWGIP, result.ID),
		},
	}
}

// buildBootstrapPeers constructs bootstrap peer multiaddrs using WG IPs
// Uses the node's LibP2P peer ID (port 4001), NOT the IPFS peer ID (port 4101)
func (h *Handler) buildBootstrapPeers(myWGIP, ipfsPeerID string) []string {
	// Read the node's LibP2P identity from disk
	keyPath := filepath.Join(h.oramaDir, "data", "identity.key")
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		h.logger.Warn("Failed to read node identity for bootstrap peers", zap.Error(err))
		return nil
	}

	priv, err := crypto.UnmarshalPrivateKey(keyData)
	if err != nil {
		h.logger.Warn("Failed to unmarshal node identity key", zap.Error(err))
		return nil
	}

	peerID, err := peer.IDFromPublicKey(priv.GetPublic())
	if err != nil {
		h.logger.Warn("Failed to derive peer ID from identity key", zap.Error(err))
		return nil
	}

	return []string{
		fmt.Sprintf("/ip4/%s/tcp/4001/p2p/%s", myWGIP, peerID.String()),
	}
}

// readBaseDomain reads the base domain from node config
func (h *Handler) readBaseDomain() string {
	data, err := os.ReadFile(h.oramaDir + "/configs/node.yaml")
	if err != nil {
		return ""
	}

	// Simple parse — look for base_domain field
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "base_domain:") {
			val := strings.TrimPrefix(line, "base_domain:")
			val = strings.TrimSpace(val)
			val = strings.Trim(val, `"'`)
			return val
		}
	}

	return ""
}
