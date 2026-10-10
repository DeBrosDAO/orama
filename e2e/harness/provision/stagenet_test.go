package provision

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Every operation that creates, changes or destroys servers refuses the
// stagenet target before it reads a credential or reaches a broker.
func TestOperations_refuseTheStagenetTarget(t *testing.T) {
	st := &fleet.State{Target: config.TargetStagenet, RunID: "stagenet-20260930-101500"}
	ctx := context.Background()
	ops := map[string]func() error{
		"AddExtra":             func() error { _, err := AddExtra(ctx, st, "extra-1", "hel1"); return err },
		"RemoveExtra":          func() error { return RemoveExtra(ctx, st, "extra-1") },
		"AddEvalCluster":       func() error { _, err := AddEvalCluster(ctx, st, "eval1"); return err },
		"RemoveEvalCluster":    func() error { return RemoveEvalCluster(ctx, st, "eval1") },
		"LiveExtras":           func() error { _, err := LiveExtras(ctx, st); return err },
		"Down":                 func() error { return Down(ctx, st, &testLogger{}) },
		"DestroyNode":          func() error { return DestroyNode(ctx, st, "57.129.166.16") },
		"BreakUpgrade":         func() error { return BreakUpgrade(ctx, st, "57.129.166.16") },
		"RestoreUpgrade":       func() error { return RestoreUpgrade(ctx, st, "57.129.166.16") },
		"UpgradeToHead":        func() error { return UpgradeToHead(ctx, st, &testLogger{}) },
		"Direct.AddExtra":      func() error { _, err := Direct{}.AddExtra(ctx, st, "extra-1", "hel1"); return err },
		"Direct.RemoveExtra":   func() error { return Direct{}.RemoveExtra(ctx, st, "extra-1") },
		"Direct.AddCluster":    func() error { _, err := Direct{}.AddCluster(ctx, st, "eval1"); return err },
		"Direct.RemoveCluster": func() error { return Direct{}.RemoveCluster(ctx, st, "eval1") },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			err := op()
			if err == nil || !strings.Contains(err.Error(), "not available on the stagenet target") || !strings.Contains(err.Error(), name) {
				t.Fatalf("err %v, want a stagenet refusal naming %s", err, name)
			}
		})
	}
}

func TestRefuseStagenet_otherTargetsPass(t *testing.T) {
	for _, st := range []*fleet.State{nil, {}, {Target: config.TargetFleet}} {
		if err := refuseStagenet("Down", st); err != nil {
			t.Errorf("refused %+v: %v", st, err)
		}
	}
}
