package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"go.uber.org/zap"
)

// HandlerOption adds routes to Handler.
type HandlerOption func(mux *http.ServeMux, logger *zap.Logger)

// meshPeersBody is the request to POST /mesh/peers.
type meshPeersBody struct {
	Addrs []string `json:"addrs"`
}

// WithMesh serves GET /mesh/self and POST /mesh/peers, through which the
// gateway makes this node's service and the other nodes' one GossipSub mesh.
func WithMesh(mesh *Mesh) HandlerOption {
	return func(mux *http.ServeMux, logger *zap.Logger) {
		mux.HandleFunc("/mesh/self", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			self, err := mesh.Self()
			if err != nil {
				logger.Warn("mesh self failed", zap.Error(err))
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSONReply(w, logger, self)
		})
		mux.HandleFunc("/mesh/peers", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var body meshPeersBody
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
				http.Error(w, "invalid body: expected {addrs: [...]}", http.StatusBadRequest)
				return
			}
			result, err := mesh.ConnectPeers(r.Context(), body.Addrs)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSONReply(w, logger, result)
		})
	}
}

func writeJSONReply(w http.ResponseWriter, logger *zap.Logger, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Warn("write reply failed", zap.Error(err))
	}
}

// MeshSelf asks the service for the address other nodes' services dial it at.
func (c *HTTPClient) MeshSelf(ctx context.Context) (MeshSelf, error) {
	var self MeshSelf
	if err := c.meshCall(ctx, http.MethodGet, "/mesh/self", nil, &self); err != nil {
		return MeshSelf{}, fmt.Errorf("pubsub mesh self: %w", err)
	}
	return self, nil
}

// MeshConnect has the service connect to the peers at addrs (see
// Mesh.ConnectPeers).
func (c *HTTPClient) MeshConnect(ctx context.Context, addrs []string) (MeshConnectResult, error) {
	var out MeshConnectResult
	if err := c.meshCall(ctx, http.MethodPost, "/mesh/peers", meshPeersBody{Addrs: addrs}, &out); err != nil {
		return MeshConnectResult{}, fmt.Errorf("pubsub mesh connect: %w", err)
	}
	return out, nil
}
