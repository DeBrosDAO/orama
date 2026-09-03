package lifecycle

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Infrastructure hooks.
//
// Most of what these scenarios do goes through the orama CLI, deliberately: a
// harness that reaches around the CLI tests a path no operator runs. Four
// things have no CLI equivalent, because they are not things the CLI should be
// able to do:
//
//   - destroy a VM with no clean shutdown
//   - break a node so an upgrade fails on it
//   - provision a brand-new VM to join
//   - stop the index rqlite on every node at once
//
// Each is a command supplied by whoever runs the harness — Multipass, Lima, a
// cloud CLI — through an environment variable. A scenario whose hook is not
// configured SKIPS rather than passing: a green run that silently omitted the
// kill-a-voter scenario would be worse than no harness at all.
const (
	// DestroyHookVar destroys a node abruptly. Receives the host as $1.
	// Example: ORAMA_LIFECYCLE_DESTROY="multipass delete --purge"
	DestroyHookVar = "ORAMA_LIFECYCLE_DESTROY"

	// BreakHookVar makes an upgrade fail on a node. Receives the host as $1.
	BreakHookVar = "ORAMA_LIFECYCLE_BREAK"

	// ProvisionHookVar creates a new node and prints its host on stdout.
	ProvisionHookVar = "ORAMA_LIFECYCLE_PROVISION"

	// ResolverVar is the resolver DNS assertions query. Defaults to the
	// environment's own nameservers, which is what an end user hits.
	ResolverVar = "ORAMA_LIFECYCLE_RESOLVER"

	// BaseDomainVar is the zone the DNS scenarios resolve within.
	BaseDomainVar = "ORAMA_LIFECYCLE_BASE_DOMAIN"

	hookBudget = 10 * time.Minute
	dnsBudget  = 5 * time.Second
)

// runHook executes a configured hook, skipping the test when it is not set.
func runHook(t *testing.T, envVar, purpose string, args ...string) string {
	t.Helper()

	hook := strings.TrimSpace(os.Getenv(envVar))
	if hook == "" {
		t.Skipf("%s is not set; skipping — this scenario needs to %s, "+
			"which has no CLI equivalent (see docs/DEV_DEPLOY.md, 'Lifecycle harness')",
			envVar, purpose)
	}

	ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
	defer cancel()

	fields := strings.Fields(hook)
	fields = append(fields, args...)

	out, err := exec.CommandContext(ctx, fields[0], fields[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s hook failed (%s): %v\n%s", envVar, strings.Join(fields, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

// DestroyNode kills a node with no clean shutdown, the way a failed VPS does.
//
// Not `orama node stop`: a clean stop transfers leadership, drains, and
// deregisters, which is the path that already works. What has never been tested
// is the node that simply stops existing.
func DestroyNode(t *testing.T, c *Cluster, host string) {
	t.Helper()
	t.Logf("destroying %s abruptly", host)
	runHook(t, DestroyHookVar, "destroy a VM with no clean shutdown", host)
}

// BreakNode makes the next upgrade fail on this node.
//
// What "broken" means is the hook's business — a corrupted binary, a full disk,
// a masked unit. The scenario only asserts that the rollout notices and stops.
func BreakNode(t *testing.T, c *Cluster, host string) {
	t.Helper()
	t.Logf("breaking %s so its upgrade fails", host)
	runHook(t, BreakHookVar, "make an upgrade fail on a node", host)
}

// ProvisionNode creates a new VM and joins it, returning its host.
//
// The join itself goes through the CLI — `orama node invite` then
// `orama node install --join` — because that is the path being tested. The hook
// only supplies a machine.
func ProvisionNode(t *testing.T, c *Cluster) string {
	t.Helper()

	host := runHook(t, ProvisionHookVar, "create a new VM to join")
	if host == "" {
		t.Fatalf("%s produced no host on stdout", ProvisionHookVar)
	}
	if net.ParseIP(host) == nil {
		t.Fatalf("%s printed %q, which is not an IP address", ProvisionHookVar, host)
	}

	t.Logf("joining %s", host)
	token := strings.TrimSpace(c.MustCLI("node", "invite", "--env", c.Env))
	if token == "" {
		t.Fatal("orama node invite produced no token")
	}
	c.MustCLI("node", "install", "--env", c.Env, "--vps-ip", host, "--token", token)
	return host
}

// StopIndexRQLiteEverywhere takes the index rqlite down on every node and
// returns a function that brings it back.
//
// Through the CLI, since stopping a service is something the CLI does. The
// restore runs from a defer, so a failed assertion still leaves the environment
// usable for the next scenario.
func StopIndexRQLiteEverywhere(t *testing.T, c *Cluster) func() {
	t.Helper()

	for _, host := range c.Nodes {
		t.Logf("stopping index rqlite on %s", host)
		c.MustCLI("node", "stop", "--env", c.Env, "--node", host, "--service", "rqlite")
	}

	return func() {
		for _, host := range c.Nodes {
			if _, err := c.CLI("node", "start", "--env", c.Env, "--node", host, "--service", "rqlite"); err != nil {
				t.Logf("could not restart index rqlite on %s: %v", host, err)
			}
		}
		c.WaitConverged(ConvergeBudget, "the cluster reconverged after rqlite came back",
			func(r *Report) error { return r.Converged(len(c.Nodes)) })
	}
}

// WireGuardIPOf returns a node's overlay address, which is the identity every
// membership store keys on.
func WireGuardIPOf(t *testing.T, c *Cluster, host string) string {
	t.Helper()

	r, err := c.Report()
	if err != nil {
		t.Fatalf("cannot read the cluster: %v", err)
	}
	for _, n := range r.Nodes {
		if n.Host == host {
			if n.Report.WireGuardIP == "" {
				t.Fatalf("%s reports no WireGuard IP", host)
			}
			return n.Report.WireGuardIP
		}
	}
	t.Fatalf("%s is not in the monitor report", host)
	return ""
}

// SomeResolvableName returns a name inside the environment's zone.
func SomeResolvableName(t *testing.T, c *Cluster) string {
	t.Helper()

	base := strings.TrimSpace(os.Getenv(BaseDomainVar))
	if base == "" {
		t.Skipf("%s is not set; skipping — the DNS scenarios need the zone to query", BaseDomainVar)
	}
	return "ns1." + base
}

// Resolves reports whether name answers, through the environment's own
// nameservers.
//
// dig rather than the Go resolver: the Go resolver consults the host's
// configuration, so a passing assertion could be measuring a laptop's cache
// rather than the cluster.
func Resolves(name string) error {
	args := []string{"+short", "+time=2", "+tries=1", name}
	if resolver := strings.TrimSpace(os.Getenv(ResolverVar)); resolver != "" {
		args = append([]string{"@" + resolver}, args...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dnsBudget)
	defer cancel()

	out, err := exec.CommandContext(ctx, "dig", args...).Output()
	if err != nil {
		return fmt.Errorf("dig %s: %w", strings.Join(args, " "), err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return fmt.Errorf("%s returned no answer", name)
	}
	return nil
}
