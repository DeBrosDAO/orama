package provision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// upgradeLogPrefix names the command logs UpgradeToHead writes.
const upgradeLogPrefix = "upgrade"

// UpgradeToHead rolls the run's cluster forward to the HEAD archive through
// the CLI under test, the way docs/DEV_DEPLOY.md upgrades a cluster: push the
// archive to every node, then `orama node upgrade --node <ip> --yes` one node
// at a time, followers first and the RQLite leader last. The cluster must be
// healthy before the first node, and after each node both that node's report
// and the whole cluster's must be healthy again before the next one is
// touched; the first failure stops the upgrade with the remaining nodes
// untouched. It is meant for a run Up installed with the previous release
// (E2E_INSTALL_PREVIOUS=1), and works on any run.
func UpgradeToHead(ctx context.Context, st *fleet.State, log Logger) error {
	if err := refuseStagenet("UpgradeToHead", st); err != nil {
		return err
	}

	return upgradeToHead(ctx, st, log, execCommander{}, defaultTiming())
}

func upgradeToHead(ctx context.Context, st *fleet.State, log Logger, cmd commander, tm timing) error {
	if err := checkUpgradeState(st); err != nil {
		return err
	}
	u := &upgrader{st: st, log: log, cmd: cmd, tm: tm}
	if err := u.prepareTmp(); err != nil {
		return err
	}
	if _, err := u.orama(ctx, "push", "node", "push", "--env", st.Env, "--archive", st.ArchivePath); err != nil {
		return fmt.Errorf("failed to push the HEAD archive %s: %w", st.ArchivePath, err)
	}
	h, err := u.waitCluster(ctx)
	if err != nil {
		return fmt.Errorf("the cluster is not healthy, not upgrading anything: %w", err)
	}
	order, err := upgradeOrder(st.Nodes, h.Summary.RQLiteLeader)
	if err != nil {
		return err
	}
	for _, n := range order {
		if err := u.upgradeNode(ctx, n); err != nil {
			return err
		}
	}
	log.Infof("run %s is on HEAD", st.RunID)
	return nil
}

// checkUpgradeState refuses a state UpgradeToHead cannot drive.
func checkUpgradeState(st *fleet.State) error {
	if st == nil || !runIDPattern.MatchString(st.RunID) {
		return errors.New("refusing to upgrade: the state has no valid run id")
	}
	if st.Env == "" || st.OramaBin == "" || st.ArchivePath == "" || st.Home == "" || st.RWSock == "" || st.ArtifactDir == "" {
		return fmt.Errorf("refusing to upgrade run %s: the state lacks the environment, CLI, HEAD archive, test agent or artifact dir", st.RunID)
	}
	if len(st.Nodes) == 0 {
		return fmt.Errorf("refusing to upgrade run %s: it has no nodes", st.RunID)
	}
	return nil
}

// upgradeOrder is the nodes with the leader (named by public or WireGuard
// address) moved last; a leader that is none of them stops the upgrade, since
// the order would then be a guess.
func upgradeOrder(nodes []fleet.Node, leader string) ([]fleet.Node, error) {
	var order []fleet.Node
	var last *fleet.Node
	for i, n := range nodes {
		if leader != "" && (n.PublicIP == leader || n.WGIP == leader) {
			last = &nodes[i]
			continue
		}
		order = append(order, n)
	}
	if last == nil {
		return nil, fmt.Errorf("the RQLite leader %q is not a node of this run; not upgrading without knowing which node goes last", leader)
	}
	return append(order, *last), nil
}

// upgrader runs the CLI under test with the run's test agent.
type upgrader struct {
	st  *fleet.State
	log Logger
	cmd commander
	tm  timing
	seq int
}

// upgradeNode upgrades n, then gates on n and on the whole cluster.
func (u *upgrader) upgradeNode(ctx context.Context, n fleet.Node) error {
	u.log.Infof("upgrading %s (%s) to HEAD", n.Name, n.PublicIP)
	if _, err := u.orama(ctx, "node-upgrade-"+n.Name, "node", "upgrade", "--env", u.st.Env, "--node", n.PublicIP, "--yes"); err != nil {
		return fmt.Errorf("the upgrade of %s failed; the nodes after it are untouched: %w", n.Name, err)
	}
	nodeReport := u.command("monitor-"+n.Name, "monitor", "report", "--env", u.st.Env, "--node", n.PublicIP, "--json")
	if _, err := waitReport(ctx, u.cmd, nodeReport, u.tm, nodeProblem); err != nil {
		return fmt.Errorf("%s did not come back healthy after its upgrade: %w", n.Name, err)
	}
	if _, err := u.waitCluster(ctx); err != nil {
		return fmt.Errorf("the cluster is not healthy after upgrading %s: %w", n.Name, err)
	}
	return nil
}

// nodeProblem judges a report scoped to one node: that node must be healthy.
// The cluster-wide fields (leader, quorum) are judged by the cluster report.
func nodeProblem(h healthReport) string {
	if h.Meta.NodeCount != 1 || h.Meta.HealthyCount != 1 {
		return fmt.Sprintf("%d of %d nodes healthy (want the one node)", h.Meta.HealthyCount, h.Meta.NodeCount)
	}
	return ""
}

// waitCluster waits for the whole cluster to report healthy.
func (u *upgrader) waitCluster(ctx context.Context) (healthReport, error) {
	var h healthReport
	c := u.command("monitor-cluster", "monitor", "report", "--env", u.st.Env, "--json")
	_, err := waitReport(ctx, u.cmd, c, u.tm, func(r healthReport) string {
		h = r
		return r.problem(len(u.st.Nodes))
	})
	return h, err
}

func (u *upgrader) orama(ctx context.Context, label string, args ...string) (string, error) {
	return u.cmd.Run(ctx, u.command(label, args...))
}

// command is the CLI under test with args, logging to a numbered file.
func (u *upgrader) command(label string, args ...string) command {
	u.seq++
	name := fmt.Sprintf("%s-%02d-%s.log", upgradeLogPrefix, u.seq, sanitize(label))
	env := []string{"PATH=" + os.Getenv("PATH"), e2eFlag, "HOME=" + u.st.Home, "RW_AGENT_SOCK=" + u.st.RWSock, "TMPDIR=" + u.tmpDir()}
	return command{name: u.st.OramaBin, args: args, env: env, log: filepath.Join(u.st.ArtifactDir, name)}
}

// tmpDir is the CLI's private TMPDIR, inside the test agent's directory
// (which Down shreds).
func (u *upgrader) tmpDir() string { return filepath.Join(u.st.Home, tmpDirName) }

func (u *upgrader) prepareTmp() error {
	if err := os.MkdirAll(u.tmpDir(), dirMode); err != nil {
		return fmt.Errorf("failed to create the CLI's TMPDIR %s: %w", u.tmpDir(), err)
	}
	return nil
}
