// Package namespace provides HTTP handlers for namespace cluster operations
package namespace

import (
	"encoding/json"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/logging"
	ns "github.com/DeBrosOfficial/network/pkg/namespace"
	"go.uber.org/zap"
)

// StatusHandler handles namespace cluster status requests
type StatusHandler struct {
	clusterManager *ns.ClusterManager
	logger         *zap.Logger
}

// NewStatusHandler creates a new namespace status handler
func NewStatusHandler(clusterManager *ns.ClusterManager, logger *logging.ColoredLogger) *StatusHandler {
	return &StatusHandler{
		clusterManager: clusterManager,
		logger:         logger.Logger.With(zap.String("handler", "namespace-status")),
	}
}

// StatusResponse represents the response for /v1/namespace/status
type StatusResponse struct {
	ClusterID    string   `json:"cluster_id"`
	Namespace    string   `json:"namespace"`
	Status       string   `json:"status"`
	Nodes        []string `json:"nodes"`
	RQLiteReady  bool     `json:"rqlite_ready"`
	OlricReady   bool     `json:"olric_ready"`
	GatewayReady bool     `json:"gateway_ready"`
	DNSReady     bool     `json:"dns_ready"`
	Error        string   `json:"error,omitempty"`
	GatewayURL   string   `json:"gateway_url,omitempty"`
}

// Handle handles GET /v1/namespace/status?id={cluster_id}
func (h *StatusHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	clusterID := r.URL.Query().Get("id")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "cluster_id parameter required")
		return
	}

	ctx := r.Context()
	status, err := h.clusterManager.GetClusterStatus(ctx, clusterID)
	if err != nil {
		h.logger.Error("Failed to get cluster status",
			zap.String("cluster_id", clusterID),
			zap.Error(err),
		)
		writeError(w, http.StatusNotFound, "cluster not found")
		return
	}

	resp := StatusResponse{
		ClusterID:    status.ClusterID,
		Namespace:    status.Namespace,
		Status:       string(status.Status),
		Nodes:        status.Nodes,
		RQLiteReady:  status.RQLiteReady,
		OlricReady:   status.OlricReady,
		GatewayReady: status.GatewayReady,
		DNSReady:     status.DNSReady,
		Error:        status.Error,
	}

	// Include gateway URL when ready
	if status.Status == ns.ClusterStatusReady {
		// Gateway URL would be constructed from cluster configuration
		// For now, we'll leave it empty and let the client construct it
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleByName handles GET /v1/namespace/status/name/{namespace}
func (h *StatusHandler) HandleByName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract namespace from path
	path := r.URL.Path
	namespace := ""
	const prefix = "/v1/namespace/status/name/"
	if len(path) > len(prefix) {
		namespace = path[len(prefix):]
	}

	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace parameter required")
		return
	}

	cluster, err := h.clusterManager.GetClusterByNamespace(r.Context(), namespace)
	if err != nil {
		h.logger.Debug("Cluster not found for namespace",
			zap.String("namespace", namespace),
			zap.Error(err),
		)
		writeError(w, http.StatusNotFound, "cluster not found for namespace")
		return
	}

	status, err := h.clusterManager.GetClusterStatus(r.Context(), cluster.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get cluster status")
		return
	}

	resp := StatusResponse{
		ClusterID:    status.ClusterID,
		Namespace:    status.Namespace,
		Status:       string(status.Status),
		Nodes:        status.Nodes,
		RQLiteReady:  status.RQLiteReady,
		OlricReady:   status.OlricReady,
		GatewayReady: status.GatewayReady,
		DNSReady:     status.DNSReady,
		Error:        status.Error,
	}

	writeJSON(w, http.StatusOK, resp)
}

// ProvisionRequest represents a request to provision a new namespace cluster
type ProvisionRequest struct {
	Namespace    string `json:"namespace"`
	ProvisionedBy string `json:"provisioned_by"` // Wallet address
}

// ProvisionResponse represents the response when provisioning starts
type ProvisionResponse struct {
	Status               string `json:"status"`
	ClusterID            string `json:"cluster_id"`
	PollURL              string `json:"poll_url"`
	EstimatedTimeSeconds int    `json:"estimated_time_seconds"`
}

// HandleProvision handles POST /v1/namespace/provision
func (h *StatusHandler) HandleProvision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ProvisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	if req.Namespace == "" || req.ProvisionedBy == "" {
		writeError(w, http.StatusBadRequest, "namespace and provisioned_by are required")
		return
	}

	// Don't allow provisioning the "default" namespace this way
	if req.Namespace == "default" {
		writeError(w, http.StatusBadRequest, "cannot provision the default namespace")
		return
	}

	// Check if namespace exists
	// For now, we assume the namespace ID is passed or we look it up
	// This would typically be done through the auth service
	// For simplicity, we'll use a placeholder namespace ID

	h.logger.Info("Namespace provisioning requested",
		zap.String("namespace", req.Namespace),
		zap.String("provisioned_by", req.ProvisionedBy),
	)

	// Note: In a full implementation, we'd look up the namespace ID from the database
	// For now, we'll create a placeholder that indicates provisioning should happen
	// The actual provisioning is triggered through the auth flow

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "accepted",
		"message": "Provisioning request accepted. Use auth flow to provision namespace cluster.",
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
