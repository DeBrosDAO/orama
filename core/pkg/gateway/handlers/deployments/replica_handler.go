package deployments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"os/exec"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"go.uber.org/zap"
)

// ReplicaHandler handles internal node-to-node replica coordination endpoints.
type ReplicaHandler struct {
	service        *DeploymentService
	processManager *process.Manager
	ipfsClient     ipfs.IPFSClient
	logger         *zap.Logger
	baseDeployPath string
}

// NewReplicaHandler creates a new replica handler.
func NewReplicaHandler(
	service *DeploymentService,
	processManager *process.Manager,
	ipfsClient ipfs.IPFSClient,
	logger *zap.Logger,
	baseDeployPath string,
) *ReplicaHandler {
	if baseDeployPath == "" {
		baseDeployPath = filepath.Join(os.Getenv("HOME"), ".orama", "deployments")
	}
	return &ReplicaHandler{
		service:        service,
		processManager: processManager,
		ipfsClient:     ipfsClient,
		logger:         logger,
		baseDeployPath: baseDeployPath,
	}
}

// replicaSetupRequest is the payload for setting up a new replica.
type replicaSetupRequest struct {
	DeploymentID    string `json:"deployment_id"`
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	ContentCID      string `json:"content_cid"`
	BuildCID        string `json:"build_cid"`
	Environment     string `json:"environment"` // JSON-encoded env vars
	HealthCheckPath string `json:"health_check_path"`
	MemoryLimitMB   int    `json:"memory_limit_mb"`
	CPULimitPercent int    `json:"cpu_limit_percent"`
	RestartPolicy   string `json:"restart_policy"`
	MaxRestartCount int    `json:"max_restart_count"`
}

// HandleSetup sets up a new deployment replica on this node.
// POST /v1/internal/deployments/replica/setup
func (h *ReplicaHandler) HandleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.isInternalRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req replicaSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := process.ValidateInstance(req.Namespace, req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.logger.Info("Setting up deployment replica",
		zap.String("deployment_id", req.DeploymentID),
		zap.String("name", req.Name),
		zap.String("type", req.Type),
	)

	ctx := r.Context()

	// Allocate a port on this node
	port, err := h.service.portAllocator.AllocatePort(ctx, h.service.nodePeerID, req.DeploymentID)
	if err != nil {
		h.logger.Error("Failed to allocate port for replica", zap.Error(err))
		http.Error(w, "Failed to allocate port", http.StatusInternalServerError)
		return
	}

	// Release port if setup fails after this point
	setupOK := false
	defer func() {
		if !setupOK {
			if deallocErr := h.service.portAllocator.DeallocatePort(ctx, req.DeploymentID); deallocErr != nil {
				h.logger.Error("Failed to deallocate port after setup failure", zap.Error(deallocErr))
			}
		}
	}()

	// Claim the instance on this host: another gateway here may already run
	// a deployment whose instance this one maps to (instance_claim.go).
	claim, err := h.service.claimInstance(ctx, h.baseDeployPath, req.Namespace, req.Name)
	if err != nil {
		writeDeploymentNameError(w, h.logger, err)
		return
	}
	defer func() {
		if !setupOK {
			h.service.releaseClaim(claim)
		}
	}()
	deployPath := claim.dir

	// Extract content from IPFS
	cid := req.BuildCID
	if cid == "" {
		cid = req.ContentCID
	}

	if err := h.extractFromIPFS(ctx, cid, deployPath); err != nil {
		h.logger.Error("Failed to extract IPFS content for replica", zap.Error(err))
		http.Error(w, "Failed to extract content", http.StatusInternalServerError)
		return
	}

	// Read the environment. It arrives sealed with the cluster key, which
	// every node derives identically. An environment that cannot be read is
	// not an empty environment: starting the replica without its database URL
	// would look like the tenant's own bug.
	env, envErr := h.service.decodeEnvironment(req.Namespace, req.Name, req.Environment)
	if envErr != nil {
		h.logger.Error("Failed to read the replica's environment", zap.Error(envErr))
		http.Error(w, "Failed to read the deployment environment", http.StatusBadRequest)
		return
	}

	// Build a Deployment struct for the process manager
	deployment := &deployments.Deployment{
		ID:              req.DeploymentID,
		Namespace:       req.Namespace,
		Name:            req.Name,
		Type:            deployments.DeploymentType(req.Type),
		Port:            port,
		HomeNodeID:      h.service.nodePeerID,
		ContentCID:      req.ContentCID,
		BuildCID:        req.BuildCID,
		Environment:     env,
		HealthCheckPath: req.HealthCheckPath,
		MemoryLimitMB:   req.MemoryLimitMB,
		CPULimitPercent: req.CPULimitPercent,
		RestartPolicy:   deployments.RestartPolicy(req.RestartPolicy),
		MaxRestartCount: req.MaxRestartCount,
	}

	// A replica gets no server-side install; it must not run with what an
	// earlier holder of this instance installed on this node either.
	if process.UsesBuildOutput(deployment.Type) {
		if err := h.processManager.ClearDependencies(deployment.Namespace, deployment.Name); err != nil {
			h.logger.Error("Failed to clear the replica's build output", zap.Error(err))
			http.Error(w, "Failed to prepare the replica", http.StatusInternalServerError)
			return
		}
	}

	// Start the process
	if err := h.processManager.Start(ctx, deployment, deployPath); err != nil {
		h.logger.Error("Failed to start replica process", zap.Error(err))
		http.Error(w, fmt.Sprintf("Failed to start process: %v", err), http.StatusInternalServerError)
		return
	}

	setupOK = true

	// Wait for health check
	if err := h.processManager.WaitForHealthy(ctx, deployment, 90*time.Second); err != nil {
		h.logger.Warn("Replica did not become healthy", zap.Error(err))
	}

	// Update replica record to active with the port
	if h.service.replicaManager != nil {
		h.service.replicaManager.CreateReplica(ctx, req.DeploymentID, h.service.nodePeerID, port, false, deployments.ReplicaStatusActive)
	}

	resp := map[string]interface{}{
		"status":  "active",
		"port":    port,
		"node_id": h.service.nodePeerID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// replicaUpdateRequest is the payload for updating a replica.
type replicaUpdateRequest struct {
	DeploymentID string `json:"deployment_id"`
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	ContentCID   string `json:"content_cid"`
	BuildCID     string `json:"build_cid"`
	NewVersion   int    `json:"new_version"`
}

// HandleUpdate updates a deployment replica on this node.
// POST /v1/internal/deployments/replica/update
func (h *ReplicaHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.isInternalRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req replicaUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := process.ValidateInstance(req.Namespace, req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.logger.Info("Updating deployment replica",
		zap.String("deployment_id", req.DeploymentID),
		zap.String("name", req.Name),
	)

	ctx := r.Context()
	deployType := deployments.DeploymentType(req.Type)

	isStatic := deployType == deployments.DeploymentTypeStatic ||
		deployType == deployments.DeploymentTypeNextJSStatic ||
		deployType == deployments.DeploymentTypeGoWASM

	if isStatic {
		// Static deployments: nothing to do locally, IPFS handles content
		resp := map[string]interface{}{"status": "updated"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Dynamic deployment: extract new content and restart
	cid := req.BuildCID
	if cid == "" {
		cid = req.ContentCID
	}

	deployPath := process.DeployDir(h.baseDeployPath, req.Namespace, req.Name)
	stagingPath := deployPath + ".new"
	oldPath := deployPath + ".old"

	// The directory on this host must be this deployment's before anything
	// replaces it (instance_claim.go).
	if err := h.service.checkInstanceOwner(ctx, deployPath, req.Namespace, req.Name); err != nil {
		writeDeploymentNameError(w, h.logger, err)
		return
	}

	// Extract to staging
	if err := os.MkdirAll(stagingPath, 0755); err != nil {
		http.Error(w, "Failed to create staging directory", http.StatusInternalServerError)
		return
	}

	if err := h.extractFromIPFS(ctx, cid, stagingPath); err != nil {
		os.RemoveAll(stagingPath)
		http.Error(w, "Failed to extract content", http.StatusInternalServerError)
		return
	}
	// The staged directory replaces the claimed one, so it carries the marker.
	if err := writeOwnerMarker(stagingPath, req.Namespace, req.Name, false); err != nil {
		h.logger.Error("Failed to mark the staged replica directory", zap.Error(err))
		os.RemoveAll(stagingPath)
		http.Error(w, "Failed to stage the update", http.StatusInternalServerError)
		return
	}

	// Atomic swap
	if err := os.Rename(deployPath, oldPath); err != nil {
		os.RemoveAll(stagingPath)
		http.Error(w, "Failed to backup current deployment", http.StatusInternalServerError)
		return
	}

	if err := os.Rename(stagingPath, deployPath); err != nil {
		os.Rename(oldPath, deployPath)
		http.Error(w, "Failed to activate new deployment", http.StatusInternalServerError)
		return
	}

	// Get the port for this replica
	var port int
	if h.service.replicaManager != nil {
		p, err := h.service.replicaManager.GetReplicaPort(ctx, req.DeploymentID, h.service.nodePeerID)
		if err == nil {
			port = p
		}
	}

	// Restart the process
	deployment := &deployments.Deployment{
		ID:         req.DeploymentID,
		Namespace:  req.Namespace,
		Name:       req.Name,
		Type:       deployType,
		Port:       port,
		HomeNodeID: h.service.nodePeerID,
	}

	if err := h.processManager.Restart(ctx, deployment); err != nil {
		// Rollback
		os.Rename(deployPath, stagingPath)
		os.Rename(oldPath, deployPath)
		h.processManager.Restart(ctx, deployment)
		http.Error(w, fmt.Sprintf("Failed to restart: %v", err), http.StatusInternalServerError)
		return
	}

	// Health check
	if err := h.processManager.WaitForHealthy(ctx, deployment, 60*time.Second); err != nil {
		h.logger.Warn("Replica unhealthy after update, rolling back", zap.Error(err))
		os.Rename(deployPath, stagingPath)
		os.Rename(oldPath, deployPath)
		h.processManager.Restart(ctx, deployment)
		http.Error(w, "Health check failed after update", http.StatusInternalServerError)
		return
	}

	os.RemoveAll(oldPath)

	resp := map[string]interface{}{"status": "updated"}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleRollback rolls back a deployment replica on this node.
// POST /v1/internal/deployments/replica/rollback
func (h *ReplicaHandler) HandleRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.isInternalRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Rollback uses the same logic as update — the caller sends the target CID
	h.HandleUpdate(w, r)
}

// replicaTeardownRequest is the payload for tearing down a replica.
type replicaTeardownRequest struct {
	DeploymentID string `json:"deployment_id"`
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	Type         string `json:"type"`
}

// HandleTeardown removes a deployment replica from this node.
// POST /v1/internal/deployments/replica/teardown
func (h *ReplicaHandler) HandleTeardown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.isInternalRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req replicaTeardownRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := process.ValidateInstance(req.Namespace, req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.logger.Info("Tearing down deployment replica",
		zap.String("deployment_id", req.DeploymentID),
		zap.String("name", req.Name),
	)

	ctx := r.Context()

	// The unit and the directory are this deployment's only if the directory
	// on this host is: another gateway here may run a deployment whose
	// instance this one maps to, and stopping it or deleting its files would
	// take that one down.
	deployPath := process.DeployDir(h.baseDeployPath, req.Namespace, req.Name)
	owned, err := h.service.ownsInstanceDir(ctx, deployPath, req.Namespace, req.Name)
	if err != nil {
		h.logger.Error("Failed to check who owns the replica's directory", zap.Error(err))
		http.Error(w, "Failed to check the replica's directory", http.StatusInternalServerError)
		return
	}

	// Get port for this replica before teardown
	var port int
	if h.service.replicaManager != nil {
		p, err := h.service.replicaManager.GetReplicaPort(ctx, req.DeploymentID, h.service.nodePeerID)
		if err == nil {
			port = p
		}
	}

	// Stop the process
	deployment := &deployments.Deployment{
		ID:         req.DeploymentID,
		Namespace:  req.Namespace,
		Name:       req.Name,
		Type:       deployments.DeploymentType(req.Type),
		Port:       port,
		HomeNodeID: h.service.nodePeerID,
	}

	if owned {
		if err := h.processManager.Stop(ctx, deployment); err != nil {
			h.logger.Warn("Failed to stop replica process", zap.Error(err))
		}
		// Removing the directory releases the instance on this host.
		if err := os.RemoveAll(deployPath); err != nil {
			h.logger.Error("Failed to remove replica files", zap.String("path", deployPath), zap.Error(err))
			http.Error(w, "Failed to remove the replica's files", http.StatusInternalServerError)
			return
		}
	} else {
		h.logger.Warn("Left the replica's unit and directory alone: another deployment on this host holds its instance",
			zap.String("instance", process.InstanceName(req.Namespace, req.Name)))
	}

	// Deallocate the port
	if err := h.service.portAllocator.DeallocatePort(ctx, req.DeploymentID); err != nil {
		h.logger.Warn("Failed to deallocate port during teardown", zap.Error(err))
	}

	// Update replica status
	if h.service.replicaManager != nil {
		h.service.replicaManager.UpdateReplicaStatus(ctx, req.DeploymentID, h.service.nodePeerID, deployments.ReplicaStatusRemoving)
	}

	resp := map[string]interface{}{"status": "removed"}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// extractFromIPFS downloads and extracts a tarball from IPFS.
func (h *ReplicaHandler) extractFromIPFS(ctx context.Context, cid, destPath string) error {
	reader, err := h.ipfsClient.Get(ctx, "/ipfs/"+cid, "")
	if err != nil {
		return err
	}
	defer reader.Close()

	tmpFile, err := os.CreateTemp("", "replica-deploy-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := tmpFile.ReadFrom(reader); err != nil {
		return err
	}
	tmpFile.Close()

	cmd := exec.Command("tar", tarExtractArgs(tmpFile.Name(), destPath)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to extract tarball: %s: %w", string(output), err)
	}

	return nil
}

// isInternalRequest checks if the request is an internal node-to-node call.
// Requires both the static auth header AND that the request originates from
// the WireGuard mesh subnet (cryptographic peer authentication).
func (h *ReplicaHandler) isInternalRequest(r *http.Request) bool {
	if r.Header.Get("X-Orama-Internal-Auth") != "replica-coordination" {
		return false
	}
	return auth.IsWireGuardPeer(r.RemoteAddr)
}
