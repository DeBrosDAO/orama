package development

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (pm *ProcessManager) startIPFSCluster(ctx context.Context) error {
	topology := DefaultTopology()
	var nodes []struct {
		name        string
		clusterPath string
		restAPIPort int
		clusterPort int
		ipfsPort    int
	}

	for _, nodeSpec := range topology.Nodes {
		nodes = append(nodes, struct {
			name        string
			clusterPath string
			restAPIPort int
			clusterPort int
			ipfsPort    int
		}{
			nodeSpec.Name,
			filepath.Join(pm.oramaDir, nodeSpec.DataDir, "ipfs-cluster"),
			nodeSpec.ClusterAPIPort,
			nodeSpec.ClusterPort,
			nodeSpec.IPFSAPIPort,
		})
	}

	fmt.Fprintf(pm.logWriter, "  Waiting for IPFS daemons to be ready...\n")
	ipfsNodes := pm.buildIPFSNodes(topology)
	for _, ipfsNode := range ipfsNodes {
		if err := pm.waitIPFSReady(ctx, ipfsNode); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: IPFS %s did not become ready: %v\n", ipfsNode.name, err)
		}
	}

	secretPath := filepath.Join(pm.oramaDir, "cluster-secret")
	clusterSecret, err := os.ReadFile(secretPath)
	if err != nil {
		return fmt.Errorf("failed to read cluster secret: %w", err)
	}
	clusterSecretHex := strings.TrimSpace(string(clusterSecret))

	bootstrapMultiaddr := ""
	{
		node := nodes[0]
		if err := pm.cleanClusterState(node.clusterPath); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to clean cluster state for %s: %v\n", node.name, err)
		}

		os.MkdirAll(node.clusterPath, 0755)
		fmt.Fprintf(pm.logWriter, "  Initializing IPFS Cluster (%s)...\n", node.name)
		cmd := exec.CommandContext(ctx, "ipfs-cluster-service", "init", "--force")
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("IPFS_CLUSTER_PATH=%s", node.clusterPath),
			fmt.Sprintf("CLUSTER_SECRET=%s", clusterSecretHex),
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: ipfs-cluster-service init failed: %v (output: %s)\n", err, string(output))
		}

		if err := pm.ensureIPFSClusterPorts(node.clusterPath, node.restAPIPort, node.clusterPort); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to update IPFS Cluster config for %s: %v\n", node.name, err)
		}

		pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("ipfs-cluster-%s.pid", node.name))
		logPath := filepath.Join(pm.oramaDir, "logs", fmt.Sprintf("ipfs-cluster-%s.log", node.name))

		cmd = exec.CommandContext(ctx, "ipfs-cluster-service", "daemon")
		cmd.Env = append(os.Environ(), fmt.Sprintf("IPFS_CLUSTER_PATH=%s", node.clusterPath))
		logFile, _ := os.Create(logPath)
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		if err := cmd.Start(); err != nil {
			return err
		}

		os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
		fmt.Fprintf(pm.logWriter, "✓ IPFS Cluster (%s) started (PID: %d, API: %d)\n", node.name, cmd.Process.Pid, node.restAPIPort)

		if err := pm.waitClusterReady(ctx, node.name, node.restAPIPort); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: IPFS Cluster %s did not become ready: %v\n", node.name, err)
		}

		time.Sleep(500 * time.Millisecond)

		peerID, err := pm.waitForClusterPeerID(ctx, filepath.Join(node.clusterPath, "identity.json"))
		if err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to read bootstrap peer ID: %v\n", err)
		} else {
			bootstrapMultiaddr = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", node.clusterPort, peerID)
		}
	}

	for i := 1; i < len(nodes); i++ {
		node := nodes[i]
		if err := pm.cleanClusterState(node.clusterPath); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to clean cluster state for %s: %v\n", node.name, err)
		}

		os.MkdirAll(node.clusterPath, 0755)
		fmt.Fprintf(pm.logWriter, "  Initializing IPFS Cluster (%s)...\n", node.name)
		cmd := exec.CommandContext(ctx, "ipfs-cluster-service", "init", "--force")
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("IPFS_CLUSTER_PATH=%s", node.clusterPath),
			fmt.Sprintf("CLUSTER_SECRET=%s", clusterSecretHex),
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: ipfs-cluster-service init failed for %s: %v (output: %s)\n", node.name, err, string(output))
		}

		if err := pm.ensureIPFSClusterPorts(node.clusterPath, node.restAPIPort, node.clusterPort); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to update IPFS Cluster config for %s: %v\n", node.name, err)
		}

		pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("ipfs-cluster-%s.pid", node.name))
		logPath := filepath.Join(pm.oramaDir, "logs", fmt.Sprintf("ipfs-cluster-%s.log", node.name))

		args := []string{"daemon"}
		if bootstrapMultiaddr != "" {
			args = append(args, "--bootstrap", bootstrapMultiaddr)
		}

		cmd = exec.CommandContext(ctx, "ipfs-cluster-service", args...)
		cmd.Env = append(os.Environ(), fmt.Sprintf("IPFS_CLUSTER_PATH=%s", node.clusterPath))
		logFile, _ := os.Create(logPath)
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		if err := cmd.Start(); err != nil {
			continue
		}

		os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
		fmt.Fprintf(pm.logWriter, "✓ IPFS Cluster (%s) started (PID: %d, API: %d)\n", node.name, cmd.Process.Pid, node.restAPIPort)

		if err := pm.waitClusterReady(ctx, node.name, node.restAPIPort); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: IPFS Cluster %s did not become ready: %v\n", node.name, err)
		}
	}

	fmt.Fprintf(pm.logWriter, "  Waiting for IPFS Cluster peers to form...\n")
	if err := pm.waitClusterFormed(ctx, nodes[0].restAPIPort); err != nil {
		fmt.Fprintf(pm.logWriter, "    Warning: IPFS Cluster did not form fully: %v\n", err)
	}

	time.Sleep(1 * time.Second)
	return nil
}

func (pm *ProcessManager) waitForClusterPeerID(ctx context.Context, identityPath string) (string, error) {
	maxRetries := 30
	retryInterval := 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		data, err := os.ReadFile(identityPath)
		if err == nil {
			var identity map[string]interface{}
			if err := json.Unmarshal(data, &identity); err == nil {
				if id, ok := identity["id"].(string); ok {
					return id, nil
				}
			}
		}

		select {
		case <-time.After(retryInterval):
			continue
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	return "", fmt.Errorf("could not read cluster peer ID")
}

func (pm *ProcessManager) waitClusterReady(ctx context.Context, name string, restAPIPort int) error {
	maxRetries := 30
	retryInterval := 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		httpURL := fmt.Sprintf("http://127.0.0.1:%d/peers", restAPIPort)
		resp, err := http.Get(httpURL)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}

		select {
		case <-time.After(retryInterval):
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("IPFS Cluster %s did not become ready", name)
}

func (pm *ProcessManager) waitClusterFormed(ctx context.Context, bootstrapRestAPIPort int) error {
	maxRetries := 30
	retryInterval := 1 * time.Second
	requiredPeers := 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		httpURL := fmt.Sprintf("http://127.0.0.1:%d/peers", bootstrapRestAPIPort)
		resp, err := http.Get(httpURL)
		if err == nil && resp.StatusCode == 200 {
			dec := json.NewDecoder(resp.Body)
			peerCount := 0
			for {
				var peer interface{}
				if err := dec.Decode(&peer); err != nil {
					break
				}
				peerCount++
			}
			resp.Body.Close()
			if peerCount >= requiredPeers {
				return nil
			}
		}
		if resp != nil {
			resp.Body.Close()
		}

		select {
		case <-time.After(retryInterval):
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("IPFS Cluster did not form fully")
}

func (pm *ProcessManager) cleanClusterState(clusterPath string) error {
	pebblePath := filepath.Join(clusterPath, "pebble")
	os.RemoveAll(pebblePath)

	peerstorePath := filepath.Join(clusterPath, "peerstore")
	os.Remove(peerstorePath)

	serviceJSONPath := filepath.Join(clusterPath, "service.json")
	os.Remove(serviceJSONPath)

	lockPath := filepath.Join(clusterPath, "cluster.lock")
	os.Remove(lockPath)

	return nil
}

func (pm *ProcessManager) ensureIPFSClusterPorts(clusterPath string, restAPIPort int, clusterPort int) error {
	serviceJSONPath := filepath.Join(clusterPath, "service.json")
	data, err := os.ReadFile(serviceJSONPath)
	if err != nil {
		return err
	}

	var config map[string]interface{}
	json.Unmarshal(data, &config)

	portOffset := restAPIPort - 9094
	proxyPort := 9095 + portOffset
	pinsvcPort := 9097 + portOffset
	ipfsPort := 4501 + (portOffset / 10)

	if api, ok := config["api"].(map[string]interface{}); ok {
		if restapi, ok := api["restapi"].(map[string]interface{}); ok {
			restapi["http_listen_multiaddress"] = fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", restAPIPort)
		}
		if proxy, ok := api["ipfsproxy"].(map[string]interface{}); ok {
			proxy["listen_multiaddress"] = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", proxyPort)
			proxy["node_multiaddress"] = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort)
		}
		if pinsvc, ok := api["pinsvcapi"].(map[string]interface{}); ok {
			pinsvc["http_listen_multiaddress"] = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", pinsvcPort)
		}
	}

	if cluster, ok := config["cluster"].(map[string]interface{}); ok {
		cluster["listen_multiaddress"] = []string{
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", clusterPort),
			fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", clusterPort),
		}
	}

	if connector, ok := config["ipfs_connector"].(map[string]interface{}); ok {
		if ipfshttp, ok := connector["ipfshttp"].(map[string]interface{}); ok {
			ipfshttp["node_multiaddress"] = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort)
		}
	}

	updatedData, _ := json.MarshalIndent(config, "", "  ")
	return os.WriteFile(serviceJSONPath, updatedData, 0644)
}

