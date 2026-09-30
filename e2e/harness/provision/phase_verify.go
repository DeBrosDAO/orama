package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// wgSubnet is the WireGuard overlay every node joins.
	wgSubnet = "10.0.0.0/24"
	// wgAddrCommand prints a node's wg0 IPv4 address.
	wgAddrCommand = "ip -4 -o addr show dev wg0"
	// summaryOK is the monitor report's healthy value; noLeader its "none".
	summaryOK = "ok"
	noLeader  = "none"
	// healthReportName keeps the report that ended the health wait.
	healthReportName = "monitor-report-provision.json"
)

var wgInetPattern = regexp.MustCompile(`\binet (10\.[0-9]+\.[0-9]+\.[0-9]+)/`)

// healthReport is the part of `orama monitor report --json` provisioning
// reads (its contract: display/report.go, fields are only ever added).
type healthReport struct {
	Meta struct {
		NodeCount    int `json:"node_count"`
		HealthyCount int `json:"healthy_count"`
	} `json:"meta"`
	Summary struct {
		RQLiteLeader   string `json:"rqlite_leader"`
		RQLiteQuorum   string `json:"rqlite_quorum"`
		WGMeshStatus   string `json:"wg_mesh_status"`
		CriticalAlerts int    `json:"critical_alerts"`
	} `json:"summary"`
	Alerts []json.RawMessage `json:"alerts"`
}

// problem is why the report is not healthy for want nodes, or "".
func (h healthReport) problem(want int) string {
	s := h.Summary
	switch {
	case h.Meta.NodeCount != want || h.Meta.HealthyCount != want:
		return fmt.Sprintf("%d of %d nodes healthy (want %d)", h.Meta.HealthyCount, h.Meta.NodeCount, want)
	case s.RQLiteLeader == "" || s.RQLiteLeader == noLeader || s.RQLiteQuorum != summaryOK:
		return fmt.Sprintf("rqlite leader %q, quorum %q", s.RQLiteLeader, s.RQLiteQuorum)
	case s.WGMeshStatus != summaryOK:
		return "WireGuard mesh " + s.WGMeshStatus
	case s.CriticalAlerts > 0:
		return fmt.Sprintf("%d critical alerts, first %s", s.CriticalAlerts, firstAlert(h.Alerts))
	}
	return ""
}

func firstAlert(alerts []json.RawMessage) string {
	if len(alerts) == 0 {
		return "(none listed)"
	}
	return string(alerts[0])
}

// checkHealth signs in as the operator and waits for the monitor report to
// show every node healthy.
func (r *run) checkHealth(ctx context.Context) error {
	if _, err := r.oramaCmd(ctx, "auth", "login"); err != nil {
		return err
	}
	c := command{name: r.st.OramaBin, args: []string{"monitor", "report", "--env", r.st.Env, "--json"}, env: r.cliEnv(),
		log: filepath.Join(r.cfg.ArtifactDir, "provision-poll-monitor-report.log"), redact: r.red.Redact}
	out, err := waitReport(ctx, r.d.cmd, c, r.d.timing, func(h healthReport) string { return h.problem(len(r.st.Nodes)) })
	if err != nil {
		return err
	}
	return writeArtifact(r.cfg.ArtifactDir, healthReportName, out)
}

// waitReport runs the monitor report c every poll until judge finds no
// problem in it, returning that report, or fails after the health timeout
// naming the last problem. A report that is not JSON fails at once.
func waitReport(ctx context.Context, cmd commander, c command, tm timing, judge func(healthReport) string) (string, error) {
	wctx, cancel := context.WithTimeout(ctx, tm.health)
	defer cancel()
	ticker := time.NewTicker(tm.poll)
	defer ticker.Stop()
	last := "no report yet"
	for {
		out, err := cmd.Run(wctx, c)
		if err == nil {
			var h healthReport
			if jerr := json.Unmarshal([]byte(out), &h); jerr != nil {
				return "", fmt.Errorf("failed to parse `%s`: %w", c, jerr)
			}
			if last = judge(h); last == "" {
				return out, nil
			}
		} else {
			last = err.Error()
		}
		select {
		case <-wctx.Done():
			return "", fmt.Errorf("`%s` was not healthy within %s: %s", c, tm.health, last)
		case <-ticker.C:
		}
	}
}

// readWireGuardIPs records each node's overlay address.
func (r *run) readWireGuardIPs(ctx context.Context) error {
	_, subnet, err := net.ParseCIDR(wgSubnet)
	if err != nil {
		return fmt.Errorf("bad WireGuard subnet constant: %w", err)
	}
	for i := range r.st.Nodes {
		n := &r.st.Nodes[i]
		rctx, cancel := context.WithTimeout(ctx, r.d.timing.remoteAction)
		out, errOut, exit, err := r.d.remote.Run(rctx, target(r.st, *n), wgAddrCommand)
		cancel()
		if err != nil || exit != 0 {
			return fmt.Errorf("failed to read wg0 on %s (exit %d, %v): %s", n.Name, exit, err, strings.TrimSpace(errOut))
		}
		m := wgInetPattern.FindStringSubmatch(out)
		if m == nil || !subnet.Contains(net.ParseIP(m[1])) {
			return fmt.Errorf("%s has no wg0 address in %s: %q", n.Name, wgSubnet, strings.TrimSpace(out))
		}
		n.WGIP = m[1]
	}
	return nil
}

// deployChain runs the chain validators on the three nodes.
func (r *run) deployChain(ctx context.Context) error {
	var spec []string
	for _, n := range r.st.Nodes {
		spec = append(spec, n.Name+":"+n.PublicIP+":"+n.WGIP)
	}
	id := chainID(r.cfg.RunID)
	env := append(hostEnv(), r.goEnv...)
	env = append(env,
		"CHAIN_ID="+id, "E2E_CHAIN_NODES="+strings.Join(spec, " "),
		"E2E_SSH_KEY="+r.st.SSHKeyFile, "E2E_KNOWN_HOSTS="+r.st.KnownHostsFile, "E2E_SSH_USER="+sshUser,
		"EPOCH_DURATION="+r.cfg.EpochDuration, "EPOCH_MIN_BLOCKS="+r.cfg.EpochMinBlocks,
		"CHAIN_ROOT="+filepath.Join(r.cfg.RepoRoot, "chain"))
	script := filepath.Join(r.cfg.RepoRoot, chainScript)
	if _, err := r.runLogged(ctx, command{name: "bash", args: []string{script, "up"}, dir: r.cfg.RepoRoot, env: env}); err != nil {
		return fmt.Errorf("failed to deploy chain %s: %w", id, err)
	}
	r.st.ChainID, r.st.ChainRPC = id, chainRPC
	return nil
}

func writeArtifact(dir, name, content string) error {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), logFileMode); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}
