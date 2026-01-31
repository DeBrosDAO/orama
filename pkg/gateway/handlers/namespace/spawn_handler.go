package namespace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// SpawnRequest represents a request to spawn or stop a namespace instance
type SpawnRequest struct {
	Action    string `json:"action"` // "spawn-rqlite", "spawn-olric", "stop-rqlite", "stop-olric"
	Namespace string `json:"namespace"`
	NodeID    string `json:"node_id"`

	// RQLite config (when action = "spawn-rqlite")
	RQLiteHTTPPort    int      `json:"rqlite_http_port,omitempty"`
	RQLiteRaftPort    int      `json:"rqlite_raft_port,omitempty"`
	RQLiteHTTPAdvAddr string   `json:"rqlite_http_adv_addr,omitempty"`
	RQLiteRaftAdvAddr string   `json:"rqlite_raft_adv_addr,omitempty"`
	RQLiteJoinAddrs   []string `json:"rqlite_join_addrs,omitempty"`
	RQLiteIsLeader    bool     `json:"rqlite_is_leader,omitempty"`

	// Olric config (when action = "spawn-olric")
	OlricHTTPPort       int      `json:"olric_http_port,omitempty"`
	OlricMemberlistPort int      `json:"olric_memberlist_port,omitempty"`
	OlricBindAddr       string   `json:"olric_bind_addr,omitempty"`
	OlricAdvertiseAddr  string   `json:"olric_advertise_addr,omitempty"`
	OlricPeerAddresses  []string `json:"olric_peer_addresses,omitempty"`
}

// SpawnResponse represents the response from a spawn/stop request
type SpawnResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	PID     int    `json:"pid,omitempty"`
}

// SpawnHandler handles remote namespace instance spawn/stop requests.
// It tracks spawned RQLite instances locally so they can be stopped later.
type SpawnHandler struct {
	rqliteSpawner    *rqlite.InstanceSpawner
	olricSpawner     *olric.InstanceSpawner
	logger           *zap.Logger
	rqliteInstances  map[string]*rqlite.Instance // key: "namespace:nodeID"
	rqliteInstanceMu sync.RWMutex
}

// NewSpawnHandler creates a new spawn handler
func NewSpawnHandler(rs *rqlite.InstanceSpawner, os *olric.InstanceSpawner, logger *zap.Logger) *SpawnHandler {
	return &SpawnHandler{
		rqliteSpawner:   rs,
		olricSpawner:    os,
		logger:          logger.With(zap.String("component", "namespace-spawn-handler")),
		rqliteInstances: make(map[string]*rqlite.Instance),
	}
}

// ServeHTTP implements http.Handler
func (h *SpawnHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authenticate via internal auth header
	if r.Header.Get("X-Orama-Internal-Auth") != "namespace-coordination" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req SpawnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSpawnResponse(w, http.StatusBadRequest, SpawnResponse{Error: "invalid request body"})
		return
	}

	if req.Namespace == "" || req.NodeID == "" {
		writeSpawnResponse(w, http.StatusBadRequest, SpawnResponse{Error: "namespace and node_id are required"})
		return
	}

	h.logger.Info("Received spawn request",
		zap.String("action", req.Action),
		zap.String("namespace", req.Namespace),
		zap.String("node_id", req.NodeID),
	)

	ctx := r.Context()

	switch req.Action {
	case "spawn-rqlite":
		cfg := rqlite.InstanceConfig{
			Namespace:      req.Namespace,
			NodeID:         req.NodeID,
			HTTPPort:       req.RQLiteHTTPPort,
			RaftPort:       req.RQLiteRaftPort,
			HTTPAdvAddress: req.RQLiteHTTPAdvAddr,
			RaftAdvAddress: req.RQLiteRaftAdvAddr,
			JoinAddresses:  req.RQLiteJoinAddrs,
			IsLeader:       req.RQLiteIsLeader,
		}
		instance, err := h.rqliteSpawner.SpawnInstance(ctx, cfg)
		if err != nil {
			h.logger.Error("Failed to spawn RQLite instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		// Track the instance for later stop requests
		key := fmt.Sprintf("%s:%s", req.Namespace, req.NodeID)
		h.rqliteInstanceMu.Lock()
		h.rqliteInstances[key] = instance
		h.rqliteInstanceMu.Unlock()
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true, PID: instance.PID})

	case "spawn-olric":
		cfg := olric.InstanceConfig{
			Namespace:      req.Namespace,
			NodeID:         req.NodeID,
			HTTPPort:       req.OlricHTTPPort,
			MemberlistPort: req.OlricMemberlistPort,
			BindAddr:       req.OlricBindAddr,
			AdvertiseAddr:  req.OlricAdvertiseAddr,
			PeerAddresses:  req.OlricPeerAddresses,
		}
		instance, err := h.olricSpawner.SpawnInstance(ctx, cfg)
		if err != nil {
			h.logger.Error("Failed to spawn Olric instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true, PID: instance.PID})

	case "stop-rqlite":
		key := fmt.Sprintf("%s:%s", req.Namespace, req.NodeID)
		h.rqliteInstanceMu.Lock()
		instance, ok := h.rqliteInstances[key]
		if ok {
			delete(h.rqliteInstances, key)
		}
		h.rqliteInstanceMu.Unlock()
		if !ok {
			writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true}) // Already stopped
			return
		}
		if err := h.rqliteSpawner.StopInstance(ctx, instance); err != nil {
			h.logger.Error("Failed to stop RQLite instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "stop-olric":
		if err := h.olricSpawner.StopInstance(ctx, req.Namespace, req.NodeID); err != nil {
			h.logger.Error("Failed to stop Olric instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	default:
		writeSpawnResponse(w, http.StatusBadRequest, SpawnResponse{Error: fmt.Sprintf("unknown action: %s", req.Action)})
	}
}

func writeSpawnResponse(w http.ResponseWriter, status int, resp SpawnResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
