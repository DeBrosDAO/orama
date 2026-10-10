//go:build e2e_fleet

package infra

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// extraLocation is where joining servers are created (Hetzner Helsinki).
const extraLocation = "hel1"

// SetupArgs is `orama node setup` joining extra through the first core node,
// the command provisioning runs for every joiner (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama
// node setup"): the run key opens the fresh server once, its host key is
// pinned, and the invite is minted on the --join-via node over SSH.
func SetupArgs(t testing.TB, extra harness.Extra, archive string) []string {
	t.Helper()
	f := harness.Fleet(t)
	via := f.State.Nodes[0]
	return []string{"node", "setup", "--ip", extra.PublicIP, "--user", extra.SSHUser, "--env", f.State.Env,
		"--role", "node", "--base-domain", f.State.BaseDomain, "--archive", archive,
		"--host-key", extra.HostKey, "--bootstrap-key", f.State.SSHKeyFile,
		"--join-via", via.SSHUser + "@" + via.PublicIP}
}

// NewJoinedExtra creates a server, joins it as a fourth node with `orama node
// setup`, and waits until it is a full member: the cluster converges with it
// in the report. The cleanup, registered before the join, removes it from the
// cluster again (`orama node remove --force`) if a test left it a member,
// and waits for the three core nodes to converge without it; the server is
// deleted after that by ExtraNode's own cleanup.
func NewJoinedExtra(t testing.TB, name string) harness.Extra {
	t.Helper()
	extra := harness.ExtraNode(t, name, extraLocation)
	JoinExtra(t, extra)
	return extra
}

// JoinExtra joins a fresh extra server as NewJoinedExtra does.
func JoinExtra(t testing.TB, extra harness.Extra) {
	t.Helper()
	f := harness.Fleet(t)
	RequireHealthy(t)
	archive := RunningArchive(t, f)
	cli := harness.CLI(t)
	t.Cleanup(func() { RemoveMembers(t, f, cli, extra.PublicIP) })
	res := RunFor(t, cli, InstallBudget, SetupArgs(t, extra, archive)...)
	ExpectExit(t, res, ExitOK, "setup complete")
	r := WaitConverged(t, len(f.State.Nodes)+1, ConvergeBudget, extra.Name+" to join as a full member")
	if _, err := ReportFor(r, extra.Node); err != nil {
		t.Fatal(err)
	}
}

// NewExtra is a fresh server for this test, nothing installed.
func NewExtra(t testing.TB, name string) harness.Extra {
	t.Helper()
	return harness.ExtraNode(t, name, extraLocation)
}

// RemoveIfMember is RemoveMembers for one host with the run's fleet and CLI
// looked up at call time. Inside a cleanup that lookup fails once the run is
// interrupted (harness.Fleet refuses), so cleanups use RemoveMembers with the
// fleet and CLI captured before registering; this form remains for callers
// outside this change's scope (invite-join-destructive).
func RemoveIfMember(t testing.TB, host string) {
	t.Helper()
	RemoveMembers(t, harness.Fleet(t), harness.CLI(t), host)
}

// RemoveMembers removes every host the monitor still lists from the cluster,
// then waits once for the core nodes to converge without all of them: a
// convergence wait per host would expect the core count while the other
// joiners are still members and never succeed. For cleanups: f and cli are
// captured before the cleanup is registered, and it reports, never Fatals.
func RemoveMembers(t testing.TB, f *fleet.Fleet, cli *oramacli.Runner, hosts ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), InstallBudget+ConvergeBudget)
	defer cancel()
	removed := false
	for _, host := range hosts {
		if !isMember(ctx, cli, f.State.Env, host) {
			continue
		}
		args := []string{"node", "remove", "--env", f.State.Env, "--node", host, "--force"}
		if !sshReachable(host) {
			args = append(args, "--offline")
		}
		res, err := cli.Run(ctx, args...)
		if err != nil || res.Exit != 0 {
			t.Errorf("cleanup: orama node remove %s failed (exit %d): %v\n%s%s", host, res.Exit, err, res.Stdout, f.Redact(res.Stderr))
			continue
		}
		removed = true
	}
	if !removed {
		return
	}
	err := eventually.Poll(ctx, PollEvery, ConvergeBudget, "the core nodes to converge without the joiners", func() (bool, error) {
		r, err := monitor.Get(ctx, cli, f.State.Env)
		if err != nil {
			return false, err
		}
		if err := r.Converged(len(f.State.Nodes)); err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("cleanup: %v", err)
	}
}

// sshReachable reports whether host still accepts TCP on 22: a destroyed
// server is retired cluster-side only (`orama node remove --offline`).
func sshReachable(host string) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, "22"), dialBudget)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func isMember(ctx context.Context, cli *oramacli.Runner, env, host string) bool {
	r, err := monitor.Get(ctx, cli, env)
	if err != nil {
		return true
	}
	for _, n := range r.Nodes {
		if n.Host == host {
			return true
		}
	}
	return false
}

// WGIPOf is the WireGuard address the report gives host.
func WGIPOf(r *monitor.Report, host string) (string, error) {
	for _, n := range r.Nodes {
		if n.Host == host && n.Report != nil && n.Report.WGIP != "" {
			return n.Report.WGIP, nil
		}
	}
	return "", fmt.Errorf("%s has no WireGuard address in the monitor report", host)
}
