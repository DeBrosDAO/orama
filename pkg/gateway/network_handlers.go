package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// networkStatusHandler handles GET /v1/network/status.
// It returns the network status including peer ID and connection information.
func (g *Gateway) networkStatusHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	// Use internal auth context to bypass client credential requirements
	ctx := client.WithInternalAuth(r.Context())
	status, err := g.client.Network().GetStatus(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Override with the node's actual peer ID if available
	// (the client's embedded host has a different temporary peer ID)
	if g.nodePeerID != "" {
		status.PeerID = g.nodePeerID
	}
	writeJSON(w, http.StatusOK, status)
}

// networkPeersHandler handles GET /v1/network/peers.
// It returns a list of connected peers in multiaddr format.
func (g *Gateway) networkPeersHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	// Use internal auth context to bypass client credential requirements
	ctx := client.WithInternalAuth(r.Context())
	peers, err := g.client.Network().GetPeers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Flatten peer addresses into a list of multiaddr strings
	// Each PeerInfo can have multiple addresses, so we collect all of them
	peerAddrs := make([]string, 0)
	for _, peer := range peers {
		// Add peer ID as /p2p/ multiaddr format
		if peer.ID != "" {
			peerAddrs = append(peerAddrs, "/p2p/"+peer.ID)
		}
		// Add all addresses for this peer
		peerAddrs = append(peerAddrs, peer.Addresses...)
	}
	// Return peers in expected format: {"peers": ["/p2p/...", "/ip4/...", ...]}
	writeJSON(w, http.StatusOK, map[string]any{"peers": peerAddrs})
}

// networkConnectHandler handles POST /v1/network/connect.
// It connects to a peer specified by multiaddr.
func (g *Gateway) networkConnectHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Multiaddr string `json:"multiaddr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Multiaddr == "" {
		writeError(w, http.StatusBadRequest, "invalid body: expected {multiaddr}")
		return
	}
	if err := g.client.Network().ConnectToPeer(r.Context(), body.Multiaddr); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// networkDisconnectHandler handles POST /v1/network/disconnect.
// It disconnects from a peer specified by peer ID.
func (g *Gateway) networkDisconnectHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		PeerID string `json:"peer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PeerID == "" {
		writeError(w, http.StatusBadRequest, "invalid body: expected {peer_id}")
		return
	}
	if err := g.client.Network().DisconnectFromPeer(r.Context(), body.PeerID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
