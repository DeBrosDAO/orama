package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func goodPins() StagenetPins {
	return StagenetPins{RunID: "stagenet-20260930-101500", Env: StagenetEnv, BaseDomain: StagenetBaseDomain, OperatorNamespace: StagenetOperatorNamespace,
		GatewayURL: StagenetGatewayURL, ChainID: "orama-stagenet-4", NodeIPs: []string{"57.128.226.141", "37.59.116.212", "141.227.165.168"}}
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
	for _, v := range []string{StagenetEnv, StagenetBaseDomain, StagenetGatewayURL, StagenetDefaultChainID} {
		if strings.Contains(v, "devnet") || strings.Contains(v, "testnet") {
			t.Errorf("%q names a shared environment", v)
		}
	}
	if len(StagenetNodes) != 3 || len(StagenetIPs()) != 3 {
		t.Fatalf("want 3 stagenet nodes, have %d", len(StagenetNodes))
	}
}

// TestStagenetDefaultChainID_isWhatTheDeployScriptDeploys: the target's default
// chain id drifted from the one deploy.sh deploys, and every chain-id assertion
// of a stagenet run then failed against the real chain.
func TestStagenetDefaultChainID_isWhatTheDeployScriptDeploys(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "chain", "scripts", "stagenet", "deploy.sh"))
	if err != nil {
		t.Fatalf("read the stagenet deploy script: %v", err)
	}
	m := regexp.MustCompile(`(?m)^CHAIN_ID="\$\{CHAIN_ID:-([a-z0-9-]+)\}"$`).FindSubmatch(script)
	if m == nil {
		t.Fatal(`deploy.sh no longer sets CHAIN_ID="${CHAIN_ID:-<default>}"`)
	}
	if got := string(m[1]); got != StagenetDefaultChainID {
		t.Errorf("deploy.sh deploys %q by default, the target writes %q", got, StagenetDefaultChainID)
	}
}
