package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/sandbox"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// nodeReportCommand is what --ssh runs on every node.
const nodeReportCommand = "sudo orama node report --json"

// maxSSHOutputChars bounds how much of a node's output an error quotes.
const maxSSHOutputChars = 200

// CollectorConfig holds configuration for the collection pipeline.
type CollectorConfig struct {
	ConfigPath string
	Env        string
	Timeout    time.Duration
}

// CollectOnce runs `sudo orama node report --json` on all matching nodes
// in parallel and returns a ClusterSnapshot.
func CollectOnce(ctx context.Context, cfg CollectorConfig) (*cluster.ClusterSnapshot, error) {
	nodes, cleanup, err := loadNodes(cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultSSHTimeout
	}

	start := time.Now()
	snap := &cluster.ClusterSnapshot{
		Environment: cfg.Env,
		CollectedAt: start,
		Nodes:       make([]cluster.CollectionStatus, len(nodes)),
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

	snap.DurationMS = time.Since(start).Milliseconds()
	snap.Alerts = cluster.DeriveAlerts(snap)

	return snap, nil
}

// collectNodeReport SSHes into a single node and parses the JSON report.
func collectNodeReport(ctx context.Context, node inspector.Node, timeout time.Duration) cluster.CollectionStatus {
	nodeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	result := inspector.RunSSH(nodeCtx, node, nodeReportCommand)

	cs := cluster.CollectionStatus{
		Node:       cluster.NodeRef{Host: node.Host, Role: node.Role},
		DurationMS: time.Since(start).Milliseconds(),
		Retries:    result.Retries,
	}

	if !result.OK() {
		cs.Err = fmt.Sprintf("SSH failed (exit %d): %s", result.ExitCode, truncate(result.Stderr, maxSSHOutputChars))
		return cs
	}

	cs = withReport(cs, node.Host, result.Stdout)
	if cs.Report != nil {
		// The node stamped its report as it began collecting, which ended
		// just before its output came back: that moment on this clock is the
		// return time less the collection time.
		began := time.Now().Add(-time.Duration(cs.Report.CollectMS) * time.Millisecond)
		cs.ClockOffsetMS, cs.ClockMeasured = cs.Report.Timestamp.Sub(began).Milliseconds(), true
	}
	return cs
}

// withReport parses a node's `orama node report --json` output into cs. The
// node's public address is the one it was reached at; its overlay address is
// the one it reports, so --node can name it either way.
func withReport(cs cluster.CollectionStatus, host, stdout string) cluster.CollectionStatus {
	var rpt report.NodeReport
	if err := json.Unmarshal([]byte(stdout), &rpt); err != nil {
		cs.Err = fmt.Sprintf("parse report JSON: %v (first %d bytes: %s)", err, maxSSHOutputChars, truncate(stdout, maxSSHOutputChars))
		return cs
	}
	if rpt.Hostname == "" {
		rpt.Hostname = host
	}
	rpt.PublicIP = host
	cs.Node.WGIP = rpt.WGIP
	cs.Report = &rpt
	return cs
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
			return nil, noop, clierr.Wrap(clierr.CodeUsage, fmt.Errorf("load nodes from --config %s: %w", cfg.ConfigPath, err))
		}
		nodes = inspector.FilterByEnv(nodes, cfg.Env)
	}
	if len(nodes) == 0 {
		return nil, noop, clierr.NotFound("no nodes found for env %q", cfg.Env)
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
		return nil, noop, clierr.Wrap(clierr.CodeUsage, fmt.Errorf("load sandbox config: %w", err))
	}

	state, err := sandbox.FindActiveSandbox()
	if err != nil {
		return nil, noop, clierr.Wrap(clierr.CodeUsage, fmt.Errorf("find active sandbox: %w", err))
	}
	if state == nil {
		return nil, noop, clierr.NotFound("no active sandbox found (start one with `orama sandbox create`)")
	}

	nodes := state.ToNodes(sbxCfg.SSHKey.VaultTarget)
	if len(nodes) == 0 {
		return nil, noop, clierr.NotFound("no nodes found for sandbox %q", state.Name)
	}

	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return nil, noop, fmt.Errorf("prepare SSH keys: %w", err)
	}

	return nodes, cleanup, nil
}
