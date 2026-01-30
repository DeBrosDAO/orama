package wireguard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// PeerRecord represents a WireGuard peer stored in RQLite
type PeerRecord struct {
	NodeID    string `json:"node_id" db:"node_id"`
	WGIP      string `json:"wg_ip" db:"wg_ip"`
	PublicKey string `json:"public_key" db:"public_key"`
	PublicIP  string `json:"public_ip" db:"public_ip"`
	WGPort    int    `json:"wg_port" db:"wg_port"`
}

// RegisterPeerRequest is the request body for peer registration
type RegisterPeerRequest struct {
	NodeID       string `json:"node_id"`
	PublicKey    string `json:"public_key"`
	PublicIP     string `json:"public_ip"`
	WGPort       int    `json:"wg_port,omitempty"`
	ClusterSecret string `json:"cluster_secret"`
}

// RegisterPeerResponse is the response for peer registration
type RegisterPeerResponse struct {
	AssignedWGIP string       `json:"assigned_wg_ip"`
	Peers        []PeerRecord `json:"peers"`
}

// Handler handles WireGuard peer exchange endpoints
type Handler struct {
	logger        *zap.Logger
	rqliteClient  rqlite.Client
	clusterSecret string // expected cluster secret for auth
}

// NewHandler creates a new WireGuard handler
func NewHandler(logger *zap.Logger, rqliteClient rqlite.Client, clusterSecret string) *Handler {
	return &Handler{
		logger:        logger,
		rqliteClient:  rqliteClient,
		clusterSecret: clusterSecret,
	}
}

// HandleRegisterPeer handles POST /v1/internal/wg/peer
// A new node calls this to register itself and get all existing peers.
func (h *Handler) HandleRegisterPeer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterPeerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate cluster secret
	if h.clusterSecret != "" && req.ClusterSecret != h.clusterSecret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if req.NodeID == "" || req.PublicKey == "" || req.PublicIP == "" {
		http.Error(w, "node_id, public_key, and public_ip are required", http.StatusBadRequest)
		return
	}

	if req.WGPort == 0 {
		req.WGPort = 51820
	}

	ctx := r.Context()

	// Assign next available WG IP
	wgIP, err := h.assignNextWGIP(ctx)
	if err != nil {
		h.logger.Error("failed to assign WG IP", zap.Error(err))
		http.Error(w, "failed to assign WG IP", http.StatusInternalServerError)
		return
	}

	// Insert peer record
	_, err = h.rqliteClient.Exec(ctx,
		"INSERT OR REPLACE INTO wireguard_peers (node_id, wg_ip, public_key, public_ip, wg_port) VALUES (?, ?, ?, ?, ?)",
		req.NodeID, wgIP, req.PublicKey, req.PublicIP, req.WGPort)
	if err != nil {
		h.logger.Error("failed to insert WG peer", zap.Error(err))
		http.Error(w, "failed to register peer", http.StatusInternalServerError)
		return
	}

	// Get all peers (including the one just added)
	peers, err := h.ListPeers(ctx)
	if err != nil {
		h.logger.Error("failed to list WG peers", zap.Error(err))
		http.Error(w, "failed to list peers", http.StatusInternalServerError)
		return
	}

	resp := RegisterPeerResponse{
		AssignedWGIP: wgIP,
		Peers:        peers,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)

	h.logger.Info("registered WireGuard peer",
		zap.String("node_id", req.NodeID),
		zap.String("wg_ip", wgIP),
		zap.String("public_ip", req.PublicIP))
}

// HandleListPeers handles GET /v1/internal/wg/peers
func (h *Handler) HandleListPeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	peers, err := h.ListPeers(r.Context())
	if err != nil {
		h.logger.Error("failed to list WG peers", zap.Error(err))
		http.Error(w, "failed to list peers", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peers)
}

// HandleRemovePeer handles DELETE /v1/internal/wg/peer?node_id=xxx
func (h *Handler) HandleRemovePeer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nodeID := r.URL.Query().Get("node_id")
	if nodeID == "" {
		http.Error(w, "node_id parameter required", http.StatusBadRequest)
		return
	}

	_, err := h.rqliteClient.Exec(r.Context(),
		"DELETE FROM wireguard_peers WHERE node_id = ?", nodeID)
	if err != nil {
		h.logger.Error("failed to remove WG peer", zap.Error(err))
		http.Error(w, "failed to remove peer", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	h.logger.Info("removed WireGuard peer", zap.String("node_id", nodeID))
}

// ListPeers returns all registered WireGuard peers
func (h *Handler) ListPeers(ctx context.Context) ([]PeerRecord, error) {
	var peers []PeerRecord
	err := h.rqliteClient.Query(ctx, &peers,
		"SELECT node_id, wg_ip, public_key, public_ip, wg_port FROM wireguard_peers ORDER BY wg_ip")
	if err != nil {
		return nil, fmt.Errorf("failed to query wireguard_peers: %w", err)
	}
	return peers, nil
}

// assignNextWGIP finds the next available 10.0.0.x IP
func (h *Handler) assignNextWGIP(ctx context.Context) (string, error) {
	var result []struct {
		MaxIP string `db:"max_ip"`
	}

	err := h.rqliteClient.Query(ctx, &result,
		"SELECT MAX(wg_ip) as max_ip FROM wireguard_peers")
	if err != nil {
		return "", fmt.Errorf("failed to query max WG IP: %w", err)
	}

	if len(result) == 0 || result[0].MaxIP == "" {
		return "10.0.0.1", nil
	}

	// Parse last octet and increment
	maxIP := result[0].MaxIP
	var a, b, c, d int
	if _, err := fmt.Sscanf(maxIP, "%d.%d.%d.%d", &a, &b, &c, &d); err != nil {
		return "", fmt.Errorf("failed to parse max WG IP %s: %w", maxIP, err)
	}

	d++
	if d > 254 {
		c++
		d = 1
		if c > 255 {
			return "", fmt.Errorf("WireGuard IP space exhausted")
		}
	}

	return fmt.Sprintf("%d.%d.%d.%d", a, b, c, d), nil
}
