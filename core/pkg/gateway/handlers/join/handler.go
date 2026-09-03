package join

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
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
	ClusterSecret      string `json:"cluster_secret"`
	SwarmKey           string `json:"swarm_key"`
	APIKeyHMACSecret   string `json:"api_key_hmac_secret,omitempty"`
	RQLitePassword     string `json:"rqlite_password,omitempty"`
	OlricEncryptionKey string `json:"olric_encryption_key,omitempty"`
	// Serverless secrets encryption key (bugboard #837) — must be identical on
	// every node so namespace function secrets decrypt cluster-wide.
	SecretsEncryptionKey string `json:"secrets_encryption_key,omitempty"`
	// TURN shared secret (feat-124 #913) — must be identical on every node so
	// WebRTC TURN credentials validate cluster-wide.
	TURNSecret string `json:"turn_secret,omitempty"`

	// Cluster join info (all using WG IPs)
	RQLiteJoinAddress  string   `json:"rqlite_join_address"`
	IPFSPeer           PeerInfo `json:"ipfs_peer"`
	IPFSClusterPeer    PeerInfo `json:"ipfs_cluster_peer"`
	IPFSClusterPeerIDs []string `json:"ipfs_cluster_peer_ids,omitempty"`
	BootstrapPeers     []string `json:"bootstrap_peers"`

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

	// Validate public IP format
	if net.ParseIP(req.PublicIP) == nil || net.ParseIP(req.PublicIP).To4() == nil {
		http.Error(w, "public_ip must be a valid IPv4 address", http.StatusBadRequest)
		return
	}

	// Validate WireGuard public key: must be base64-encoded 32 bytes (Curve25519)
	// Also reject control characters (newlines) to prevent config injection
	if strings.ContainsAny(req.WGPublicKey, "\n\r") {
		http.Error(w, "wg_public_key contains invalid characters", http.StatusBadRequest)
		return
	}
	wgKeyBytes, err := base64.StdEncoding.DecodeString(req.WGPublicKey)
	if err != nil || len(wgKeyBytes) != 32 {
		http.Error(w, "wg_public_key must be a valid base64-encoded 32-byte key", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 1. Validate and consume the invite token (atomic single-use)
	if err := h.consumeToken(ctx, req.Token, req.PublicIP); err != nil {
		h.logger.Warn("join token validation failed", zap.Error(err))
		http.Error(w, "unauthorized: invalid or expired token", http.StatusUnauthorized)
		return
	}

	// 1b. Look up the operator wallet from the consumed token (may be empty for legacy tokens)
	operatorWallet := h.tokenOperatorWallet(ctx, req.Token)

	// 2. Clean up stale WG entries for this public IP (from previous installs).
	//    This prevents ghost peers: old rows with different node_id/wg_key that
	//    the sync loop would keep trying to reach.
	if _, err := h.rqliteClient.Exec(ctx,
		"DELETE FROM wireguard_peers WHERE public_ip = ?", req.PublicIP); err != nil {
		h.logger.Warn("failed to clean up stale WG entries", zap.Error(err))
		// Non-fatal: proceed with join
	}

	// 3. Assign WG IP with retry on conflict (runs after cleanup so ghost IPs
	//    from this public_ip are not counted)
	wgIP, err := h.assignWGIP(ctx)
	if err != nil {
		h.logger.Error("failed to assign WG IP", zap.Error(err))
		http.Error(w, "failed to assign WG IP", http.StatusInternalServerError)
		return
	}

	// 4. Register WG peer in database
	nodeID := fmt.Sprintf("node-%s", wgIP) // temporary ID based on WG IP
	_, err = h.rqliteClient.Exec(ctx,
		"INSERT OR REPLACE INTO wireguard_peers (node_id, wg_ip, public_key, public_ip, wg_port, operator_wallet) VALUES (?, ?, ?, ?, ?, ?)",
		nodeID, wgIP, req.WGPublicKey, req.PublicIP, 51820, operatorWallet)
	if err != nil {
		h.logger.Error("failed to register WG peer", zap.Error(err))
		http.Error(w, "failed to register peer", http.StatusInternalServerError)
		return
	}

	// 5. Add peer to local WireGuard interface immediately
	if err := h.addWGPeerLocally(req.WGPublicKey, req.PublicIP, wgIP); err != nil {
		h.logger.Warn("failed to add WG peer to local interface", zap.Error(err))
		// Non-fatal: the sync loop will pick it up
	}

	// 6. Read secrets from disk
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

	// Read API key HMAC secret (optional — may not exist on older clusters)
	apiKeyHMACSecret := ""
	if data, err := os.ReadFile(h.oramaDir + "/secrets/api-key-hmac-secret"); err == nil {
		apiKeyHMACSecret = strings.TrimSpace(string(data))
	}

	// Read RQLite password (optional — may not exist on older clusters)
	rqlitePassword := ""
	if data, err := os.ReadFile(h.oramaDir + "/secrets/rqlite-password"); err == nil {
		rqlitePassword = strings.TrimSpace(string(data))
	}

	// Read Olric encryption key (optional — may not exist on older clusters)
	olricEncryptionKey := ""
	if data, err := os.ReadFile(h.oramaDir + "/secrets/olric-encryption-key"); err == nil {
		olricEncryptionKey = strings.TrimSpace(string(data))
	}

	// Read serverless secrets encryption key (optional — may not exist on
	// older clusters; bugboard #837)
	secretsEncryptionKey := ""
	if data, err := os.ReadFile(h.oramaDir + "/secrets/secrets-encryption-key"); err == nil {
		secretsEncryptionKey = strings.TrimSpace(string(data))
	}

	// Read TURN shared secret (optional — may not exist on older clusters;
	// feat-124 #913)
	turnSecret := ""
	if data, err := os.ReadFile(h.oramaDir + "/secrets/turn-secret"); err == nil {
		turnSecret = strings.TrimSpace(string(data))
	}

	// 7. Get this node's WG IP (needed before peer list to check self-inclusion)
	myWGIP, err := h.getMyWGIP()
	if err != nil {
		h.logger.Error("failed to get local WG IP", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 8. Get all WG peers
	wgPeers, err := h.getWGPeers(ctx, req.WGPublicKey)
	if err != nil {
		h.logger.Error("failed to list WG peers", zap.Error(err))
		http.Error(w, "failed to list peers", http.StatusInternalServerError)
		return
	}

	// Ensure this node (the join handler's host) is in the peer list.
	// On a fresh genesis node, the WG sync loop may not have self-registered
	// into wireguard_peers yet, causing 0 peers to be returned.
	if !wgPeersContainsIP(wgPeers, myWGIP) {
		myPubKey, err := h.getMyWGPublicKey()
		if err != nil {
			h.logger.Error("failed to get local WG public key", zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		myPublicIP, err := h.getMyPublicIP()
		if err != nil {
			h.logger.Error("failed to get local public IP", zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		wgPeers = append([]WGPeerInfo{{
			PublicKey: myPubKey,
			Endpoint:  fmt.Sprintf("%s:%d", myPublicIP, 51820),
			AllowedIP: fmt.Sprintf("%s/32", myWGIP),
		}}, wgPeers...)
		h.logger.Info("self-injected into WG peer list (sync loop hasn't registered yet)",
			zap.String("wg_ip", myWGIP))
	}

	// 9. Query IPFS and IPFS Cluster peer info
	ipfsPeer := h.queryIPFSPeerInfo(myWGIP)
	ipfsClusterPeer := h.queryIPFSClusterPeerInfo(myWGIP)

	// 10. Get this node's libp2p peer ID for bootstrap peers
	bootstrapPeers := h.buildBootstrapPeers(myWGIP, ipfsPeer.ID)

	// 11. Read base domain from config
	baseDomain := h.readBaseDomain()

	// 12. Read IPFS Cluster trusted peer IDs
	ipfsClusterPeerIDs := h.readIPFSClusterTrustedPeers()

	// Build Olric seed peers from all existing WG peer IPs (memberlist port 3322)
	var olricPeers []string
	for _, p := range wgPeers {
		peerIP := strings.TrimSuffix(p.AllowedIP, "/32")
		olricPeers = append(olricPeers, fmt.Sprintf("%s:3322", peerIP))
	}
	// Include this node too
	olricPeers = append(olricPeers, fmt.Sprintf("%s:3322", myWGIP))

	resp := JoinResponse{
		WGIP:                 wgIP,
		WGPeers:              wgPeers,
		ClusterSecret:        strings.TrimSpace(string(clusterSecret)),
		SwarmKey:             strings.TrimSpace(string(swarmKey)),
		APIKeyHMACSecret:     apiKeyHMACSecret,
		RQLitePassword:       rqlitePassword,
		OlricEncryptionKey:   olricEncryptionKey,
		SecretsEncryptionKey: secretsEncryptionKey,
		TURNSecret:           turnSecret,
		RQLiteJoinAddress:    fmt.Sprintf("%s:7001", myWGIP),
		IPFSPeer:             ipfsPeer,
		IPFSClusterPeer:      ipfsClusterPeer,
		IPFSClusterPeerIDs:   ipfsClusterPeerIDs,
		BootstrapPeers:       bootstrapPeers,
		OlricPeers:           olricPeers,
		BaseDomain:           baseDomain,
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

// tokenOperatorWallet looks up the operator_wallet from a consumed invite token.
// Returns empty string if the token has no operator (legacy tokens).
func (h *Handler) tokenOperatorWallet(ctx context.Context, token string) string {
	var rows []struct {
		Wallet string `db:"operator_wallet"`
	}
	if err := h.rqliteClient.Query(ctx, &rows,
		"SELECT COALESCE(operator_wallet, '') AS operator_wallet FROM invite_tokens WHERE token = ?", token); err != nil {
		return ""
	}
	if len(rows) > 0 {
		return rows[0].Wallet
	}
	return ""
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

// wgPeersContainsIP checks if any peer in the list has the given WG IP
func wgPeersContainsIP(peers []WGPeerInfo, wgIP string) bool {
	target := fmt.Sprintf("%s/32", wgIP)
	for _, p := range peers {
		if p.AllowedIP == target {
			return true
		}
	}
	return false
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

// getMyWGPublicKey reads the local WireGuard public key from the orama secrets
// directory. The key is saved there during install by Phase6SetupWireGuard.
// This avoids needing root/CAP_NET_ADMIN permissions that `wg show wg0` requires.
func (h *Handler) getMyWGPublicKey() (string, error) {
	data, err := os.ReadFile(h.oramaDir + "/secrets/wg-public-key")
	if err != nil {
		return "", fmt.Errorf("failed to read WG public key from %s/secrets/wg-public-key: %w", h.oramaDir, err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", fmt.Errorf("WG public key file is empty")
	}
	return key, nil
}

// getMyPublicIP determines this node's public IP by connecting to a public server
func (h *Handler) getMyPublicIP() (string, error) {
	conn, err := net.DialTimeout("udp", "8.8.8.8:80", 3*time.Second)
	if err != nil {
		return "", fmt.Errorf("failed to determine public IP: %w", err)
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String(), nil
}

// queryIPFSPeerInfo gets the local IPFS node's peer ID and builds addrs with WG IP
func (h *Handler) queryIPFSPeerInfo(myWGIP string) PeerInfo {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://localhost:10107/api/v0/id", "", nil)
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
	resp, err := client.Get("http://localhost:10108/id")
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

// readIPFSClusterTrustedPeers reads IPFS Cluster trusted peer IDs from the secrets file
func (h *Handler) readIPFSClusterTrustedPeers() []string {
	data, err := os.ReadFile(h.oramaDir + "/secrets/ipfs-cluster-trusted-peers")
	if err != nil {
		return nil
	}
	var peers []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			peers = append(peers, line)
		}
	}
	return peers
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
