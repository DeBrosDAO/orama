package namespace

import (
	"context"
	"fmt"
	"sync"
)

// webrtcTeardownTask is one WebRTC unit a disable retires: the SFU of a node, or
// the pre-#283 per-namespace TURN unit of a node.
type webrtcTeardownTask struct {
	NodeID      string
	NodeIP      string
	ServiceType string // "sfu" or "turn"
	Action      string // teardownSFUAction or teardownTURNAction
}

// webrtcTeardownResult is the outcome of one webrtcTeardownTask.
type webrtcTeardownResult struct {
	Task webrtcTeardownTask
	Err  error
}

// teardownWebRTCConcurrently runs every task at once and returns one result per
// task, in task order. The tasks share nothing but the namespace's per-node lock
// (which serialises two units of one node), and an SFU drains for up to 45s, so
// run one after another a disable took the SUM of every node's drain — 117s on
// three nodes in the stagenet e2e run, past the gateway's 120s WriteTimeout,
// which dropped the connection and surfaced as a bare 502 (see website/src/docs/operator/webrtc-operations.mdx).
// Every task is attempted; a failure never cancels the others.
func (cm *ClusterManager) teardownWebRTCConcurrently(ctx context.Context, namespace, clusterID string, tasks []webrtcTeardownTask) []webrtcTeardownResult {
	results := make([]webrtcTeardownResult, len(tasks))
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = webrtcTeardownResult{Task: task}
			if err := cm.teardownWebRTCOnNode(ctx, task.NodeID, task.NodeIP, namespace, clusterID, task.Action); err != nil {
				results[i].Err = fmt.Errorf("%s on node %s: %w", webrtcServiceLabel(task.ServiceType), task.NodeID, err)
			}
		}()
	}
	wg.Wait()
	return results
}

func webrtcServiceLabel(serviceType string) string {
	if serviceType == "turn" {
		return "TURN"
	}
	return "SFU"
}
