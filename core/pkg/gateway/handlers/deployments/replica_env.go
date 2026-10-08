package deployments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"go.uber.org/zap"
)

const (
	// replicaEnvPath is the internal route a replica applies an environment on.
	replicaEnvPath = "/v1/internal/deployments/replica/env"

	// replicaEnvFailedEvent is the deployment event a replica that did not apply
	// an environment change is recorded under.
	replicaEnvFailedEvent = "replica_env_failed"
)

// The budgets of the chain in constants/timeouts.go, as variables so a test can
// shorten them.
var (
	replicaEnvReconfigureTimeout = constants.DeploymentEnvReconfigureTimeout
	replicaEnvCallTimeout        = constants.DeploymentEnvCallTimeout
)

// envReconfigurer rewrites a deployment's environment file and restarts it.
type envReconfigurer interface {
	Reconfigure(ctx context.Context, deployment *deployments.Deployment, workDir string) error
}

// ReconfigureReplicas makes every other node running the deployment apply its
// current environment, and returns only when each has answered. It returns the
// number of replicas that applied it. version is the stamp of this change
// (nextEnvVersion): a replica refuses an older one than it has applied.
//
// A replica that could not be reached, or refused, is reported in a
// *replicaFanOutError; running the same change again retries every replica,
// because applying an environment is idempotent. It does not retry only the
// failed ones: the replicas that applied it simply apply it again.
func (s *DeploymentService) ReconfigureReplicas(ctx context.Context, deployment *deployments.Deployment, version int64) (int, error) {
	storedEnv, err := s.EncodeEnvironment(deployment.Namespace, deployment.ID, deployment.Environment)
	if err != nil {
		return 0, fmt.Errorf("failed to encode the environment: %w", err)
	}
	return s.callReplicasAndWait(ctx, deployment, replicaFanOut{
		what: "environment change",
		path: replicaEnvPath,
		payload: map[string]interface{}{
			"deployment_id": deployment.ID,
			"namespace":     deployment.Namespace,
			"name":          deployment.Name,
			"environment":   storedEnv, // sealed with the cluster key
			"env_version":   version,
		},
		timeout:     replicaEnvCallTimeout,
		failedEvent: replicaEnvFailedEvent,
	})
}

// replicaEnvRequest is the payload of the internal replica env call. It names
// the deployment and carries the environment; the type, limits and port are the
// registry's, not the caller's.
type replicaEnvRequest struct {
	DeploymentID string `json:"deployment_id"`
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	Environment  string `json:"environment"` // sealed
	EnvVersion   int64  `json:"env_version"`
}

// reconfigurableTypes are the deployment types that run a process on a replica.
var reconfigurableTypes = map[deployments.DeploymentType]bool{
	deployments.DeploymentTypeNextJS:        true,
	deployments.DeploymentTypeNodeJSBackend: true,
	deployments.DeploymentTypeGoBackend:     true,
}

// HandleEnv applies a new environment to this node's replica of a deployment.
// POST /v1/internal/deployments/replica/env
func (h *ReplicaHandler) HandleEnv(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeReplicaError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if !h.isInternalRequest(r) {
		writeReplicaError(w, http.StatusForbidden, "Forbidden")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req replicaEnvRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeReplicaError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if err := process.ValidateInstance(req.Namespace, req.Name); err != nil {
		writeReplicaError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The bound is what keeps a restart from outliving the caller's wait, and
	// the detached context is what keeps a dropped connection from abandoning
	// one half way.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), replicaEnvReconfigureTimeout)
	defer cancel()

	unlock, err := h.service.lockDeployment(ctx, req.Namespace, req.Name)
	if err != nil {
		writeReplicaError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	defer unlock()

	if status, err := h.applyEnv(ctx, req); err != nil {
		writeReplicaError(w, status, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "reconfigured", "node_id": h.service.nodePeerID})
}

// applyEnv checks the request against the registry and this node's state, then
// reconfigures the replica. On a refusal it returns the status to answer with.
func (h *ReplicaHandler) applyEnv(ctx context.Context, req replicaEnvRequest) (int, error) {
	if h.service.replicaManager == nil {
		return http.StatusInternalServerError, errors.New("this node has no replica registry")
	}
	// The id, namespace and name must be one deployment, and this node must
	// hold an active replica of it. What the replica runs with comes from that
	// row, so a caller cannot choose another type or other limits.
	binding, err := h.service.replicaManager.LookupReplicaBinding(ctx, req.DeploymentID, req.Namespace, req.Name, h.service.nodePeerID)
	if errors.Is(err, deployments.ErrReplicaNotBound) {
		return http.StatusNotFound, err
	}
	if err != nil {
		h.logger.Error("Failed to look up the replica", zap.Error(err))
		return http.StatusInternalServerError, errors.New("failed to look up the replica")
	}
	if !reconfigurableTypes[binding.Type] {
		return http.StatusBadRequest, fmt.Errorf("a %s deployment has no process to reconfigure", binding.Type)
	}

	deployPath := process.DeployDir(h.baseDeployPath, req.Namespace, req.Name)
	if err := h.service.checkInstanceOwner(ctx, deployPath, req.Namespace, req.Name); err != nil {
		status, msg := deploymentNameStatus(h.logger, err)
		return status, errors.New(msg)
	}
	if err := checkVersionNotStale(deployPath, envVersion, req.EnvVersion); err != nil {
		return http.StatusConflict, err
	}

	// An environment that cannot be read is not an empty one: restarting the
	// replica without its database URL would look like the tenant's own bug.
	env, err := h.service.decodeEnvironment(req.Namespace, req.DeploymentID, req.Name, req.Environment)
	if err != nil {
		h.logger.Error("Failed to read the replica's environment", zap.Error(err))
		return http.StatusBadRequest, errors.New("failed to read the deployment environment")
	}

	deployment := &deployments.Deployment{
		ID:              req.DeploymentID,
		Namespace:       req.Namespace,
		Name:            req.Name,
		Type:            binding.Type,
		Port:            binding.Port,
		HomeNodeID:      h.service.nodePeerID,
		Environment:     env,
		MemoryLimitMB:   binding.MemoryLimitMB,
		CPULimitPercent: binding.CPULimitPercent,
	}
	// Reconfigure re-mints the workload token. Every gateway has the minter
	// (gateway.go); one that could not mint answers an error here, which the
	// home node reports instead of leaving the replica on a stale credential.
	if err := h.reconfigurer.Reconfigure(ctx, deployment, deployPath); err != nil {
		h.logger.Error("Failed to reconfigure the replica", zap.Error(err))
		return http.StatusInternalServerError, fmt.Errorf("failed to restart the replica: %w", err)
	}
	if err := recordAppliedVersion(deployPath, envVersion, req.EnvVersion); err != nil {
		h.logger.Error("Applied the environment but could not record its version", zap.Error(err))
		return http.StatusInternalServerError, err
	}
	return http.StatusOK, nil
}
