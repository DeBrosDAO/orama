package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// networkStatusHandler handles GET /v1/network/status.
// It returns the network status including peer ID and connection information.
func (g *Gateway) networkStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !g.authorizeNetworkDetail(w, r) {
		return
	}
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
	if !g.authorizeNetworkDetail(w, r) {
		return
	}
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

// networkMutationBodyMaxBytes bounds the body of connect and disconnect: a
// multiaddr or a peer id is far smaller.
const networkMutationBodyMaxBytes = 4096

// peerDialTimeout bounds how long connect waits for a peer to answer, so a
// target that swallows packets is a 504 and not a request held open.
const peerDialTimeout = 10 * time.Second

// authorizeNetworkMutation admits the callers that may change this gateway's
// peer connections: POST, from an operator on the operator list. It runs
// before the body is read, so a caller who is not an operator learns nothing
// about what the route accepts. It writes the refusal; false means the
// handler returns.
func (g *Gateway) authorizeNetworkMutation(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	if g.operatorHandler == nil {
		writeError(w, http.StatusServiceUnavailable, "this gateway cannot check the operator list")
		return false
	}
	if _, ok := g.operatorHandler.Authorize(w, r); !ok {
		return false
	}
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return false
	}
	return true
}

// decodeNetworkMutationBody reads the one-field JSON body of connect or
// disconnect. It writes the refusal; false means the handler returns.
func decodeNetworkMutationBody(w http.ResponseWriter, r *http.Request, dst any, field string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, networkMutationBodyMaxBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid body: expected {"+field+"}")
		return false
	}
	return true
}

// writeNetworkMutationError answers a failed connect or disconnect: a peer
// the caller named wrongly is 400, a dial that ran out of time is 504, and a
// dial the peer or the network refused is 502. Nothing the client reports
// here is this gateway's own fault.
func writeNetworkMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, client.ErrInvalidPeer):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, err.Error())
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// networkConnectHandler handles POST /v1/network/connect.
// It connects to a peer specified by multiaddr.
func (g *Gateway) networkConnectHandler(w http.ResponseWriter, r *http.Request) {
	if !g.authorizeNetworkMutation(w, r) {
		return
	}
	var body struct {
		Multiaddr string `json:"multiaddr"`
	}
	if !decodeNetworkMutationBody(w, r, &body, "multiaddr") {
		return
	}
	if body.Multiaddr == "" {
		writeError(w, http.StatusBadRequest, "invalid body: expected {multiaddr}")
		return
	}
	// Internal auth: this gateway's own client holds no API key or JWT; the
	// caller was authorized above.
	ctx, cancel := context.WithTimeout(client.WithInternalAuth(r.Context()), peerDialTimeout)
	defer cancel()
	if err := g.client.Network().ConnectToPeer(ctx, body.Multiaddr); err != nil {
		writeNetworkMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// networkDisconnectHandler handles POST /v1/network/disconnect.
// It disconnects from a peer specified by peer ID.
func (g *Gateway) networkDisconnectHandler(w http.ResponseWriter, r *http.Request) {
	if !g.authorizeNetworkMutation(w, r) {
		return
	}
	var body struct {
		PeerID string `json:"peer_id"`
	}
	if !decodeNetworkMutationBody(w, r, &body, "peer_id") {
		return
	}
	if body.PeerID == "" {
		writeError(w, http.StatusBadRequest, "invalid body: expected {peer_id}")
		return
	}
	if err := g.client.Network().DisconnectFromPeer(client.WithInternalAuth(r.Context()), body.PeerID); err != nil {
		writeNetworkMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
