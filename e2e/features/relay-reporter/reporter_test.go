//go:build e2e_fleet

package relayreporter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	// reporterBin is where `orama global install` puts orama-global.
	reporterBin = "/usr/lib/orama-global/bin/orama-global"
	// reporterHome and reporterAccount are what `orama global install --services
	// chain,dirauth,reporter` prepares: the home with the operator and the
	// authority-id, and the account that owns it.
	reporterHome    = constants.GlobalReporterHome
	reporterAccount = "orama-reporter"
	// reporterUnitFile is the unit the install writes.
	reporterUnitFile = "/etc/systemd/system/" + constants.GlobalReporterUnit
	// runBudget covers more than one epoch of the run chain (E2E_EPOCH_DURATION).
	runMinutes = 8
	runBudget  = runMinutes * time.Minute
	// passInterval is the reporter's pass interval for the run.
	passInterval = "5s"
	pollEvery    = 15 * time.Second
	// voteBudget is how long a freshly installed authority may take to export its
	// first vote: its first consensus (one voting interval and the distribution
	// delays), then the archive timer's next minute.
	voteBudget = 40 * time.Minute
	// authorityAccount owns the shared votes directory and writes the votes in it.
	authorityAccount = "orama-tor-dirauth"
)

// operatorAddress is the shape the install accepts for the operator file.
var operatorAddress = regexp.MustCompile(`^orama1[02-9ac-hj-np-z]{20,100}$`)

// node is the chain node the reporter runs on; the test is not applicable
// without orama-global on it.
func node(t *testing.T) (*chain.Chain, fleet.Node) {
	t.Helper()
	c := chain.New(t)
	n := c.Node(t, chain.OperatorNode)
	if c.F.Exec(t, n, "test -x "+reporterBin).Exit != 0 {
		harness.SkipNotApplicable(t, n.Name+" has no "+reporterBin+": the run installed no orama-global (the stagenet install does)")
	}
	return c, n
}

// installedNode is the node `orama global install --services chain,dirauth,reporter`
// ran on: the one that has the reporter's unit. A run chain has none.
func installedNode(t *testing.T) (*chain.Chain, fleet.Node) {
	t.Helper()
	c := chain.New(t)
	for _, n := range c.F.State.Nodes {
		if c.F.Exec(t, n, "test -f "+reporterUnitFile).Exit == 0 {
			return c, n
		}
	}
	harness.SkipNotApplicable(t, "no node of this target has "+constants.GlobalReporterUnit+": install a directory authority's reporter with `orama global install --services chain,dirauth,reporter` (orama.network/docs/operator/tor-network, The relay bandwidth reporter)")
	return nil, fleet.Node{}
}

// A reporter with no identity does not start: no operator file, no report.
func TestReporter_refusesToStartWithoutItsIdentity(t *testing.T) {
	c, n := node(t)
	out := c.F.Exec(t, n, `d=$(mktemp -d) && `+reporterBin+` reporter --home "$d" --rpc tcp://127.0.0.1:31001; code=$?; rm -rf "$d"; exit $code`)
	infra.ExpectNodeExit(t, "orama-global reporter without an identity", out, infra.ExitFailure, "operator", "must hold")
}

// The install prepares the reporter's home with no manual step: the account
// owns it, the operator is an account address, and the authority-id is the v3
// identity the network file the install kept lists for an authority (one file
// names the authorities for the Tor roles, the clients and the reporter).
func TestReporter_installPreparedItsHome(t *testing.T) {
	c, n := installedNode(t)
	network, err := tornet.ParseNetwork(c.F.ReadFile(t, n, constants.GlobalStateRoot+"/"+constants.GlobalTorAuthoritiesFile))
	if err != nil {
		t.Fatalf("%s: the network file the install kept: %v", n.Name, err)
	}
	id := strings.TrimSpace(c.F.MustExec(t, n, "sudo cat "+fleet.ShellQuote(reporterHome+"/authority-id")).Stdout)
	var listed bool
	for _, a := range network.Authorities {
		listed = listed || a.V3Ident == id
	}
	if !listed {
		t.Errorf("%s/authority-id is %s, which no authority of the installed network file lists as its v3_ident", reporterHome, id)
	}
	if op := strings.TrimSpace(c.F.MustExec(t, n, "sudo cat "+fleet.ShellQuote(reporterHome+"/operator")).Stdout); !operatorAddress.MatchString(op) {
		t.Errorf("%s/operator is %q, not an account address", reporterHome, op)
	}
	wantInterval := fmt.Sprintf("%dm", network.VotingIntervalMinutes)
	if got := strings.TrimSpace(c.F.MustExec(t, n, "sudo cat "+fleet.ShellQuote(reporterHome+"/vote-interval")).Stdout); got != wantInterval {
		t.Errorf("%s/vote-interval is %q, want %q, the voting interval of the installed network file", reporterHome, got, wantInterval)
	}
	for path, want := range map[string]string{
		reporterHome:                    reporterAccount + " 700",
		reporterHome + "/authority-id":  reporterAccount + " 600",
		reporterHome + "/operator":      reporterAccount + " 600",
		reporterHome + "/vote-interval": reporterAccount + " 600",
	} {
		if got := strings.TrimSpace(c.F.MustExec(t, n, "stat -c '%U %a' "+fleet.ShellQuote(path)).Stdout); got != want {
			t.Errorf("%s is %q, want %q", path, got, want)
		}
	}
	// The shared votes directory: the authority's account writes it and the
	// reporter's group reads it, setgid so a vote made in it has that group.
	if got := strings.TrimSpace(c.F.MustExec(t, n, "stat -c '%U %G %a' "+fleet.ShellQuote(constants.GlobalTorVotesDir)).Stdout); got != authorityAccount+" "+reporterAccount+" 2750" {
		t.Errorf("%s is %q, want %q", constants.GlobalTorVotesDir, got, authorityAccount+" "+reporterAccount+" 2750")
	}
	if got := strings.TrimSpace(c.F.Exec(t, n, "systemctl is-enabled "+constants.GlobalReporterUnit).Stdout); got != "enabled" {
		t.Errorf("%s is %q after the install, want enabled", constants.GlobalReporterUnit, got)
	}
}

// The authority's archive oneshot hands the reporter the authority's own vote:
// the reporter's account reads it, it is the vote of the authority in
// authority-id, and the reporter's account reads nothing of the authority's home
// (its keys).
func TestReporter_readsTheAuthoritysOwnVoteAndNothingElseOfItsHome(t *testing.T) {
	c, n := installedNode(t)
	votes := fleet.ShellQuote(constants.GlobalTorVotesDir)
	eventually.Require(t, pollEvery, voteBudget, n.Name+" to export its authority's vote", func() (bool, error) {
		return strings.TrimSpace(c.F.Exec(t, n, "sudo ls "+votes+" | grep -c '\\.vote$'").Stdout) != "0", nil
	})
	id := strings.TrimSpace(c.F.MustExec(t, n, "sudo cat "+fleet.ShellQuote(reporterHome+"/authority-id")).Stdout)
	file := strings.TrimSpace(c.F.MustExec(t, n, "sudo ls "+votes+" | grep '\\.vote$' | tail -n 1").Stdout)
	path := fleet.ShellQuote(constants.GlobalTorVotesDir + "/" + file)
	body := c.F.MustExec(t, n, "sudo -u "+reporterAccount+" cat "+path).Stdout
	if !strings.Contains(body, "\nvote-status vote\n") || !strings.Contains(body, "\ndir-source ") || !strings.Contains(strings.ToUpper(body), " "+strings.ToUpper(id)+" ") {
		t.Errorf("%s is not a vote of authority %s", file, id)
	}
	if got := strings.TrimSpace(c.F.MustExec(t, n, "stat -c '%U %G %a' "+path).Stdout); got != authorityAccount+" "+reporterAccount+" 640" {
		t.Errorf("%s is %q, want %q", file, got, authorityAccount+" "+reporterAccount+" 640")
	}
	if c.F.Exec(t, n, "sudo -u "+reporterAccount+" ls "+fleet.ShellQuote(constants.GlobalTorDirauthHome)).Exit == 0 {
		t.Errorf("the reporter's account lists the authority's home %s, which holds its keys", constants.GlobalTorDirauthHome)
	}
	if c.F.Exec(t, n, "sudo -u "+authorityAccount+" ls "+fleet.ShellQuote(reporterHome)).Exit == 0 {
		t.Errorf("the authority's account lists the reporter's home %s, which holds its hot key", reporterHome)
	}
}

type reporterState struct {
	Seen     uint64 `json:"seen_epoch"`
	Reported uint64 `json:"reported_epoch"`
}

type reporterMonitor struct {
	Epoch  uint64 `json:"epoch"`
	Chunks int    `json:"chunks"`
	Relays int    `json:"relays"`
}

// The reporter reports the epoch that closed. It needs what the install does
// not make: a hot key whose address is in x/relay's reporter set and funded (the
// reporter creates the key on its first start), and a chain whose epochs last at
// least one voting interval of the Tor network; without either the run is not
// applicable (reportBlockers) and ends at once, and with both a reporter that
// reports nothing fails after the budget. The votes it reports from are the ones
// the authority exports, which the test waits for. A reporter that is running is
// read; one that is stopped is run across an epoch close.
func TestReporter_reportsAClosedEpoch(t *testing.T) {
	c, n := installedNode(t)
	if c.F.Exec(t, n, "sudo test -f "+fleet.ShellQuote(reporterHome+"/hot-key")).Exit != 0 {
		harness.SkipNotApplicable(t, n.Name+" has no reporter hot key yet: start the reporter once, add its address to x/relay's reporter set and fund it")
	}
	skipUnlessReportable(t, c, n)
	eventually.Require(t, pollEvery, voteBudget, n.Name+" to export its authority's vote", func() (bool, error) {
		return strings.TrimSpace(c.F.Exec(t, n, "sudo ls "+fleet.ShellQuote(constants.GlobalTorVotesDir)+" | grep -c '\\.vote$'").Stdout) != "0", nil
	})
	if c.F.Unit(t, n, constants.GlobalReporterUnit) == infra.UnitActive {
		eventually.Require(t, pollEvery, runBudget, "the running reporter to report an epoch", func() (bool, error) {
			st := readJSON[reporterState](t, c, n, "state.json")
			return st.Reported > 0 && st.Reported < st.Seen, nil
		})
	} else {
		cmd := "sudo -u " + reporterAccount + " timeout " + strconv.Itoa(runMinutes) + "m " + reporterBin + " reporter --home " + fleet.ShellQuote(reporterHome) + " --votes-dir " + fleet.ShellQuote(constants.GlobalTorVotesDir) + " --interval " + passInterval + " --rpc tcp://127.0.0.1:31001"
		// timeout ends the reporter after the budget (exit 124); that is the expected end.
		out := c.Run(t, n, runBudget+time.Minute, cmd)
		if out.Exit != 124 {
			t.Fatalf("the reporter stopped on its own (exit %d), it should run until timeout ends it:\n%s%s", out.Exit, out.Stdout, out.Stderr)
		}
		if strings.Contains(out.Stderr, "reporter pass failed") {
			t.Errorf("a pass failed:\n%s", out.Stderr)
		}
	}
	st := readJSON[reporterState](t, c, n, "state.json")
	if st.Reported == 0 || st.Reported >= st.Seen {
		t.Errorf("state = %+v: an epoch should be reported and the epoch in progress is after it", st)
	}
	mon := readJSON[reporterMonitor](t, c, n, "monitor.json")
	if mon.Epoch != st.Reported || mon.Chunks < 1 {
		t.Errorf("monitor = %+v, state = %+v", mon, st)
	}
}

// skipUnlessReportable says the test does not apply when the chain cannot take
// this reporter's report whatever the reporter does (reportBlockers): waiting
// for a reported epoch would only run out the budget. When nothing blocks, the
// wait stays and a reporter that reports nothing fails it.
func skipUnlessReportable(t *testing.T, c *chain.Chain, n fleet.Node) {
	t.Helper()
	network, err := tornet.ParseNetwork(c.F.ReadFile(t, n, constants.GlobalStateRoot+"/"+constants.GlobalTorAuthoritiesFile))
	if err != nil {
		t.Fatalf("%s: the network file the install kept: %v", n.Name, err)
	}
	address := strings.TrimSpace(c.F.MustExec(t, n, "sudo -u "+reporterAccount+" "+reporterBin+" reporter --print-address --home "+fleet.ShellQuote(reporterHome)).Stdout)
	blockers := reportBlockers(address, c.RelayReporters(t, n), c.EpochDuration(t, n), time.Duration(network.VotingIntervalMinutes)*time.Minute)
	if len(blockers) > 0 {
		harness.SkipNotApplicable(t, "this chain cannot take the report, whatever the reporter does: "+strings.Join(blockers, "; and "))
	}
}

// readJSON decodes a file of the reporter's home.
func readJSON[T any](t *testing.T, c *chain.Chain, n fleet.Node, name string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(c.F.MustExec(t, n, "sudo cat "+fleet.ShellQuote(reporterHome+"/"+name)).Stdout), &v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}
