//go:build e2e_fleet

package relayreporter

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	// reporterBin is where `orama global install` puts orama-global.
	reporterBin = "/usr/lib/orama-global/bin/orama-global"
	// envHome is a reporter home on the chain node, prepared by the tester: a
	// funded hot-key whose address is in x/relay's reporter set, operator,
	// authority-id and votes/*.vote of a directory authority.
	envHome = "E2E_REPORTER_HOME"
	// runBudget covers more than one epoch of the run chain (E2E_EPOCH_DURATION).
	runMinutes = 8
	runBudget  = runMinutes * time.Minute
	// passInterval is the reporter's pass interval for the run.
	passInterval = "5s"
)

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

// A reporter with no identity does not start: no operator file, no report.
func TestReporter_refusesToStartWithoutItsIdentity(t *testing.T) {
	c, n := node(t)
	out := c.F.Exec(t, n, `d=$(mktemp -d) && `+reporterBin+` reporter --home "$d" --rpc tcp://127.0.0.1:31001; code=$?; rm -rf "$d"; exit $code`)
	infra.ExpectNodeExit(t, "orama-global reporter without an identity", out, infra.ExitFailure, "operator", "must hold")
}

// The authority-id the reporter reads is the v3 identity the network file
// lists for an authority: one file names the authorities for the Tor roles,
// the clients and the reporter. The test reads the file from
// ORAMA_ONION_NETWORK (as every client does) and the reporter home from
// E2E_REPORTER_HOME.
func TestReporter_authorityIDIsOneOfTheNetworkFilesAuthorities(t *testing.T) {
	file := os.Getenv(tornet.NetworkEnv)
	home := os.Getenv(envHome)
	if file == "" || home == "" {
		harness.SkipNotApplicable(t, tornet.NetworkEnv+" and "+envHome+" must name the network file and a reporter home: the run has no directory authority to report for")
	}
	network, err := tornet.Load(file)
	if err != nil {
		t.Fatalf("the network file %s: %v", file, err)
	}
	c, n := node(t)
	id := strings.ToUpper(strings.TrimSpace(c.F.MustExec(t, n, "sudo cat "+fleet.ShellQuote(home+"/authority-id")).Stdout))
	for _, a := range network.Authorities {
		if a.V3Ident == id {
			return
		}
	}
	t.Fatalf("%s/authority-id is %s, which no authority of %s lists as its v3_ident", home, id, file)
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

// The reporter runs across an epoch close and reports the epoch that closed.
func TestReporter_reportsAClosedEpoch(t *testing.T) {
	home := os.Getenv(envHome)
	if home == "" {
		harness.SkipNotApplicable(t, envHome+" names no reporter home: the run has no directory authority votes to report from (track E2 builds the dirauth)")
	}
	c, n := node(t)
	cmd := "sudo timeout " + strconv.Itoa(runMinutes) + "m " + reporterBin + " reporter --home " + fleet.ShellQuote(home) + " --interval " + passInterval + " --rpc tcp://127.0.0.1:31001"
	// timeout ends the reporter after the budget (exit 124); that is the expected end.
	out := c.Run(t, n, runBudget+time.Minute, cmd)
	if out.Exit != 124 {
		t.Fatalf("the reporter stopped on its own (exit %d), it should run until timeout ends it:\n%s%s", out.Exit, out.Stdout, out.Stderr)
	}
	if strings.Contains(out.Stderr, "reporter pass failed") {
		t.Errorf("a pass failed:\n%s", out.Stderr)
	}
	var st reporterState
	if err := json.Unmarshal([]byte(c.F.MustExec(t, n, "sudo cat "+fleet.ShellQuote(home+"/state.json")).Stdout), &st); err != nil {
		t.Fatalf("state.json: %v", err)
	}
	if st.Reported == 0 || st.Reported >= st.Seen {
		t.Errorf("state = %+v: an epoch should be reported and the epoch in progress is after it", st)
	}
	var mon reporterMonitor
	if err := json.Unmarshal([]byte(c.F.MustExec(t, n, "sudo cat "+fleet.ShellQuote(home+"/monitor.json")).Stdout), &mon); err != nil {
		t.Fatalf("monitor.json: %v", err)
	}
	if mon.Epoch != st.Reported || mon.Chunks < 1 {
		t.Errorf("monitor = %+v, state = %+v", mon, st)
	}
}
