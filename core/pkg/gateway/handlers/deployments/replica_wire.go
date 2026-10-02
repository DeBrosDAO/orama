package deployments

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"go.uber.org/zap"
)

const (
	// replicaContentWait bounds how long a replica waits for its artifact to
	// become retrievable on this node. The home node calls the replica right
	// after pinning, before the cluster has placed the blocks here, so the
	// first fetch can fail with the content not yet reachable. It stays below
	// replicaCallTimeout so the caller gets this node's answer, not a timeout.
	replicaContentWait = 60 * time.Second
	// replicaContentPollInterval is the pause between fetch attempts.
	replicaContentPollInterval = 2 * time.Second
	// replicaCallTimeout bounds one internal replica call: the content wait
	// plus the replica's own health wait.
	replicaCallTimeout = constants.DeploymentReplicaCallTimeout
	// maxReplicaResponseBytes bounds how much of a peer's reply is read.
	maxReplicaResponseBytes = 1 << 20
	// replicaSetupFailedEvent is the deployment event a failed replica setup
	// is recorded under.
	replicaSetupFailedEvent = "replica_setup_failed"
	// maxReplicaErrorText bounds the peer's error text carried into an error.
	maxReplicaErrorText = 512
)

// replicaErrorBody is the JSON body every internal replica route answers an
// error with.
type replicaErrorBody struct {
	Error string `json:"error"`
}

// writeReplicaError answers an internal replica call with a machine-readable
// error, so the calling node can surface the reason rather than parse prose.
func writeReplicaError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(replicaErrorBody{Error: msg})
}

// writeReplicaNameError is writeDeploymentNameError for the internal routes.
func writeReplicaNameError(w http.ResponseWriter, logger *zap.Logger, err error) {
	status, msg := deploymentNameStatus(logger, err)
	writeReplicaError(w, status, msg)
}

// remoteErrorText is the reason a peer gave for a failed internal call: the
// "error" field of its JSON body, or, when something in front of the handler
// (the gateway's auth, a proxy) answered in plain text, that text.
func remoteErrorText(body []byte) string {
	var parsed replicaErrorBody
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != "" {
		return parsed.Error
	}
	text := strings.TrimSpace(string(body))
	if len(text) > maxReplicaErrorText {
		text = text[:maxReplicaErrorText]
	}
	if text == "" {
		return "no error text"
	}
	return text
}

// awaitContent calls get until the content is retrievable, the failure is one
// that waiting cannot fix, or timeout passes. It polls the actual readiness of
// the content rather than sleeping a fixed time.
func awaitContent(ctx context.Context, get func(context.Context) (io.ReadCloser, error), timeout, interval time.Duration) (io.ReadCloser, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		reader, err := get(waitCtx)
		if err == nil {
			return reader, nil
		}
		if !ipfs.IsContentUnavailable(err) {
			return nil, err
		}
		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("content still not retrievable after %s: %w", timeout, err)
		case <-time.After(interval):
		}
	}
}

// replicaStatusError is a peer's refusal of an internal call. Its text is the
// peer's own, so it is for logs, not for the tenant: see publicReplicaReason.
type replicaStatusError struct {
	nodeID string
	status int
	text   string
}

func (e *replicaStatusError) Error() string {
	return fmt.Sprintf("node %s returned status %d: %s", e.nodeID, e.status, e.text)
}

// parseReplicaResponse turns a peer's reply to an internal call into its JSON
// result, or, for a refusal, an error carrying the peer's own reason.
func parseReplicaResponse(nodeID string, status int, body []byte) (map[string]interface{}, error) {
	if status != http.StatusOK && status != http.StatusCreated {
		return nil, &replicaStatusError{nodeID: nodeID, status: status, text: remoteErrorText(body)}
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode the response of node %s: %w", nodeID, err)
	}
	return result, nil
}
