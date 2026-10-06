package wireguard

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/overlay"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/libp2p/go-libp2p/core/peer"
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
	NodeID        string `json:"node_id"`
	PublicKey     string `json:"public_key"`
	PublicIP      string `json:"public_ip"`
	WGPort        int    `json:"wg_port,omitempty"`
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

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req RegisterPeerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// This endpoint writes into the WireGuard overlay, so it is held to the
	// same bar as the other internal endpoints: the caller must already be on
	// the mesh AND present the cluster secret.
	//
	// It previously checked neither. There was no peer check at all, and the
	// secret check read `if h.clusterSecret != "" && ...`, which let anything
	// through on a gateway configured without one — an unauthenticated way to
	// insert a peer into the private network.
	if !auth.IsWireGuardPeer(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.clusterSecret == "" {
		h.logger.Warn("refusing WireGuard peer registration: this gateway has no cluster secret configured, " +
			"so the caller cannot be authenticated")
		http.Error(w, "peer registration unavailable: no cluster secret configured", http.StatusServiceUnavailable)
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.ClusterSecret), []byte(h.clusterSecret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if req.NodeID == "" || req.PublicKey == "" || req.PublicIP == "" {
		http.Error(w, "node_id, public_key, and public_ip are required", http.StatusBadRequest)
		return
	}

	// node_id becomes the row's primary key and is read back as a peer id by
	// every consumer of the table, so a malformed one is the caller's mistake,
	// not a server error.
	if _, err := peer.Decode(req.NodeID); err != nil {
		http.Error(w, "node_id must be a valid libp2p peer id", http.StatusBadRequest)
		return
	}

	// The public key is rendered into wg0.conf on every node, so it is held to
	// the same bar as the join handler's: a real Curve25519 key, and no control
	// characters that could append directives of the caller's choosing.
	if strings.ContainsAny(req.PublicKey, "\n\r") {
		http.Error(w, "public_key contains invalid characters", http.StatusBadRequest)
		return
	}
	if keyBytes, err := base64.StdEncoding.DecodeString(req.PublicKey); err != nil || len(keyBytes) != 32 {
		http.Error(w, "public_key must be a valid base64-encoded 32-byte key", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	wgIP, err := overlay.Register(ctx, h.rqliteClient, overlay.Peer{
		NodeID:    req.NodeID,
		PublicKey: req.PublicKey,
		PublicIP:  req.PublicIP,
		WGPort:    req.WGPort,
	})
	if err != nil {
		h.logger.Error("failed to register WG peer", zap.Error(err))
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

	if !h.validateInternalRequest(r) {
		http.Error(w, "unauthorized", http.StatusForbidden)
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

	if !h.validateInternalRequest(r) {
		http.Error(w, "unauthorized", http.StatusForbidden)
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

// validateInternalRequest checks that the request comes from a WireGuard peer
// and includes a valid cluster secret. Both conditions must be met.
func (h *Handler) validateInternalRequest(r *http.Request) bool {
	if !auth.IsWireGuardPeer(r.RemoteAddr) {
		return false
	}
	// No configured secret means no way to authenticate the caller, so there is
	// nothing to be permissive about: it used to return true here, making every
	// internal endpoint on such a gateway open to anything that could reach the
	// overlay address.
	if h.clusterSecret == "" {
		h.logger.Warn("refusing an internal WireGuard request: this gateway has no cluster secret configured")
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Cluster-Secret")), []byte(h.clusterSecret)) == 1
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
