//go:build e2e_fleet

package joinonecommand

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	// networkEnv names a network the CLI knows whose chain is the fleet's;
	// manifestEnv, when set, is the manifest URL it is added from first.
	networkEnv  = "E2E_SETUP_NETWORK"
	manifestEnv = "E2E_SETUP_MANIFEST_URL"
	// extraLocation is where the fresh servers are created.
	extraLocation = "hel1"
	// nodeName is the base name of the nodes the tests register; the other
	// nodes of a run are <name>-2 and <name>-3.
	nodeName = "e2ejoin"
	// fundingNorama is what the operator account is given before setup: 5,000
	// ORAMA covers the bonds, the 1,000 ORAMA self-bond and the fees.
	fundingNorama = "5000000000000"
	// statusBudget bounds one `orama status`.
	statusBudget = infra.ConvergeBudget
)

// network returns the name of the network the tests join, adding it from its
// manifest when the run says where it is. Without E2E_SETUP_NETWORK the test is
// not applicable: the registry built into the CLI has no network of the fleet.
func network(t *testing.T, cli *oramacli.Runner) string {
	t.Helper()
	name := os.Getenv(networkEnv)
	if name == "" {
		harness.SkipNotApplicable(t, "set "+networkEnv+" to a network the CLI knows whose chain and release are the fleet's (and "+manifestEnv+" to add it from its manifest)")
	}
	if url := os.Getenv(manifestEnv); url != "" {
		infra.ExpectExit(t, infra.Run(t, cli, "network", "add", url, "--yes"), infra.ExitOK)
	}
	return name
}

// machineArgs name the fresh servers, unattended: each server's host key is
// pinned from the fingerprint the provisioner recorded, and the run's key opens
// it once.
func machineArgs(t *testing.T, extras ...harness.Extra) []string {
	t.Helper()
	f := harness.Fleet(t)
	args := []string{"--user", extras[0].SSHUser, "--bootstrap-key", f.State.SSHKeyFile}
	for _, e := range extras {
		args = append(args, "--ip", e.PublicIP, "--host-key", e.PublicIP+"="+e.HostKey)
	}
	return args
}

// setupArgs is `orama setup` for the fresh servers, joining net.
func setupArgs(t *testing.T, net, env string, extras ...harness.Extra) []string {
	t.Helper()
	return append([]string{"setup", "--network", net, "--env", env, "--yes"}, machineArgs(t, extras...)...)
}

// isolatedCLI is the operator's CLI in a HOME of its own: setup records the
// cluster there, and nothing leaks into the run's shared environment list.
func isolatedCLI(t *testing.T) *oramacli.Runner {
	t.Helper()
	return harness.CLI(t).Isolated(t)
}

// statusDoc is the part of `orama status --json` the tests read.
type statusDoc struct {
	Healthy bool `json:"healthy"`
	Nodes   []struct {
		Host   string `json:"host"`
		Status string `json:"status"`
		Chain  *struct {
			Responsive bool `json:"responsive"`
			CatchingUp bool `json:"catching_up"`
			Validator  bool `json:"validator"`
		} `json:"chain"`
	} `json:"nodes"`
	Operator *struct {
		Address string `json:"address"`
	} `json:"operator"`
}

// status runs `orama status --json --ssh` for env and decodes it. SSH is used
// because a cluster with no domain has no gateway name to reach.
func status(t *testing.T, cli *oramacli.Runner, env string) statusDoc {
	t.Helper()
	res := infra.RunFor(t, cli, statusBudget, "status", "--env", env, "--ssh", "--json")
	if res.Exit != infra.ExitOK && !strings.Contains(res.Stdout, `"healthy"`) {
		t.Fatalf("orama status: exit %d\n%s%s", res.Exit, res.Stdout, res.Stderr)
	}
	var doc statusDoc
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatalf("orama status --json is not JSON: %v\n%s", err, res.Stdout)
	}
	return doc
}

// requireHealthy fails unless status says the cluster is healthy with want nodes.
func requireHealthy(t *testing.T, cli *oramacli.Runner, env string, want int) statusDoc {
	t.Helper()
	doc := status(t, cli, env)
	if !doc.Healthy || len(doc.Nodes) != want {
		raw, _ := json.MarshalIndent(doc, "", "  ")
		t.Fatalf("orama status: healthy=%v with %d nodes, want healthy with %d\n%s", doc.Healthy, len(doc.Nodes), want, raw)
	}
	return doc
}

// requireNodeName fails unless the chain says node id holds the name setup claimed for it, which
// is the node's own name.
func requireNodeName(t *testing.T, id string) {
	t.Helper()
	c := chain.New(t)
	var ofNode struct {
		Name struct {
			Name    string    `json:"name"`
			NodeID  string    `json:"node_id"`
			Deposit chain.Int `json:"deposit"`
		} `json:"name"`
	}
	c.Query(t, c.Node(t, 0), &ofNode, "nodes", "name-of-node", id)
	if ofNode.Name.Name != id || ofNode.Name.NodeID != id || ofNode.Name.Deposit.IsZero() {
		t.Errorf("name-of-node %s = %+v, want the name %s with its deposit locked", id, ofNode.Name, id)
	}
}
