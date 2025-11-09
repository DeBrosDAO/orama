package development

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// HealthCheckResult represents the result of a health check
type HealthCheckResult struct {
	Name    string
	Healthy bool
	Details string
}

// IPFSHealthCheck verifies IPFS peer connectivity
func (pm *ProcessManager) IPFSHealthCheck(ctx context.Context, nodes []ipfsNodeInfo) HealthCheckResult {
	result := HealthCheckResult{Name: "IPFS Peers"}

	healthyCount := 0
	for _, node := range nodes {
		cmd := exec.CommandContext(ctx, "ipfs", "swarm", "peers", "--repo-dir="+node.ipfsPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			result.Details += fmt.Sprintf("%s: error getting peers (%v); ", node.name, err)
			continue
		}

		// Split by newlines and filter empty lines
		peerLines := strings.Split(strings.TrimSpace(string(output)), "\n")
		peerCount := 0
		for _, line := range peerLines {
			if strings.TrimSpace(line) != "" {
				peerCount++
			}
		}

		if peerCount < 2 {
			result.Details += fmt.Sprintf("%s: only %d peers (want 2+); ", node.name, peerCount)
		} else {
			result.Details += fmt.Sprintf("%s: %d peers; ", node.name, peerCount)
			healthyCount++
		}
	}

	result.Healthy = healthyCount == len(nodes)
	return result
}

// RQLiteHealthCheck verifies RQLite cluster formation
func (pm *ProcessManager) RQLiteHealthCheck(ctx context.Context) HealthCheckResult {
	result := HealthCheckResult{Name: "RQLite Cluster"}

	// Check bootstrap node
	bootstrapStatus := pm.checkRQLiteNode(ctx, "bootstrap", 5001)
	if !bootstrapStatus.Healthy {
		result.Details += fmt.Sprintf("bootstrap: %s; ", bootstrapStatus.Details)
		return result
	}

	// Check node2 and node3
	node2Status := pm.checkRQLiteNode(ctx, "node2", 5002)
	node3Status := pm.checkRQLiteNode(ctx, "node3", 5003)

	if node2Status.Healthy && node3Status.Healthy {
		result.Healthy = true
		result.Details = fmt.Sprintf("bootstrap: leader ok; node2: %s; node3: %s", node2Status.Details, node3Status.Details)
	} else {
		result.Details = fmt.Sprintf("bootstrap: ok; node2: %s; node3: %s", node2Status.Details, node3Status.Details)
	}

	return result
}

// checkRQLiteNode queries a single RQLite node's status
func (pm *ProcessManager) checkRQLiteNode(ctx context.Context, name string, httpPort int) HealthCheckResult {
	result := HealthCheckResult{Name: fmt.Sprintf("RQLite-%s", name)}

	urlStr := fmt.Sprintf("http://localhost:%d/status", httpPort)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(urlStr)
	if err != nil {
		result.Details = fmt.Sprintf("connection failed: %v", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		result.Details = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return result
	}

	var status map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		result.Details = fmt.Sprintf("decode error: %v", err)
		return result
	}

	// Check the store.raft structure (RQLite 8 format)
	store, ok := status["store"].(map[string]interface{})
	if !ok {
		result.Details = "store data not found"
		return result
	}

	raft, ok := store["raft"].(map[string]interface{})
	if !ok {
		result.Details = "raft data not found"
		return result
	}

	// Check if we have a leader
	leader, hasLeader := raft["leader"].(string)
	if hasLeader && leader != "" {
		result.Healthy = true
		result.Details = "cluster member with leader elected"
		return result
	}

	// Check node state - accept both Leader and Follower
	if state, ok := raft["state"].(string); ok {
		if state == "Leader" {
			result.Healthy = true
			result.Details = "this node is leader"
			return result
		}
		if state == "Follower" {
			result.Healthy = true
			result.Details = "this node is follower in cluster"
			return result
		}
		result.Details = fmt.Sprintf("state: %s", state)
		return result
	}

	result.Details = "not yet connected"
	return result
}

// LibP2PHealthCheck verifies that network nodes have peer connections
func (pm *ProcessManager) LibP2PHealthCheck(ctx context.Context) HealthCheckResult {
	result := HealthCheckResult{Name: "LibP2P/Node Peers"}

	// Check that at least 2 nodes are part of the RQLite cluster (implies peer connectivity)
	// and that they can communicate via LibP2P (which they use for cluster discovery)
	healthyNodes := 0
	for i, name := range []string{"bootstrap", "node2", "node3"} {
		httpPort := 5001 + i
		status := pm.checkRQLiteNode(ctx, name, httpPort)
		if status.Healthy {
			healthyNodes++
			result.Details += fmt.Sprintf("%s: connected; ", name)
		} else {
			result.Details += fmt.Sprintf("%s: %s; ", name, status.Details)
		}
	}

	// Healthy if at least 2 nodes report connectivity (including bootstrap)
	result.Healthy = healthyNodes >= 2
	return result
}

// HealthCheckWithRetry performs a health check with retry logic
func (pm *ProcessManager) HealthCheckWithRetry(ctx context.Context, nodes []ipfsNodeInfo, retries int, retryInterval time.Duration, timeout time.Duration) bool {
	fmt.Fprintf(pm.logWriter, "\n⚕️  Validating cluster health...\n")

	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for attempt := 1; attempt <= retries; attempt++ {
		// Perform all checks
		ipfsResult := pm.IPFSHealthCheck(deadlineCtx, nodes)
		rqliteResult := pm.RQLiteHealthCheck(deadlineCtx)
		libp2pResult := pm.LibP2PHealthCheck(deadlineCtx)

		// Log results
		if attempt == 1 || attempt == retries || (attempt%3 == 0) {
			fmt.Fprintf(pm.logWriter, "  Attempt %d/%d:\n", attempt, retries)
			pm.logHealthCheckResult(pm.logWriter, "    ", ipfsResult)
			pm.logHealthCheckResult(pm.logWriter, "    ", rqliteResult)
			pm.logHealthCheckResult(pm.logWriter, "    ", libp2pResult)
		}

		// All checks must pass
		if ipfsResult.Healthy && rqliteResult.Healthy && libp2pResult.Healthy {
			fmt.Fprintf(pm.logWriter, "\n✓ All health checks passed!\n")
			return true
		}

		if attempt < retries {
			select {
			case <-time.After(retryInterval):
				continue
			case <-deadlineCtx.Done():
				fmt.Fprintf(pm.logWriter, "\n❌ Health check timeout reached\n")
				return false
			}
		}
	}

	fmt.Fprintf(pm.logWriter, "\n❌ Health checks failed after %d attempts\n", retries)
	return false
}

// logHealthCheckResult logs a single health check result
func (pm *ProcessManager) logHealthCheckResult(w io.Writer, indent string, result HealthCheckResult) {
	status := "❌"
	if result.Healthy {
		status = "✓"
	}
	fmt.Fprintf(w, "%s%s %s: %s\n", indent, status, result.Name, result.Details)
}
