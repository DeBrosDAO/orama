package nodeedit

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

func gb(n uint64) *uint64 { return &n }
func flag(b bool) *bool   { return &b }

var fullNode = NodeState{Global: true, IPFS: true, Relay: true, StorageMax: "55GB"}

func TestBuildPlan_storageDeclaresOnTheChainAndResizesTheNode(t *testing.T) {
	p, err := BuildPlan("10.0.0.1", fullNode, Settings{StorageGB: gb(100)}, "node-1", false)

	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if p.Storage == nil || *p.Storage != 100 || !p.DeclareOnChain || p.Exit != nil || p.Nothing() {
		t.Fatalf("plan = %+v", p)
	}
	var out bytes.Buffer
	p.Render(&out)
	for _, want := range []string{"10.0.0.1", "declare 100 GB", "node-1", "MsgDeclareCapacity", "StorageMax 55GB"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the plan lacks %q:\n%s", want, &out)
		}
	}
}

func TestBuildPlan_noChainResizesTheNodeOnly(t *testing.T) {
	p, err := BuildPlan("10.0.0.1", fullNode, Settings{StorageGB: gb(100)}, "", true)

	if err != nil || p.DeclareOnChain || p.Storage == nil {
		t.Fatalf("plan = %+v, err = %v", p, err)
	}
}

func TestBuildPlan_storageNeedsAChainDecision(t *testing.T) {
	_, err := BuildPlan("10.0.0.1", fullNode, Settings{StorageGB: gb(100)}, "", false)

	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "--chain-node-id") || !strings.Contains(err.Error(), "--no-chain") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildPlan_storageAlreadyTheSizeIsNoChange(t *testing.T) {
	// 50 GB declared is a 55GB StorageMax: capacity plus ten percent.
	p, err := BuildPlan("10.0.0.1", fullNode, Settings{StorageGB: gb(50)}, "node-1", false)

	if err != nil || !p.Nothing() || !strings.Contains(p.Changes[0].Description, "already sized for 50 GB") {
		t.Fatalf("plan = %+v, err = %v", p, err)
	}
}

func TestBuildPlan_storageZeroIsRefused(t *testing.T) {
	_, err := BuildPlan("10.0.0.1", fullNode, Settings{StorageGB: gb(0)}, "node-1", false)

	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "orama remove") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildPlan_storageOnANodeWithoutKubo(t *testing.T) {
	_, err := BuildPlan("10.0.0.1", NodeState{Relay: true}, Settings{StorageGB: gb(10)}, "n", false)

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "no public storage") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildPlan_exitSwitchesTheRelay(t *testing.T) {
	p, err := BuildPlan("10.0.0.1", fullNode, Settings{Exit: flag(true)}, "", false)

	if err != nil || p.Exit == nil || !*p.Exit || p.Storage != nil || p.DeclareOnChain {
		t.Fatalf("plan = %+v, err = %v", p, err)
	}
	var out bytes.Buffer
	p.Render(&out)
	if !strings.Contains(out.String(), "an exit") || !strings.Contains(out.String(), "on-chain roles are not changed here") {
		t.Errorf("the plan does not say what it leaves alone:\n%s", &out)
	}
}

func TestBuildPlan_exitAlreadyInTheRoleIsNoChange(t *testing.T) {
	p, err := BuildPlan("10.0.0.1", NodeState{Relay: true, Exit: true}, Settings{Exit: flag(true)}, "", false)

	if err != nil || !p.Nothing() {
		t.Fatalf("plan = %+v, err = %v", p, err)
	}
}

func TestBuildPlan_exitWithoutARelay(t *testing.T) {
	_, err := BuildPlan("10.0.0.1", NodeState{Global: true, IPFS: true}, Settings{Exit: flag(false)}, "", false)

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "no Tor relay") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildPlan_turningTheGlobalLayerOffIsRefusedWithTheWayToTakeTheNodeOut(t *testing.T) {
	_, err := BuildPlan("10.0.0.1", fullNode, Settings{Global: flag(false)}, "", false)

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "orama remove --node 10.0.0.1") || !strings.Contains(err.Error(), "consensus key") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildPlan_turningTheGlobalLayerOnIsRefusedWithWhatDoesIt(t *testing.T) {
	_, err := BuildPlan("10.0.0.1", NodeState{}, Settings{Global: flag(true)}, "", false)

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "setup flow") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildPlan_globalAlreadyAsAskedIsNoChange(t *testing.T) {
	for name, tc := range map[string]struct {
		st NodeState
		on bool
	}{"on": {fullNode, true}, "off": {NodeState{}, false}} {
		p, err := BuildPlan("10.0.0.1", tc.st, Settings{Global: flag(tc.on)}, "", false)
		if err != nil || !p.Nothing() {
			t.Errorf("%s: plan = %+v, err = %v", name, p, err)
		}
	}
}

func TestBuildPlan_aRefusedGlobalChangeStopsTheOtherChanges(t *testing.T) {
	_, err := BuildPlan("10.0.0.1", fullNode, Settings{Global: flag(false), StorageGB: gb(100), Exit: flag(true)}, "n", false)

	if err == nil {
		t.Fatal("a plan with a refused change must be refused whole")
	}
}

func TestBuildPlan_nothingAskedIsUsage(t *testing.T) {
	_, err := BuildPlan("10.0.0.1", fullNode, Settings{}, "", false)

	if clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("err = %v", err)
	}
}
