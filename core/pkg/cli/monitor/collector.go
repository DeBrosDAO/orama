package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/cli/production/report"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/cli/sandbox"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// CollectorConfig holds configuration for the collection pipeline.
type CollectorConfig struct {
	ConfigPath string
	Env        string
	NodeFilter string
	Timeout    time.Duration
}

// CollectOnce runs `sudo orama node report --json` on all matching nodes
// in parallel and returns a ClusterSnapshot.
func CollectOnce(ctx context.Context, cfg CollectorConfig) (*ClusterSnapshot, error) {
	nodes, cleanup, err := loadNodes(cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	start := time.Now()
	snap := &ClusterSnapshot{
		Environment: cfg.Env,
		CollectedAt: start,
		Nodes:       make([]CollectionStatus, len(nodes)),
	}

	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Add(1)
		go func(idx int, n inspector.Node) {
			defer wg.Done()
			snap.Nodes[idx] = collectNodeReport(ctx, n, timeout)
		}(i, node)
	}
	wg.Wait()

	snap.Duration = time.Since(start)
	snap.Alerts = DeriveAlerts(snap)

	return snap, nil
}

// collectNodeReport SSHes into a single node and parses the JSON report.
func collectNodeReport(ctx context.Context, node inspector.Node, timeout time.Duration) CollectionStatus {
	nodeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	result := inspector.RunSSH(nodeCtx, node, "sudo orama node report --json")

	cs := CollectionStatus{
		Node:     node,
		Duration: time.Since(start),
		Retries:  result.Retries,
	}

	if !result.OK() {
		cs.Error = fmt.Errorf("SSH failed (exit %d): %s", result.ExitCode, truncate(result.Stderr, 200))
		return cs
	}

	var rpt report.NodeReport
	if err := json.Unmarshal([]byte(result.Stdout), &rpt); err != nil {
		cs.Error = fmt.Errorf("parse report JSON: %w (first 200 bytes: %s)", err, truncate(result.Stdout, 200))
		return cs
	}

	// Enrich with node metadata from nodes.conf
	if rpt.Hostname == "" {
		rpt.Hostname = node.Host
	}
	rpt.PublicIP = node.Host

	cs.Report = &rpt
	return cs
}

func filterByHost(nodes []inspector.Node, host string) []inspector.Node {
	var filtered []inspector.Node
	for _, n := range nodes {
		if n.Host == host {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// loadNodes resolves the node list and SSH keys based on the environment.
// For "sandbox", nodes are loaded from the active sandbox state file with
// the sandbox SSH key already set. For other environments, nodes come from
// nodes.conf and use wallet-derived SSH keys.
func loadNodes(cfg CollectorConfig) ([]inspector.Node, func(), error) {
	noop := func() {}

	if cfg.Env == "sandbox" {
		return loadSandboxNodes(cfg)
	}

	// With no explicit --config, nodes come from the same resolver every other
	// command uses. Reading a path relative to the working directory meant an
	// installed binary only worked from inside the source tree.
	var nodes []inspector.Node
	var err error
	if cfg.ConfigPath == "" {
		nodes, err = noderesolver.ResolveNodes(cfg.Env)
		if err != nil {
			return nil, noop, fmt.Errorf("resolve nodes for %q: %w", cfg.Env, err)
		}
	} else {
		nodes, err = inspector.LoadNodes(cfg.ConfigPath)
		if err != nil {
			return nil, noop, fmt.Errorf("load nodes: %w", err)
		}
		nodes = inspector.FilterByEnv(nodes, cfg.Env)
	}
	if cfg.NodeFilter != "" {
		nodes = filterByHost(nodes, cfg.NodeFilter)
	}
	if len(nodes) == 0 {
		return nil, noop, fmt.Errorf("no nodes found for env %q", cfg.Env)
	}

	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return nil, noop, fmt.Errorf("prepare SSH keys: %w", err)
	}
	return nodes, cleanup, nil
}

// loadSandboxNodes loads nodes from the active sandbox state file.
func loadSandboxNodes(cfg CollectorConfig) ([]inspector.Node, func(), error) {
	noop := func() {}

	sbxCfg, err := sandbox.LoadConfig()
	if err != nil {
		return nil, noop, fmt.Errorf("load sandbox config: %w", err)
	}

	state, err := sandbox.FindActiveSandbox()
	if err != nil {
		return nil, noop, fmt.Errorf("find active sandbox: %w", err)
	}
	if state == nil {
		return nil, noop, fmt.Errorf("no active sandbox found")
	}

	nodes := state.ToNodes(sbxCfg.SSHKey.VaultTarget)
	if cfg.NodeFilter != "" {
		nodes = filterByHost(nodes, cfg.NodeFilter)
	}
	if len(nodes) == 0 {
		return nil, noop, fmt.Errorf("no nodes found for sandbox %q", state.Name)
	}

	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return nil, noop, fmt.Errorf("prepare SSH keys: %w", err)
	}

	return nodes, cleanup, nil
}
