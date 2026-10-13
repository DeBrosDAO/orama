package deployments

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"go.uber.org/zap"
)

const (
	// replicaUpdateFailedEvent is the deployment event a replica that did not
	// apply an update or rollback is recorded under.
	replicaUpdateFailedEvent = "replica_update_failed"
	replicaUpdatePath        = "/v1/internal/deployments/replica/update"
	replicaRollbackPath      = "/v1/internal/deployments/replica/rollback"
)

// replicaFanOut is one change the home node makes every other replica apply.
type replicaFanOut struct {
	what        string // "environment change", "update": names the change in errors
	path        string
	payload     map[string]interface{}
	timeout     time.Duration
	failedEvent string
}

// replicaFanOutError is a change that did not reach every replica. The home
// node already has the change; the nodes listed still run the old state until
// the same command is run again. Its text is for the tenant: each node is named
// with a generic reason, and the detail is in the gateway's log.
type replicaFanOutError struct {
	what     string
	applied  int
	failures map[string]error
}

func (e *replicaFanOutError) Error() string {
	nodes := make([]string, 0, len(e.failures))
	for node := range e.failures {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		parts = append(parts, fmt.Sprintf("node %s %s", node, publicReplicaReason(e.failures[node])))
	}
	return fmt.Sprintf("the %s reached %d of %d replicas; not applied: %s",
		e.what, e.applied, e.applied+len(e.failures), strings.Join(parts, "; "))
}

// publicReplicaReason says what kind of failure a replica call was, in words a
// tenant may read. The peer's own text and the addresses in transport errors
// (overlay IPs, ports) name the cluster's internals; they go to the log
// (callOneReplica) and not to the response or the deployment's events.
func publicReplicaReason(err error) string {
	var (
		refused *replicaStatusError
		netErr  net.Error
	)
	switch {
	case errors.As(err, &refused):
		return fmt.Sprintf("refused the change (status %d)", refused.status)
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return "did not answer in time"
	default:
		return "could not be reached"
	}
}

// callReplicasAndWait makes every other active replica of the deployment apply
// the change, in parallel, and returns when each has answered or timed out. It
// returns how many applied it.
//
// This is not best-effort. A change that reached the home node and not a
// replica leaves two versions of the app answering one hostname, so a replica
// that cannot be reached or refuses is reported to the caller and recorded as a
// deployment event.
func (s *DeploymentService) callReplicasAndWait(ctx context.Context, deployment *deployments.Deployment, f replicaFanOut) (int, error) {
	if s.replicaManager == nil {
		return 0, nil
	}
	nodes, err := s.replicaManager.GetActiveReplicaNodes(ctx, deployment.ID)
	if err != nil {
		return 0, fmt.Errorf("failed to list the replicas of %s: %w", deployment.Name, err)
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		failures = make(map[string]error)
		applied  int
	)
	for _, nodeID := range nodes {
		if nodeID == s.nodePeerID {
			continue
		}
		wg.Add(1)
		go func(nodeID string) {
			defer wg.Done()
			err := s.callOneReplica(ctx, nodeID, f)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures[nodeID] = err
				return
			}
			applied++
		}(nodeID)
	}
	wg.Wait()

	if len(failures) == 0 {
		return applied, nil
	}
	failure := &replicaFanOutError{what: f.what, applied: applied, failures: failures}
	s.recordReplicaFailure(ctx, deployment, f.failedEvent, failure)
	return applied, failure
}

// callOneReplica asks one replica node to apply the change.
func (s *DeploymentService) callOneReplica(ctx context.Context, nodeID string, f replicaFanOut) error {
	nodeIP, err := s.replicaManager.GetNodeOverlayIP(ctx, nodeID)
	if err != nil {
		s.logger.Error("Replica call failed: no overlay IP for the node",
			zap.String("node_id", nodeID), zap.String("path", f.path), zap.Error(err))
		return fmt.Errorf("failed to get the node's overlay IP: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	call := s.callReplica
	if call == nil {
		call = func(ctx context.Context, nodeID, nodeIP, path string, payload map[string]interface{}) (map[string]interface{}, error) {
			return s.callInternalAPIWithin(ctx, nodeID, nodeIP, path, payload, f.timeout)
		}
	}
	if _, err := call(callCtx, nodeID, nodeIP, f.path, f.payload); err != nil {
		s.logger.Error("Replica call failed",
			zap.String("node_id", nodeID), zap.String("path", f.path), zap.Error(err))
		return err
	}
	return nil
}

// recordReplicaFailure adds the failure to the deployment's events, so it is
// visible after the request that caused it is gone. The events are readable by
// the tenant, so it takes the failure's tenant-safe text.
func (s *DeploymentService) recordReplicaFailure(ctx context.Context, deployment *deployments.Deployment, eventType string, cause error) {
	_, err := s.db.Exec(context.WithoutCancel(ctx),
		`INSERT INTO deployment_events (deployment_id, event_type, message, created_at) VALUES (?, ?, ?, ?)`,
		deployment.ID, eventType, cause.Error(), time.Now())
	if err != nil {
		s.logger.Error("Failed to record the replica failure event",
			zap.String("deployment_id", deployment.ID), zap.String("event", eventType), zap.Error(err))
	}
}

// UpdateReplicas makes every other replica apply an update or rollback to
// deployment's current version, and waits for them. path is replicaUpdatePath
// or replicaRollbackPath.
func (s *DeploymentService) UpdateReplicas(ctx context.Context, deployment *deployments.Deployment, path string) (int, error) {
	return s.callReplicasAndWait(ctx, deployment, replicaFanOut{
		what: "update",
		path: path,
		payload: map[string]interface{}{
			"deployment_id": deployment.ID,
			"namespace":     deployment.Namespace,
			"name":          deployment.Name,
			"type":          deployment.Type,
			"content_cid":   deployment.ContentCID,
			"build_cid":     deployment.BuildCID,
			"new_version":   deployment.Version,
		},
		timeout:     replicaCallTimeout,
		failedEvent: replicaUpdateFailedEvent,
	})
}

// changeResponseBudget is how long the home node's response to an update or
// rollback may take from the moment the request arrives: the whole handler
// budget with room to answer. It is longer than the server's write deadline,
// so the handler moves its deadlines before it does anything else, and shorter
// than the gateway hop in front (constants/timeouts.go).
const changeResponseBudget = constants.DeploymentUpdateBudget + 20*time.Second

// extendForChange gives an update or rollback the time its work needs. It is
// called first, before the request is read or the lock is waited for: a
// deadline moved only once the replicas are being waited on is one the upload
// and the lock wait have already run into.
func extendForChange(w http.ResponseWriter) error {
	if err := httputil.ExtendIO(w, changeResponseBudget); err != nil {
		return fmt.Errorf("the request could not be given time to finish: %w", err)
	}
	return nil
}

// localChangeContext bounds the lock wait and the work on the home node. The
// replicas are waited for under their own segment (updateReplicas), so a slow
// local step cannot leave them without time.
func localChangeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, constants.DeploymentUpdateLocalBudget)
}

// updateReplicas is UpdateReplicas for a handler that must still be able to
// answer when the replicas have taken their whole segment: it runs detached
// from the request, so a client that hangs up does not abandon the fan-out half
// way, and bounded by DeploymentUpdateReplicaBudget.
func (s *DeploymentService) updateReplicas(ctx context.Context, deployment *deployments.Deployment, path string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), constants.DeploymentUpdateReplicaBudget)
	defer cancel()
	_, err := s.UpdateReplicas(ctx, deployment, path)
	return err
}
