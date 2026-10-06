//go:build e2e_fleet

package chainglobal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
)

// scriptBudget bounds one read-only run of the chain deploy script.
const scriptBudget = chain.TxBudget

// deployScript is the run's chain deploy script, from this package's directory.
var deployScript = filepath.Join("..", "..", "scripts", "chain-deploy.sh")

var (
	// statusLine is cmd_status's line for a healthy node: height and REST API.
	statusLine    = regexp.MustCompile(`(?m)^(node-\d+)\s+height (\d+), api ok$`)
	invariantLine = regexp.MustCompile(`(?m)^(node-\d+)\s+(\w+)\s+ok$`)
)

// runDeployScript runs `chain-deploy.sh <verb>` against the run's fleet with
// the run's key and pinned known_hosts. Only the read-only verbs are used.
func runDeployScript(t *testing.T, c *chain.Chain, verb string) (string, int) {
	t.Helper()
	if c.F.State.IsStagenet() {
		harness.SkipNotApplicable(t, "chain-deploy.sh lays out a run chain (loopback ports, its own nodes spec); the stagenet chain is deployed by chain/scripts/stagenet/deploy.sh in the orama-global netns")
	}
	if verb != "status" && verb != "invariants" {
		t.Fatalf("refusing to run chain-deploy.sh %s: only status and invariants are read-only", verb)
	}
	var spec []string
	for _, n := range c.Nodes() {
		spec = append(spec, n.Name+":"+n.PublicIP+":"+n.WGIP)
	}
	ctx, cancel := context.WithTimeout(context.Background(), scriptBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", deployScript, verb)
	// Only what the script needs: never the run's secrets, and no HOME (its
	// ssh runs with -F /dev/null and the run's pinned known_hosts).
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "CHAIN_ID=" + c.ID, "E2E_CHAIN_NODES=" + strings.Join(spec, " "),
		"E2E_SSH_USER=" + c.Nodes()[0].SSHUser, "E2E_SSH_KEY=" + c.F.State.SSHKeyFile, "E2E_KNOWN_HOSTS=" + c.F.State.KnownHostsFile,
		"CHAIN_ROOT=" + filepath.Join("..", "..", "..", "chain")}
	res, err := evidence.RunRecorded(t, c.F.Recorder(), "chain-deploy.sh "+verb, cmd)
	if err != nil {
		t.Fatalf("chain-deploy.sh %s could not run: %v", verb, err)
	}
	return c.F.Redact(res.Stdout + res.Stderr), res.Exit
}

// TestDeployScript_statusAndInvariantsReadOnly: the run's chain deploy
// script (docs/CHAIN.md "The stagenet deploy script", e2e/scripts)
// `status` reports a height and a REST API that answers for every node, and `invariants` reports every
// module ok on every node; both exit 0.
func TestDeployScript_statusAndInvariantsReadOnly(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	status, code := runDeployScript(t, c, "status")
	if code != 0 || len(statusLine.FindAllString(status, -1)) != len(c.Nodes()) {
		t.Errorf("chain-deploy.sh status exited %d:\n%s", code, status)
	}
	inv, code := runDeployScript(t, c, "invariants")
	want := len(c.Nodes()) * len(chain.InvariantModules)
	if code != 0 || len(invariantLine.FindAllString(inv, -1)) != want {
		t.Errorf("chain-deploy.sh invariants exited %d with %d ok lines, want %d:\n%s", code,
			len(invariantLine.FindAllString(inv, -1)), want, inv)
	}
}
