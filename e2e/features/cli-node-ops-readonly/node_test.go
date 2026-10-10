//go:build e2e_fleet

package clinodeopsreadonly

import (
	"context"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// dnsLookupBudget bounds one public DNS lookup of the run's delegation.
const dnsLookupBudget = 30 * time.Second

type delegation struct {
	Domain      string `json:"domain"`
	Nameservers []struct {
		Hostname string `json:"hostname"`
		IP       string `json:"ip"`
	} `json:"nameservers"`
}

// TestNodeDNSDelegation_matchesThePublicDelegation: the records `orama node
// dns delegation` prints are exactly what the parent zone must hold, and the
// run's real Cloudflare delegation holds them: every glue address is a fleet
// node and the public NS set of the base domain is the printed one
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-node-dns-delegation, website/src/docs/operator/nameserver.mdx).
// --cloudflare-token-file is never passed: it writes to Cloudflare.
func TestNodeDNSDelegation_matchesThePublicDelegation(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	var ds []delegation
	decode(t, run(t, cli, "node", "dns", "delegation", "--env", f.State.Env, "--json"), &ds)
	if len(ds) != 1 || !strings.EqualFold(ds[0].Domain, f.State.BaseDomain) || len(ds[0].Nameservers) == 0 {
		t.Fatalf("delegation for %s: %+v", f.State.BaseDomain, ds)
	}
	var printed []string
	for _, ns := range ds[0].Nameservers {
		if _, ok := f.Lookup(ns.IP); !ok {
			t.Errorf("glue %s -> %s is not a fleet node", ns.Hostname, ns.IP)
		}
		printed = append(printed, strings.ToLower(ns.Hostname+"."+f.State.BaseDomain))
	}
	ctx, cancel := context.WithTimeout(t.Context(), dnsLookupBudget)
	defer cancel()
	records, err := net.DefaultResolver.LookupNS(ctx, f.State.BaseDomain)
	if err != nil {
		t.Fatalf("public NS lookup of %s: %v", f.State.BaseDomain, err)
	}
	var public []string
	for _, r := range records {
		public = append(public, strings.ToLower(strings.TrimSuffix(r.Host, ".")))
	}
	slices.Sort(printed)
	slices.Sort(public)
	if !slices.Equal(printed, public) {
		t.Errorf("printed NS %v, the internet sees %v", printed, public)
	}
	text := run(t, cli, "node", "dns", "delegation", "--env", f.State.Env)
	infra.ExpectExit(t, text, exitOK, "IN\tNS\t", "IN\tA\t")
}

// TestNodeDNSDelegation_refusals: --env is required (usage) and an
// environment that is not configured cannot be read.
func TestNodeDNSDelegation_refusals(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	infra.ExpectExit(t, run(t, cli, "node", "dns", "delegation"), exitUsage, "--env is required")
	res := run(t, cli, "node", "dns", "delegation", "--env", "e2e-cli-absent")
	if res.Exit == exitOK {
		t.Errorf("delegation of an unconfigured environment succeeded:\n%s", output(res))
	}
}

// TestNodeMigrateRaftID_freshClusterAlreadyStable: "a fresh node is on a
// stable id from its first boot" (website/src/docs/contributor/architecture-reference.mdx), and the documented
// way to check is `orama node migrate-raft-id --env <env> --dry-run`
// (website/src/docs/operator/node-replacement.mdx), which changes nothing.
func TestNodeMigrateRaftID_freshClusterAlreadyStable(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	res := run(t, harness.CLI(t), "node", "migrate-raft-id", "--env", f.State.Env, "--dry-run")
	infra.ExpectExit(t, res, exitOK, "Raft identity in "+f.State.Env, "Every node already has a stable raft id")
	for _, n := range f.State.Nodes {
		if !strings.Contains(res.Stdout, n.PublicIP) {
			t.Errorf("the dry run does not list %s (%s)", n.Name, n.PublicIP)
		}
	}
}

// TestNodeMigrateRaftID_refusals: --env is required, and --node must name a
// node of the environment; both are refused before anything is migrated.
func TestNodeMigrateRaftID_refusals(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	infra.ExpectExit(t, run(t, cli, "node", "migrate-raft-id", "--dry-run"), exitUsage, "--env is required")
	res := run(t, cli, "node", "migrate-raft-id", "--env", f.State.Env, "--node", documentAddr, "--dry-run")
	infra.ExpectRefused(t, res, "not found")
}

type listedNode struct {
	IP          string `json:"ip"`
	Role        string `json:"role"`
	User        string `json:"user"`
	Environment string `json:"environment"`
}

// TestNodeList_sameInventoryAsNodes: `orama node list` is the same listing as
// `orama nodes` (cmd/node/list.go), and it lists every fleet node in the env.
func TestNodeList_sameInventoryAsNodes(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	var a, b []listedNode
	decode(t, cli.MustOK(t, "node", "list", "--env", f.State.Env, "--json"), &a)
	decode(t, cli.MustOK(t, "nodes", "--env", f.State.Env, "--json"), &b)
	if !slices.Equal(a, b) {
		t.Errorf("node list %+v != nodes %+v", a, b)
	}
	for _, n := range f.State.Nodes {
		if !slices.ContainsFunc(a, func(l listedNode) bool { return l.IP == n.PublicIP && l.Environment == f.State.Env }) {
			t.Errorf("node list lacks %s (%s)", n.Name, n.PublicIP)
		}
	}
	if text := cli.MustOK(t, "node", "list", "--env", f.State.Env).Stdout; !strings.Contains(text, "node(s) in "+f.State.Env) {
		t.Errorf("node list table lacks its count line:\n%s", text)
	}
}

// TestNodeLogs_onNode: `orama node logs` reads the journal of one service on
// the node it runs on, by alias or by full template-instance name, and
// validates --lines (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-node-logs).
func TestNodeLogs_onNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	for _, args := range [][]string{
		{"node", "logs", "node", "--lines", "5"},
		{"node", "logs", infra.IndexGatewayUnit, "--lines", "5"},
		{"node", "logs", "node", "--since", "-30min"},
	} {
		out := infra.OnNode(t, f, n, args...)
		if out.Exit != exitOK || strings.TrimSpace(out.Stdout) == "" {
			t.Errorf("on %s, orama %v: exit %d, %d bytes\n%s", n.Name, args, out.Exit, len(out.Stdout), f.Redact(out.Stderr))
		}
	}
	expectNodeRefusal(t, f, n, exitUsage, "node", "logs", "node", "--lines", "0")
	expectNodeRefusal(t, f, n, -1, "node", "logs", "e2e-no-such-service")
}

// expectNodeRefusal runs orama on n and fails unless it is refused, with
// exit want when want >= 0.
func expectNodeRefusal(t testing.TB, f *fleet.Fleet, n fleet.Node, want int, args ...string) {
	t.Helper()
	out := infra.OnNode(t, f, n, args...)
	if out.Exit == exitOK || (want >= 0 && out.Exit != want) {
		t.Errorf("on %s, orama %v: exit %d, want a refusal (%d)\n%s%s", n.Name, args, out.Exit, want, out.Stdout, f.Redact(out.Stderr))
	}
}
