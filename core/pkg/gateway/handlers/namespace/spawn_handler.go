package namespace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	namespacepkg "github.com/DeBrosOfficial/network/pkg/namespace"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/sfu"
	"go.uber.org/zap"
)

// SpawnRequest represents a request to spawn or stop a namespace instance
type SpawnRequest struct {
	Action    string `json:"action"` // spawn-{rqlite,olric,gateway,sfu,turn}, stop-{rqlite,olric,gateway,sfu,turn}, teardown-{namespace,sfu,turn}, save-cluster-state, delete-cluster-state, reconcile-host-turn
	Namespace string `json:"namespace"`
	NodeID    string `json:"node_id"`

	// PurgeData, with action = "teardown-namespace", also removes the
	// namespace's tenant data (SQLite databases, deployment directories): the
	// namespace is being deleted, not moved or rolled back.
	PurgeData bool `json:"purge_data,omitempty"`

	// Release, with action = "reconcile-host-turn", asks the host to drop the
	// namespace from its shared TURN server instead of confirming it is served.
	Release bool `json:"release,omitempty"`

	// ClusterID, with a teardown-* action, is the cluster the teardown was asked
	// for. The node refuses it when its own state says the namespace here belongs
	// to another cluster: the name was created again, and the teardown would
	// delete the new namespace. Absent from a sender on the previous release,
	// whose teardown is then carried out as before.
	ClusterID string `json:"cluster_id,omitempty"`

	// RQLite config (when action = "spawn-rqlite")
	RQLiteHTTPPort    int      `json:"rqlite_http_port,omitempty"`
	RQLiteRaftPort    int      `json:"rqlite_raft_port,omitempty"`
	RQLiteHTTPAdvAddr string   `json:"rqlite_http_adv_addr,omitempty"`
	RQLiteRaftAdvAddr string   `json:"rqlite_raft_adv_addr,omitempty"`
	RQLiteJoinAddrs   []string `json:"rqlite_join_addrs,omitempty"`
	RQLiteIsLeader    bool     `json:"rqlite_is_leader,omitempty"`
	// Bugboard #281: clear leftover raft state before starting a brand-new
	// cluster, so re-creating a namespace of the same name is deterministic.
	RQLiteFreshStart bool `json:"rqlite_fresh_start,omitempty"`
	// Bugboard #275: HTTP base URL of the join target, verified to belong to this
	// namespace before rqlited is started.
	RQLiteJoinVerifyURL string `json:"rqlite_join_verify_url,omitempty"`

	// Olric config (when action = "spawn-olric")
	OlricHTTPPort       int      `json:"olric_http_port,omitempty"`
	OlricMemberlistPort int      `json:"olric_memberlist_port,omitempty"`
	OlricBindAddr       string   `json:"olric_bind_addr,omitempty"`
	OlricAdvertiseAddr  string   `json:"olric_advertise_addr,omitempty"`
	OlricPeerAddresses  []string `json:"olric_peer_addresses,omitempty"`

	// Gateway config (when action = "spawn-gateway")
	GatewayHTTPPort        int      `json:"gateway_http_port,omitempty"`
	GatewayBaseDomain      string   `json:"gateway_base_domain,omitempty"`
	GatewayRQLiteDSN       string   `json:"gateway_rqlite_dsn,omitempty"`
	GatewayGlobalRQLiteDSN string   `json:"gateway_global_rqlite_dsn,omitempty"`
	GatewayOlricServers    []string `json:"gateway_olric_servers,omitempty"`
	GatewayOlricTimeout    string   `json:"gateway_olric_timeout,omitempty"`
	IPFSClusterAPIURL      string   `json:"ipfs_cluster_api_url,omitempty"`
	IPFSAPIURL             string   `json:"ipfs_api_url,omitempty"`
	IPFSTimeout            string   `json:"ipfs_timeout,omitempty"`
	IPFSReplicationFactor  int      `json:"ipfs_replication_factor,omitempty"`
	// Gateway WebRTC config (when action = "spawn-gateway" and WebRTC is enabled)
	GatewayWebRTCEnabled bool   `json:"gateway_webrtc_enabled,omitempty"`
	GatewaySFUPort       int    `json:"gateway_sfu_port,omitempty"`
	GatewayTURNDomain    string `json:"gateway_turn_domain,omitempty"`
	GatewayTURNSecret    string `json:"gateway_turn_secret,omitempty"`
	// Stealth TURNS:443 host (feat-124); empty when stealth is disabled.
	GatewayTURNStealthDomain string `json:"gateway_turn_stealth_domain,omitempty"`
	// Host serverless secrets encryption key forwarded to the spawned
	// namespace gateway (bugboard #837 follow-up). Same value on every node.
	GatewaySecretsEncryptionKey string `json:"gateway_secrets_encryption_key,omitempty"`
	// Host self-hosted ntfy base URL forwarded to the spawned namespace
	// gateway (bugboard #274) so its ntfy push provider has a default
	// server. Same value on every node.
	GatewayNtfyBaseURL string `json:"gateway_ntfy_base_url,omitempty"`

	// SFU config (when action = "spawn-sfu")
	SFUListenAddr string                 `json:"sfu_listen_addr,omitempty"`
	SFUMediaStart int                    `json:"sfu_media_start,omitempty"`
	SFUMediaEnd   int                    `json:"sfu_media_end,omitempty"`
	TURNServers   []sfu.TURNServerConfig `json:"turn_servers,omitempty"`
	TURNSecret    string                 `json:"turn_secret,omitempty"`
	TURNCredTTL   int                    `json:"turn_cred_ttl,omitempty"`
	RQLiteDSN     string                 `json:"rqlite_dsn,omitempty"`

	// No TURN config fields: TURN is host-level since bugboard #283 part 2, so
	// there is no remote spawn to carry one. The surviving "stop-turn" action
	// needs only Namespace and NodeID. The removed set included turn_auth_secret,
	// a secret this struct no longer carries over the wire at all.

	// Cluster state (when action = "save-cluster-state")
	ClusterState json.RawMessage `json:"cluster_state,omitempty"`
}

// SpawnResponse represents the response from a spawn/stop request
type SpawnResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	PID     int    `json:"pid,omitempty"`
}

// SpawnHandler handles remote namespace instance spawn/stop requests.
// Now uses systemd for service management instead of direct process spawning.
type SpawnHandler struct {
	systemdSpawner *namespacepkg.SystemdSpawner
	logger         *zap.Logger
	// clusterSecretPath is where this node keeps the cluster secret. It is
	// read per request rather than at construction so that a node whose secret
	// is rotated does not have to be restarted to accept coordination calls.
	clusterSecretPath string
	// nodeID is this node's peer ID. A spawn request is addressed to one node by
	// the node_id in its body; a request for any other node is refused, so a
	// stamped request captured on its way to one node cannot act on another.
	nodeID string
	// hostTURN applies this host's shared TURN tenant set and confirms the
	// namespace is served (action = "reconcile-host-turn").
	hostTURN HostTURNConfirmer
}

// hostTURNRequestTimeout bounds a reconcile-host-turn request. It runs under its
// own context, not the request's: the reconcile may wait for the TURN server to
// reload, and an unbounded background context would let a stuck host hold the
// handler forever.
const hostTURNRequestTimeout = 30 * time.Second

// HostTURNConfirmer reconciles the host's shared TURN server and reports an
// error unless it serves the namespace (Confirm), or unless it has dropped it
// (Release).
type HostTURNConfirmer interface {
	ConfirmHostTURN(ctx context.Context, namespace string) error
	ReleaseHostTURN(ctx context.Context, namespace string) error
}

// SetHostTURN wires the confirmer for the "reconcile-host-turn" action.
func (h *SpawnHandler) SetHostTURN(c HostTURNConfirmer) { h.hostTURN = c }

// NewSpawnHandler creates a new spawn handler
func NewSpawnHandler(systemdSpawner *namespacepkg.SystemdSpawner, clusterSecretPath, nodeID string, logger *zap.Logger) *SpawnHandler {
	return &SpawnHandler{
		systemdSpawner:    systemdSpawner,
		clusterSecretPath: clusterSecretPath,
		nodeID:            nodeID,
		logger:            logger.With(zap.String("component", "namespace-spawn-handler")),
	}
}

// verifyCoordination reports whether this spawn request came from inside the
// cluster. See pkg/auth/coordination.go. Only the v2 stamp is accepted: every
// spawn action carries its parameters in the body (DSNs, peer addresses, TURN
// and encryption secrets), which the v1 stamp does not cover.
func (h *SpawnHandler) verifyCoordination(r *http.Request) bool {
	if !auth.IsWireGuardPeer(r.RemoteAddr) {
		return false
	}
	secret, err := os.ReadFile(h.clusterSecretPath)
	if err != nil {
		h.logger.Error("cannot read the cluster secret, so no coordination request can be authenticated",
			zap.String("path", h.clusterSecretPath), zap.Error(err))
		return false
	}
	key, err := auth.CoordinationKey(string(secret))
	if err != nil {
		return false
	}
	return auth.VerifyCoordinationV2(key, r, time.Now(), h.nodeID)
}

// ServeHTTP implements http.Handler
func (h *SpawnHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Two independent things: the request carries a MAC produced with a key
	// derived from the cluster secret, and it arrived over the WireGuard
	// overlay. The MAC is the credential — the header this replaces was a
	// constant in the source, and being on the overlay is not a privilege,
	// since every namespace's services are on that mesh.
	//
	// The action and the namespace are in the body, so verification reads it
	// (bounded, and restored for the decode below) and the v2 MAC covers it.
	if !h.verifyCoordination(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req SpawnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSpawnResponse(w, http.StatusBadRequest, SpawnResponse{Error: "invalid request body"})
		return
	}
	if req.Namespace == "" || req.NodeID == "" {
		writeSpawnResponse(w, http.StatusBadRequest, SpawnResponse{Error: "namespace and node_id are required"})
		return
	}
	if req.NodeID != h.nodeID {
		h.logger.Warn("refused a spawn request addressed to another node",
			zap.String("action", req.Action), zap.String("namespace", req.Namespace),
			zap.String("request_node_id", req.NodeID), zap.String("this_node_id", h.nodeID))
		writeSpawnResponse(w, http.StatusForbidden, SpawnResponse{Error: "node_id is not this node"})
		return
	}
	if err := req.validate(); err != nil {
		writeSpawnResponse(w, http.StatusBadRequest, SpawnResponse{Error: err.Error()})
		return
	}

	h.logger.Info("Received spawn request",
		zap.String("action", req.Action),
		zap.String("namespace", req.Namespace),
		zap.String("node_id", req.NodeID),
	)

	// Use a background context for spawn operations so processes outlive the HTTP request.
	// Stop operations can use request context since they're short-lived.
	ctx := context.Background()

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
			FreshStart:     req.RQLiteFreshStart,
			JoinVerifyURL:  req.RQLiteJoinVerifyURL,
		}
		if err := h.systemdSpawner.SpawnRQLite(ctx, req.Namespace, req.NodeID, cfg); err != nil {
			h.logger.Error("Failed to spawn RQLite instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "spawn-olric":
		// Reject empty or 0.0.0.0 BindAddr early — these cause IPv6 resolution on dual-stack hosts
		if req.OlricBindAddr == "" || req.OlricBindAddr == "0.0.0.0" {
			writeSpawnResponse(w, http.StatusBadRequest, SpawnResponse{
				Error: fmt.Sprintf("olric_bind_addr must be a valid IP, got %q", req.OlricBindAddr),
			})
			return
		}
		cfg := olric.InstanceConfig{
			Namespace:      req.Namespace,
			NodeID:         req.NodeID,
			HTTPPort:       req.OlricHTTPPort,
			MemberlistPort: req.OlricMemberlistPort,
			BindAddr:       req.OlricBindAddr,
			AdvertiseAddr:  req.OlricAdvertiseAddr,
			PeerAddresses:  req.OlricPeerAddresses,
		}
		if err := h.systemdSpawner.SpawnOlric(ctx, req.Namespace, req.NodeID, cfg); err != nil {
			h.logger.Error("Failed to spawn Olric instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "stop-rqlite":
		if err := h.systemdSpawner.StopRQLite(ctx, req.Namespace, req.NodeID); err != nil {
			h.logger.Error("Failed to stop RQLite instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "stop-olric":
		if err := h.systemdSpawner.StopOlric(ctx, req.Namespace, req.NodeID); err != nil {
			h.logger.Error("Failed to stop Olric instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "spawn-gateway":
		// Parse IPFS timeout if provided
		var ipfsTimeout time.Duration
		if req.IPFSTimeout != "" {
			var err error
			ipfsTimeout, err = time.ParseDuration(req.IPFSTimeout)
			if err != nil {
				h.logger.Warn("Invalid IPFS timeout, using default", zap.String("timeout", req.IPFSTimeout), zap.Error(err))
				ipfsTimeout = 60 * time.Second
			}
		}

		// Parse Olric timeout if provided
		var olricTimeout time.Duration
		if req.GatewayOlricTimeout != "" {
			var err error
			olricTimeout, err = time.ParseDuration(req.GatewayOlricTimeout)
			if err != nil {
				h.logger.Warn("Invalid Olric timeout, using default", zap.String("timeout", req.GatewayOlricTimeout), zap.Error(err))
				olricTimeout = 30 * time.Second
			}
		} else {
			olricTimeout = 30 * time.Second
		}

		cfg := gateway.InstanceConfig{
			Namespace:             req.Namespace,
			NodeID:                req.NodeID,
			HTTPPort:              req.GatewayHTTPPort,
			BaseDomain:            req.GatewayBaseDomain,
			RQLiteDSN:             req.GatewayRQLiteDSN,
			GlobalRQLiteDSN:       req.GatewayGlobalRQLiteDSN,
			OlricServers:          req.GatewayOlricServers,
			OlricTimeout:          olricTimeout,
			IPFSClusterAPIURL:     req.IPFSClusterAPIURL,
			IPFSAPIURL:            req.IPFSAPIURL,
			IPFSTimeout:           ipfsTimeout,
			IPFSReplicationFactor: req.IPFSReplicationFactor,
			WebRTCEnabled:         req.GatewayWebRTCEnabled,
			SFUPort:               req.GatewaySFUPort,
			TURNDomain:            req.GatewayTURNDomain,
			TURNStealthDomain:     req.GatewayTURNStealthDomain,
			TURNSecret:            req.GatewayTURNSecret,
			SecretsEncryptionKey:  req.GatewaySecretsEncryptionKey,
			NtfyBaseURL:           req.GatewayNtfyBaseURL,
		}
		if err := h.systemdSpawner.SpawnGateway(ctx, req.Namespace, req.NodeID, cfg); err != nil {
			h.logger.Error("Failed to spawn Gateway instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "stop-gateway":
		if err := h.systemdSpawner.StopGateway(ctx, req.Namespace, req.NodeID); err != nil {
			h.logger.Error("Failed to stop Gateway instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "restart-gateway":
		// Restart gateway with updated config (used by EnableWebRTC/DisableWebRTC)
		var ipfsTimeout time.Duration
		if req.IPFSTimeout != "" {
			var err error
			ipfsTimeout, err = time.ParseDuration(req.IPFSTimeout)
			if err != nil {
				ipfsTimeout = 60 * time.Second
			}
		}
		var olricTimeout time.Duration
		if req.GatewayOlricTimeout != "" {
			var err error
			olricTimeout, err = time.ParseDuration(req.GatewayOlricTimeout)
			if err != nil {
				olricTimeout = 30 * time.Second
			}
		} else {
			olricTimeout = 30 * time.Second
		}
		cfg := gateway.InstanceConfig{
			Namespace:             req.Namespace,
			NodeID:                req.NodeID,
			HTTPPort:              req.GatewayHTTPPort,
			BaseDomain:            req.GatewayBaseDomain,
			RQLiteDSN:             req.GatewayRQLiteDSN,
			GlobalRQLiteDSN:       req.GatewayGlobalRQLiteDSN,
			OlricServers:          req.GatewayOlricServers,
			OlricTimeout:          olricTimeout,
			IPFSClusterAPIURL:     req.IPFSClusterAPIURL,
			IPFSAPIURL:            req.IPFSAPIURL,
			IPFSTimeout:           ipfsTimeout,
			IPFSReplicationFactor: req.IPFSReplicationFactor,
			WebRTCEnabled:         req.GatewayWebRTCEnabled,
			SFUPort:               req.GatewaySFUPort,
			TURNDomain:            req.GatewayTURNDomain,
			TURNStealthDomain:     req.GatewayTURNStealthDomain,
			TURNSecret:            req.GatewayTURNSecret,
			SecretsEncryptionKey:  req.GatewaySecretsEncryptionKey,
			NtfyBaseURL:           req.GatewayNtfyBaseURL,
		}
		if err := h.systemdSpawner.RestartGateway(ctx, req.Namespace, req.NodeID, cfg); err != nil {
			h.logger.Error("Failed to restart Gateway instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "save-cluster-state":
		if len(req.ClusterState) == 0 {
			writeSpawnResponse(w, http.StatusBadRequest, SpawnResponse{Error: "cluster_state is required"})
			return
		}
		if err := h.systemdSpawner.SaveClusterState(req.Namespace, req.ClusterState); err != nil {
			h.logger.Error("Failed to save cluster state", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "delete-cluster-state":
		if err := h.systemdSpawner.DeleteClusterState(req.Namespace); err != nil {
			h.logger.Error("Failed to delete cluster state", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "teardown-namespace":
		// Stop AND disable every unit of the namespace, then delete its data and
		// env files: nothing is left for an upgrade or boot to start again. The
		// stop-* actions only stop, and are for restarts.
		// purge_data is the namespace's delete: its SQLite databases and
		// deployment directories go too. A node on the previous release ignores
		// the field and keeps them.
		if err := h.systemdSpawner.TeardownNamespaceOfCluster(ctx, req.Namespace, req.ClusterID, req.PurgeData); err != nil {
			h.writeTeardownFailure(w, "Failed to tear down namespace", err)
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "spawn-sfu":
		cfg := namespacepkg.SFUInstanceConfig{
			Namespace:      req.Namespace,
			NodeID:         req.NodeID,
			ListenAddr:     req.SFUListenAddr,
			MediaPortStart: req.SFUMediaStart,
			MediaPortEnd:   req.SFUMediaEnd,
			TURNServers:    req.TURNServers,
			TURNSecret:     req.TURNSecret,
			TURNCredTTL:    req.TURNCredTTL,
			RQLiteDSN:      req.RQLiteDSN,
		}
		if err := h.systemdSpawner.SpawnSFU(ctx, req.Namespace, req.NodeID, cfg); err != nil {
			h.logger.Error("Failed to spawn SFU instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "stop-sfu":
		if err := h.systemdSpawner.StopSFU(ctx, req.Namespace, req.NodeID); err != nil {
			h.logger.Error("Failed to stop SFU instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "teardown-sfu":
		// Stop, disable and remove the env/config: a restart must not find it.
		if err := h.systemdSpawner.TeardownSFUOfCluster(ctx, req.Namespace, req.NodeID, req.ClusterID); err != nil {
			h.writeTeardownFailure(w, "Failed to tear down SFU instance", err)
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "reconcile-host-turn":
		if h.hostTURN == nil {
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: "this node has no shared TURN reconciler"})
			return
		}
		turnCtx, cancel := context.WithTimeout(context.Background(), hostTURNRequestTimeout)
		defer cancel()
		reconcile, what := h.hostTURN.ConfirmHostTURN, "confirm the shared TURN server serves the namespace"
		if req.Release {
			reconcile, what = h.hostTURN.ReleaseHostTURN, "drop the namespace from the shared TURN server"
		}
		if err := reconcile(turnCtx, req.Namespace); err != nil {
			h.logger.Error("Failed to "+what, zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "teardown-turn":
		// Retires only the legacy per-namespace unit, never the shared host TURN.
		if err := h.systemdSpawner.TeardownTURNOfCluster(ctx, req.Namespace, req.NodeID, req.ClusterID); err != nil {
			h.writeTeardownFailure(w, "Failed to tear down TURN instance", err)
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	case "stop-turn":
		if err := h.systemdSpawner.StopTURN(ctx, req.Namespace, req.NodeID); err != nil {
			h.logger.Error("Failed to stop TURN instance", zap.Error(err))
			writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
			return
		}
		writeSpawnResponse(w, http.StatusOK, SpawnResponse{Success: true})

	default:
		writeSpawnResponse(w, http.StatusBadRequest, SpawnResponse{Error: fmt.Sprintf("unknown action: %s", req.Action)})
	}
}

// writeTeardownFailure answers a failed teardown: 409 when it was refused
// because the namespace on this node belongs to another cluster, 500 otherwise.
func (h *SpawnHandler) writeTeardownFailure(w http.ResponseWriter, msg string, err error) {
	if errors.Is(err, namespacepkg.ErrClusterMismatch) {
		h.logger.Warn(msg, zap.Error(err))
		writeSpawnResponse(w, http.StatusConflict, SpawnResponse{Error: err.Error()})
		return
	}
	h.logger.Error(msg, zap.Error(err))
	writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
}

func writeSpawnResponse(w http.ResponseWriter, status int, resp SpawnResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
