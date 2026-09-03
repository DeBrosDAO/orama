package lifecycle

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Cluster drives a real 3-node environment through the orama CLI.
//
// Only through the CLI. Every scenario here exists because a bug reached
// production through a path an operator actually takes — `orama node upgrade`,
// `orama node restart`, a reboot — and a harness that reaches around the CLI to
// set up its own state tests something no operator ever runs.
//
// Observation is the same: `orama monitor report --json`, `dig`, and the
// gateway's own /health. Reading a node's state over SSH would let a scenario
// pass while the CLI an operator uses reports something different, which is
// precisely the class of bug the stability train is about.
type Cluster struct {
	t   *testing.T
	Env string

	// Bin is the orama binary under test.
	Bin string

	// Nodes is the environment's node list, from nodes.conf.
	Nodes []string
}

// Budgets. Every scenario is bounded, so a hung cluster fails the test rather
// than the CI job's global timeout.
const (
	// ConvergeBudget is how long a cluster has to settle after one node is
	// disturbed. Generous: it covers a systemd restart, a raft rejoin, and a
	// WireGuard handshake.
	ConvergeBudget = 5 * time.Minute

	// ColdStartBudget covers every node restarting at once, which has no
	// leader to catch up from until an election completes.
	ColdStartBudget = 10 * time.Minute

	// ServingBudget is how long the public surfaces (DNS, TLS) have to answer
	// after a cold start. Much shorter than ColdStartBudget on purpose: those
	// must not wait for raft.
	ServingBudget = 90 * time.Second

	// PollInterval is how often a wait re-reads the cluster. A monitor report
	// is an SSH fan-out, so this is not free.
	PollInterval = 10 * time.Second

	// CommandBudget bounds one CLI invocation.
	CommandBudget = 15 * time.Minute
)

// EnvVar names the environment the harness runs against. There is no default:
// a scenario in this package reboots nodes and destroys VMs, and picking an
// environment for the operator is not something it should do.
const EnvVar = "ORAMA_LIFECYCLE_ENV"

// New builds the fixture, skipping when the harness is not configured.
//
// Skip rather than fail: `go test ./...` must stay green on a laptop, and these
// tests need infrastructure that a laptop does not have.
func New(t *testing.T) *Cluster {
	t.Helper()

	env := os.Getenv(EnvVar)
	if env == "" {
		t.Skipf("%s is not set; skipping the lifecycle harness "+
			"(see docs/DEV_DEPLOY.md, 'Lifecycle harness')", EnvVar)
	}
	if strings.EqualFold(env, "testnet") || strings.EqualFold(env, "mainnet") {
		t.Fatalf("%s=%s: these scenarios reboot nodes and destroy VMs; "+
			"point them at a disposable environment", EnvVar, env)
	}

	bin := os.Getenv("ORAMA_BIN")
	if bin == "" {
		bin = "./bin/orama"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("orama binary not found at %s (set ORAMA_BIN, or run `make build`): %v", bin, err)
	}

	c := &Cluster{t: t, Env: env, Bin: bin}
	c.Nodes = c.hosts()
	if len(c.Nodes) < 3 {
		t.Fatalf("environment %q has %d nodes; these scenarios need at least 3 "+
			"so a single failure leaves a quorum", env, len(c.Nodes))
	}
	return c
}

// CLI runs an orama command and returns its combined output.
func (c *Cluster) CLI(args ...string) (string, error) {
	c.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), CommandBudget)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.Bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("orama %s: %w\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

// MustCLI runs an orama command and fails the test if it does not succeed.
func (c *Cluster) MustCLI(args ...string) string {
	c.t.Helper()
	out, err := c.CLI(args...)
	if err != nil {
		c.t.Fatalf("%v", err)
	}
	return out
}

// Report reads the current cluster state through `orama monitor report --json`.
func (c *Cluster) Report() (*Report, error) {
	out, err := c.CLI("monitor", "report", "--env", c.Env, "--json")
	if err != nil {
		return nil, err
	}
	return ParseReport([]byte(out))
}

// hosts reads the environment's node list.
func (c *Cluster) hosts() []string {
	c.t.Helper()
	r, err := c.Report()
	if err != nil {
		c.t.Fatalf("cannot read %s: %v", c.Env, err)
	}
	hosts := make([]string, 0, len(r.Nodes))
	for _, n := range r.Nodes {
		hosts = append(hosts, n.Host)
	}
	return hosts
}

// WaitConverged blocks until the cluster satisfies check, or the budget runs
// out.
//
// The failure carries the last unmet condition, not "timed out": "10.0.0.2: 1
// wg peers, want 2" says what to look at, and a timeout does not.
func (c *Cluster) WaitConverged(budget time.Duration, what string, check func(*Report) error) {
	c.t.Helper()

	deadline := time.Now().Add(budget)
	var last error

	for {
		r, err := c.Report()
		if err == nil {
			last = check(r)
			if last == nil {
				c.t.Logf("  ✓ %s", what)
				return
			}
		} else {
			last = err
		}

		if time.Now().After(deadline) {
			c.t.Fatalf("%s did not happen within %s: %v", what, budget, last)
		}
		time.Sleep(PollInterval)
	}
}

// RequireHealthy fails the test unless the cluster is converged right now.
//
// Every scenario calls this first. A scenario that starts from a broken cluster
// reports a failure that has nothing to do with what it was testing, and those
// are the results that get ignored.
func (c *Cluster) RequireHealthy() {
	c.t.Helper()
	r, err := c.Report()
	if err != nil {
		c.t.Fatalf("precondition: cannot read the cluster: %v", err)
	}
	if err := r.Converged(len(c.Nodes)); err != nil {
		c.t.Fatalf("precondition: %v", err)
	}
	if err := r.LeaderAgreement(); err != nil {
		c.t.Fatalf("precondition: %v", err)
	}
}

// Leader returns the host of the current raft leader.
func (c *Cluster) Leader() string {
	c.t.Helper()
	r, err := c.Report()
	if err != nil {
		c.t.Fatalf("cannot read the cluster: %v", err)
	}
	if r.Summary.RQLiteLeader == "" {
		c.t.Fatal("the cluster has no leader")
	}
	return r.Summary.RQLiteLeader
}

// AnyFollower returns the host of some node that is not the leader.
func (c *Cluster) AnyFollower() string {
	c.t.Helper()
	r, err := c.Report()
	if err != nil {
		c.t.Fatalf("cannot read the cluster: %v", err)
	}
	for _, n := range r.Nodes {
		if n.Report.RQLite.RaftState == "Follower" {
			return n.Host
		}
	}
	c.t.Fatal("no follower found")
	return ""
}
