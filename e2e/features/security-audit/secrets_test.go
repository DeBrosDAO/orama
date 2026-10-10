//go:build e2e_fleet

package securityaudit

import (
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Root-only trees (docs/whitepaper/technical-reference/vol1/30-install-and-upgrade.md "Tenant deployments", "Env files",
// "Which key signs a token").
const (
	unitEnvDir     = "/var/lib/orama-unit-env"
	deployEnvDir   = "/var/lib/orama-deploy"
	gatewayKeysDir = "/var/lib/orama-gateway-keys/index"
	journalWindow  = "-6h"
	// cmdlineSamples is how many times the process table is scanned.
	cmdlineSamples = 5
)

// secretScanScript reads the node's shared secrets on the node and counts
// how often any of them appears in text on stdin; it prints only the count,
// so no secret reaches the runner or the evidence.
const secretScanScript = `import sys
paths=["` + infra.OramaSecretsDir + `/cluster-secret","` + infra.OramaSecretsDir + `/rqlite-password","` + infra.OramaSecretsDir + `/api-key-hmac-secret","` + infra.OramaSecretsDir + `/swarm.key"]
vals=[]
for p in paths:
    try:
        for line in open(p).read().splitlines():
            if len(line.strip())>=16 and "/" not in line: vals.append(line.strip())
    except FileNotFoundError: pass
if not vals: print("NOSECRETS");sys.exit(0)
data=sys.stdin.read();print(sum(data.count(v) for v in vals))
`

// scanFor pipes producer into the scan and returns the match count.
func scanFor(t *testing.T, f *fleet.Fleet, n fleet.Node, producer string) int {
	t.Helper()
	out := f.MustExec(t, n, "("+producer+") 2>/dev/null | python3 -c "+fleet.ShellQuote(secretScanScript))
	s := strings.TrimSpace(out.Stdout)
	if s == "NOSECRETS" {
		t.Fatalf("%s: none of the shared secret files could be read: the scan would prove nothing", n.Name)
	}
	count, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("%s: scan printed %q", n.Name, s)
	}
	return count
}

// TestSecrets_neverOnACommandLine: no process's argv carries the cluster
// secret, the rqlite password, the API-key HMAC secret or the swarm key,
// sampled several times (docs/whitepaper/technical-reference/vol1/30-install-and-upgrade.md "Secrets never on a command line";
// rqlite credentials go to curl on stdin).
func TestSecrets_neverOnACommandLine(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		for range cmdlineSamples {
			if c := scanFor(t, f, n, "cat /proc/[0-9]*/cmdline | tr '\\0' ' '"); c != 0 {
				t.Errorf("%s: a shared secret appears %d times on process command lines", n.Name, c)
				break
			}
		}
	}
}

// TestSecrets_neverInTheJournal: no journal line of the last hours carries
// one of those secrets (docs/whitepaper/technical-reference/vol1/29-build-signing-and-release.md: the report and the logs carry no
// secret material).
func TestSecrets_neverInTheJournal(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		if c := scanFor(t, f, n, "journalctl --no-pager -q -o cat --since "+journalWindow); c != 0 {
			t.Errorf("%s: a shared secret appears %d times in the journal", n.Name, c)
		}
	}
}

// TestSecrets_filesAndTreesClosed: secrets are the orama user's alone, the
// node's own key 0600, wg0.conf root 0600, the unit-env tree root-owned and
// written by nobody else, the deployment env tree and the index gateway's
// signing keys root-only (docs/whitepaper/technical-reference/vol1/30-install-and-upgrade.md "Install and upgrade never
// follow a symlink", "Env files", "What a gateway writes").
func TestSecrets_filesAndTreesClosed(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	checks := map[string]string{
		"secrets readable by others":      "find " + infra.OramaSecretsDir + " -perm /007",
		"secrets not orama's":             "find " + infra.OramaSecretsDir + " ! -user orama",
		"unit env not root's or writable": "find " + unitEnvDir + " \\( ! -user root -o -perm /022 \\)",
		"unit env readable by others":     "find " + unitEnvDir + " -type f -perm /004",
		"deploy env open to non-root":     "find " + deployEnvDir + " -perm /077 -type f",
		"gateway keys open or not root's": "find " + gatewayKeysDir + " -type f \\( -perm /077 -o ! -user root \\)",
	}
	for _, n := range f.State.Nodes {
		infra.RequireStat(t, f, n, infra.NodeKeyPath, "orama", "orama", "600")
		infra.RequireStat(t, f, n, infra.WireGuardConfPath, "root", "root", "600")
		for what, cmd := range checks {
			if out := strings.TrimSpace(f.Exec(t, n, cmd+" 2>/dev/null").Stdout); out != "" {
				t.Errorf("%s: %s:\n%s", n.Name, what, out)
			}
		}
	}
}

// TestUnits_pid1NeverOpensAnOramaPath: no installed unit has PID 1 read an
// env file or credential, or open its output, under /opt/orama — a path the
// orama user could swap for a symlink to a root-only file (docs/whitepaper/technical-reference/vol1/05-privilege-and-filesystem-trust.md
// "No unit reads a file the orama user can write as PID 1").
func TestUnits_pid1NeverOpensAnOramaPath(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cmd := "grep -rhsE '^(EnvironmentFile|LoadCredential|LoadCredentialEncrypted|StandardOutput|StandardError)=.*(/opt/orama|file:|append:|truncate:)' " +
		"/etc/systemd/system/orama-* /etc/systemd/system/*.d/ /lib/systemd/system/orama-* 2>/dev/null | grep -F /opt/orama"
	for _, n := range f.State.Nodes {
		if out := strings.TrimSpace(f.Exec(t, n, cmd).Stdout); out != "" {
			t.Errorf("%s: a unit has PID 1 open an orama-owned path:\n%s", n.Name, out)
		}
	}
}
