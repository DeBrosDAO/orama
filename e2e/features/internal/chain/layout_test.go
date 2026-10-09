//go:build e2e_fleet

package chain

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

func chainFor(target string) *Chain {
	return &Chain{F: &fleet.Fleet{State: &fleet.State{Target: target}}}
}

func TestChainHost_perTarget(t *testing.T) {
	fleetRun, stagenet := chainFor(config.TargetFleet), chainFor(config.TargetStagenet)
	if fleetRun.RPC() != "tcp://127.0.0.1:31001" || fleetRun.RPCHTTP() != "http://127.0.0.1:31001" {
		t.Errorf("fleet RPC %s %s", fleetRun.RPC(), fleetRun.RPCHTTP())
	}
	if stagenet.RPC() != "tcp://198.18.0.2:31001" || stagenet.RPCHTTP() != "http://198.18.0.2:31001" {
		t.Errorf("stagenet RPC %s %s", stagenet.RPC(), stagenet.RPCHTTP())
	}
}

func TestScripts_useTheTargetsRPC(t *testing.T) {
	for target, want := range map[string]string{config.TargetFleet: "tcp://127.0.0.1:31001", config.TargetStagenet: "tcp://198.18.0.2:31001"} {
		c := chainFor(target)
		for name, script := range map[string]string{
			"fee":       c.feeScript(TxOptions{}),
			"broadcast": c.broadcastScript(),
			"account":   c.accountScript(Key{Address: "orama1x"}),
		} {
			if !strings.Contains(script, want) {
				t.Errorf("%s: the %s script does not query %s:\n%s", target, name, want, script)
			}
		}
	}
}

func TestCheckChainID_perTarget(t *testing.T) {
	cases := []struct {
		target, id string
		ok         bool
	}{
		{config.TargetFleet, "orama-devnet-e2e-ab12", true},
		{config.TargetFleet, "orama-stagenet-4", false},
		{config.TargetFleet, "orama-testnet-1", false},
		{config.TargetStagenet, "orama-stagenet-4", true},
		{config.TargetStagenet, "orama-devnet-e2e-ab12", false},
		{config.TargetStagenet, "", false},
	}
	for _, c := range cases {
		err := checkChainID(&fleet.State{Target: c.target}, c.id)
		if (err == nil) != c.ok {
			t.Errorf("checkChainID(%q, %q) = %v, want ok=%v", c.target, c.id, err, c.ok)
		}
	}
}

// TestOramadCmd_perTarget: stagenet's host ruleset drops the chain user's
// connections to the chain's RPC through the veth, so a stagenet command runs
// inside the netns; a fleet validator listens on loopback and needs no netns.
func TestOramadCmd_perTarget(t *testing.T) {
	fleetCmd := chainFor(config.TargetFleet).OramadCmd("query", "fees", "base-fee")
	if want := "sudo -u orama-chain /usr/lib/orama-global/bin/oramad 'query' 'fees' 'base-fee' --home /var/lib/orama-global/chain"; fleetCmd != want {
		t.Errorf("fleet command\n got %s\nwant %s", fleetCmd, want)
	}
	stagenetCmd := chainFor(config.TargetStagenet).OramadCmd("query", "fees", "base-fee")
	if want := "sudo ip netns exec orama-global runuser -u orama-chain -- /usr/lib/orama-global/bin/oramad 'query' 'fees' 'base-fee' --home /var/lib/orama-global/chain"; stagenetCmd != want {
		t.Errorf("stagenet command\n got %s\nwant %s", stagenetCmd, want)
	}
}

func TestOramadCmd_noArgumentsStillNamesTheHome(t *testing.T) {
	if got := chainFor(config.TargetStagenet).OramadCmd(); !strings.HasSuffix(got, "oramad --home /var/lib/orama-global/chain") {
		t.Errorf("got %s", got)
	}
}

// skips runs fn in a subtest and reports whether it was skipped and failed.
func skips(t *testing.T, fn func(t testing.TB)) (skipped, failed bool) {
	t.Helper()
	t.Run("inner", func(t *testing.T) {
		defer func() { skipped, failed = t.Skipped(), t.Failed() }()
		fn(t)
	})
	return skipped, failed
}

func TestRequireFresh_skipsOnStagenetOnly(t *testing.T) {
	skipped, failed := skips(t, func(t testing.TB) { requireFresh(t, &fleet.State{Target: config.TargetStagenet}) })
	if !skipped || failed {
		t.Errorf("stagenet: skipped=%v failed=%v, want a skip", skipped, failed)
	}
	skipped, failed = skips(t, func(t testing.TB) { requireFresh(t, &fleet.State{Target: config.TargetFleet}) })
	if skipped || failed {
		t.Errorf("fleet: skipped=%v failed=%v, want to run on", skipped, failed)
	}
}

func TestValidator_skipsOnStagenetBeforeAnyKeyringRead(t *testing.T) {
	skipped, failed := skips(t, func(t testing.TB) { chainFor(config.TargetStagenet).Validator(t, fleet.Node{Name: "node-1"}) })
	if !skipped || failed {
		t.Errorf("skipped=%v failed=%v, want a skip", skipped, failed)
	}
}

func TestBrokenOf_queryFailedCarriesTheError(t *testing.T) {
	body := queryFailedMark + "\nfailed to query storage invariants: rpc error: code = Unknown desc = {ValuePerByte}: panic\n"
	got := brokenOf(t, fleet.Node{Name: "node-1"}, "storage", body)
	if len(got) != 1 || !strings.Contains(got[0], "query_failed") || !strings.Contains(got[0], "{ValuePerByte}: panic") {
		t.Fatalf("brokenOf = %v, want one query_failed entry carrying the error text", got)
	}
}

func TestBrokenOf_answerWithoutFailureMark(t *testing.T) {
	if got := brokenOf(t, fleet.Node{Name: "node-1"}, "fees", `{"fees_balance":true}`); len(got) != 0 {
		t.Fatalf("brokenOf = %v, want none", got)
	}
	if got := brokenOf(t, fleet.Node{Name: "node-1"}, "fees", `{"fees_balance":false,"detail":"d"}`); len(got) != 1 {
		t.Fatalf("brokenOf = %v, want the false member", got)
	}
}

func TestInvariantScript_failedQueryLeavesTheErrorLine(t *testing.T) {
	c := chainFor(config.TargetFleet)
	script := c.invariantScript("storage")
	// Swap the real command for one that fails with an error line on stderr, run the rest as written.
	cmd := c.OramadCmd("query", "storage", "invariants", "--node", c.RPC(), "--output", "json")
	script = strings.Replace(script, cmd, "{ echo usage >&2; echo 'boom: out of gas' >&2; false; }", 1)
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := invariantMark + "storage\n" + queryFailedMark + "\nboom: out of gas\n"
	if string(out) != want {
		t.Fatalf("script output %q, want %q", out, want)
	}
}

// The answer oramad printed on stagenet after the faucet had dripped 1216 ORAMA: its mints are in the
// epoch state and in the supply identity.
func TestEpochState_expectedSupplyCountsTheFaucet(t *testing.T) {
	const answer = `{"epoch_state":{"current_epoch":"46","epoch_start_unix_nano":"1791567798480204787","blocks_in_epoch":"50",
"cumulative_minted":"400896000000000","cumulative_burned":"467279948","genesis_supply":"0","cumulative_development_minted":"7",
"cumulative_service_minted":"11","validator_split_delta":"0","cumulative_faucet_minted":"1216000000000","faucet_epoch_minted":"0"}}`
	var r struct {
		EpochState EpochState `json:"epoch_state"`
	}
	if err := json.Unmarshal([]byte(answer), &r); err != nil {
		t.Fatal(err)
	}
	if got, want := r.EpochState.ExpectedSupply().String(), "402111532720070"; got != want {
		t.Errorf("expected supply %s, want %s (minted + development + service + faucet - burned)", got, want)
	}
	if got := (EpochState{}).ExpectedSupply(); !got.IsZero() {
		t.Errorf("expected supply of an empty state %s, want 0", got.String())
	}
}
