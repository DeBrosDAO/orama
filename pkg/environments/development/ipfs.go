package development

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// ipfsNodeInfo holds information about an IPFS node for peer discovery
type ipfsNodeInfo struct {
	name        string
	ipfsPath    string
	apiPort     int
	swarmPort   int
	gatewayPort int
	peerID      string
}

func (pm *ProcessManager) buildIPFSNodes(topology *Topology) []ipfsNodeInfo {
	var nodes []ipfsNodeInfo
	for _, nodeSpec := range topology.Nodes {
		nodes = append(nodes, ipfsNodeInfo{
			name:        nodeSpec.Name,
			ipfsPath:    filepath.Join(pm.oramaDir, nodeSpec.DataDir, "ipfs/repo"),
			apiPort:     nodeSpec.IPFSAPIPort,
			swarmPort:   nodeSpec.IPFSSwarmPort,
			gatewayPort: nodeSpec.IPFSGatewayPort,
			peerID:      "",
		})
	}
	return nodes
}

func (pm *ProcessManager) startIPFS(ctx context.Context) error {
	topology := DefaultTopology()
	nodes := pm.buildIPFSNodes(topology)

	for i := range nodes {
		os.MkdirAll(nodes[i].ipfsPath, 0755)

		if _, err := os.Stat(filepath.Join(nodes[i].ipfsPath, "config")); os.IsNotExist(err) {
			fmt.Fprintf(pm.logWriter, "  Initializing IPFS (%s)...\n", nodes[i].name)
			cmd := exec.CommandContext(ctx, "ipfs", "init", "--profile=server", "--repo-dir="+nodes[i].ipfsPath)
			if _, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintf(pm.logWriter, "    Warning: ipfs init failed: %v\n", err)
			}

			swarmKeyPath := filepath.Join(pm.oramaDir, "swarm.key")
			if data, err := os.ReadFile(swarmKeyPath); err == nil {
				os.WriteFile(filepath.Join(nodes[i].ipfsPath, "swarm.key"), data, 0600)
			}
		}

		peerID, err := configureIPFSRepo(nodes[i].ipfsPath, nodes[i].apiPort, nodes[i].gatewayPort, nodes[i].swarmPort)
		if err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to configure IPFS repo for %s: %v\n", nodes[i].name, err)
		} else {
			nodes[i].peerID = peerID
			fmt.Fprintf(pm.logWriter, "    Peer ID for %s: %s\n", nodes[i].name, peerID)
		}
	}

	for i := range nodes {
		pidPath := filepath.Join(pm.pidsDir, fmt.Sprintf("ipfs-%s.pid", nodes[i].name))
		logPath := filepath.Join(pm.oramaDir, "logs", fmt.Sprintf("ipfs-%s.log", nodes[i].name))

		cmd := exec.CommandContext(ctx, "ipfs", "daemon", "--enable-pubsub-experiment", "--repo-dir="+nodes[i].ipfsPath)
		logFile, _ := os.Create(logPath)
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start ipfs-%s: %w", nodes[i].name, err)
		}

		os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
		pm.processes[fmt.Sprintf("ipfs-%s", nodes[i].name)] = &ManagedProcess{
			Name:      fmt.Sprintf("ipfs-%s", nodes[i].name),
			PID:       cmd.Process.Pid,
			StartTime: time.Now(),
			LogPath:   logPath,
		}

		fmt.Fprintf(pm.logWriter, "✓ IPFS (%s) started (PID: %d, API: %d, Swarm: %d)\n", nodes[i].name, cmd.Process.Pid, nodes[i].apiPort, nodes[i].swarmPort)
	}

	time.Sleep(2 * time.Second)

	if err := pm.seedIPFSPeersWithHTTP(ctx, nodes); err != nil {
		fmt.Fprintf(pm.logWriter, "⚠️  Failed to seed IPFS peers: %v\n", err)
	}

	return nil
}

func configureIPFSRepo(repoPath string, apiPort, gatewayPort, swarmPort int) (string, error) {
	configPath := filepath.Join(repoPath, "config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to read IPFS config: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("failed to parse IPFS config: %w", err)
	}

	config["Addresses"] = map[string]interface{}{
		"API":     []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", apiPort)},
		"Gateway": []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", gatewayPort)},
		"Swarm": []string{
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", swarmPort),
			fmt.Sprintf("/ip6/::/tcp/%d", swarmPort),
		},
	}

	config["AutoConf"] = map[string]interface{}{
		"Enabled": false,
	}
	config["Bootstrap"] = []string{}

	if dns, ok := config["DNS"].(map[string]interface{}); ok {
		dns["Resolvers"] = map[string]interface{}{}
	} else {
		config["DNS"] = map[string]interface{}{
			"Resolvers": map[string]interface{}{},
		}
	}

	if routing, ok := config["Routing"].(map[string]interface{}); ok {
		routing["DelegatedRouters"] = []string{}
	} else {
		config["Routing"] = map[string]interface{}{
			"DelegatedRouters": []string{},
		}
	}

	if ipns, ok := config["Ipns"].(map[string]interface{}); ok {
		ipns["DelegatedPublishers"] = []string{}
	} else {
		config["Ipns"] = map[string]interface{}{
			"DelegatedPublishers": []string{},
		}
	}

	if api, ok := config["API"].(map[string]interface{}); ok {
		api["HTTPHeaders"] = map[string][]string{
			"Access-Control-Allow-Origin":   {"*"},
			"Access-Control-Allow-Methods":  {"GET", "PUT", "POST", "DELETE", "OPTIONS"},
			"Access-Control-Allow-Headers":  {"Content-Type", "X-Requested-With"},
			"Access-Control-Expose-Headers": {"Content-Length", "Content-Range"},
		}
	} else {
		config["API"] = map[string]interface{}{
			"HTTPHeaders": map[string][]string{
				"Access-Control-Allow-Origin":   {"*"},
				"Access-Control-Allow-Methods":  {"GET", "PUT", "POST", "DELETE", "OPTIONS"},
				"Access-Control-Allow-Headers":  {"Content-Type", "X-Requested-With"},
				"Access-Control-Expose-Headers": {"Content-Length", "Content-Range"},
			},
		}
	}

	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal IPFS config: %w", err)
	}

	if err := os.WriteFile(configPath, updatedData, 0644); err != nil {
		return "", fmt.Errorf("failed to write IPFS config: %w", err)
	}

	if id, ok := config["Identity"].(map[string]interface{}); ok {
		if peerID, ok := id["PeerID"].(string); ok {
			return peerID, nil
		}
	}

	return "", fmt.Errorf("could not extract peer ID from config")
}

func (pm *ProcessManager) seedIPFSPeersWithHTTP(ctx context.Context, nodes []ipfsNodeInfo) error {
	fmt.Fprintf(pm.logWriter, "  Seeding IPFS local bootstrap peers via HTTP API...\n")

	for _, node := range nodes {
		if err := pm.waitIPFSReady(ctx, node); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to wait for IPFS readiness for %s: %v\n", node.name, err)
		}
	}

	for i, node := range nodes {
		httpURL := fmt.Sprintf("http://127.0.0.1:%d/api/v0/bootstrap/rm?all=true", node.apiPort)
		if err := pm.ipfsHTTPCall(ctx, httpURL, "POST"); err != nil {
			fmt.Fprintf(pm.logWriter, "    Warning: failed to clear bootstrap for %s: %v\n", node.name, err)
		}

		for j, otherNode := range nodes {
			if i == j {
				continue
			}

			multiaddr := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", otherNode.swarmPort, otherNode.peerID)
			httpURL := fmt.Sprintf("http://127.0.0.1:%d/api/v0/bootstrap/add?arg=%s", node.apiPort, url.QueryEscape(multiaddr))
			if err := pm.ipfsHTTPCall(ctx, httpURL, "POST"); err != nil {
				fmt.Fprintf(pm.logWriter, "    Warning: failed to add bootstrap peer for %s: %v\n", node.name, err)
			}
		}
	}

	return nil
}

func (pm *ProcessManager) waitIPFSReady(ctx context.Context, node ipfsNodeInfo) error {
	maxRetries := 30
	retryInterval := 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		httpURL := fmt.Sprintf("http://127.0.0.1:%d/api/v0/version", node.apiPort)
		if err := pm.ipfsHTTPCall(ctx, httpURL, "POST"); err == nil {
			return nil
		}

		select {
		case <-time.After(retryInterval):
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("IPFS daemon %s did not become ready", node.name)
}

func (pm *ProcessManager) ipfsHTTPCall(ctx context.Context, urlStr string, method string) error {
	client := tlsutil.NewHTTPClient(5 * time.Second)
	req, err := http.NewRequestWithContext(ctx, method, urlStr, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return nil
}

func readIPFSConfigValue(ctx context.Context, repoPath string, key string) (string, error) {
	configPath := filepath.Join(repoPath, "config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to read IPFS config: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, key) {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				value := strings.TrimSpace(parts[1])
				value = strings.Trim(value, `",`)
				if value != "" {
					return value, nil
				}
			}
		}
	}

	return "", fmt.Errorf("key %s not found in IPFS config", key)
}

