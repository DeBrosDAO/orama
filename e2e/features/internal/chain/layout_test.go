//go:build e2e_fleet

package chain

import (
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
