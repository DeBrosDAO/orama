package config

import (
	"strings"
	"testing"
)

func goodPins() StagenetPins {
	return StagenetPins{RunID: "stagenet-20260930-101500", Env: StagenetEnv, BaseDomain: StagenetBaseDomain, OperatorNamespace: StagenetOperatorNamespace,
		GatewayURL: StagenetGatewayURL, ChainID: "orama-stagenet-4", NodeIPs: StagenetIPs()}
}

func TestCheckStagenet_acceptsThePinnedValues(t *testing.T) {
	if err := CheckStagenet(goodPins()); err != nil {
		t.Fatal(err)
	}
}

func TestCheckStagenet_reportsEveryMismatch(t *testing.T) {
	p := goodPins()
	p.Env, p.ChainID, p.NodeIPs = "devnet", "orama-devnet-1", nil
	err := CheckStagenet(p)
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"environment", "chain id", "public addresses"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q: %v", want, err)
		}
	}
}

func TestCheckStagenetChainID_shape(t *testing.T) {
	for id, ok := range map[string]bool{
		"orama-stagenet-4": true, "orama-stagenet-12": true, "orama-stagenet-": false, "orama-stagenet-4x": false,
		"xorama-stagenet-4": false, "orama-devnet-4": false, "": false, "orama-stagenet-4\n": false,
	} {
		if err := CheckStagenetChainID(id); (err == nil) != ok {
			t.Errorf("CheckStagenetChainID(%q) = %v, want ok=%v", id, err, ok)
		}
	}
}

func TestCheckTarget(t *testing.T) {
	for target, ok := range map[string]bool{TargetFleet: true, TargetStagenet: true, "devnet": false, "testnet": false, "Stagenet": false} {
		if err := CheckTarget(target); (err == nil) != ok {
			t.Errorf("CheckTarget(%q) = %v, want ok=%v", target, err, ok)
		}
	}
}

func TestStagenetPins_neverNameASharedEnvironment(t *testing.T) {
	for _, v := range []string{StagenetEnv, StagenetBaseDomain, StagenetGatewayURL} {
		if strings.Contains(v, "devnet") || strings.Contains(v, "testnet") {
			t.Errorf("%q names a shared environment", v)
		}
	}
	if len(StagenetNodes) != 5 || len(StagenetIPs()) != 5 {
		t.Fatalf("want 5 stagenet nodes, have %d", len(StagenetNodes))
	}
}
