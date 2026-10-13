package stages

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
)

func lookup(vars map[string]string) EnvLookup {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

var soakManifests = []manifest.Manifest{
	{ID: "plain", Stage: 1},
	{ID: "soak", Stage: 1, Destructive: true, OptIn: "E2E_CHAIN_SOAK"},
}

func TestPlanWith_optInFeatureIsLeftOutWithoutItsVariable(t *testing.T) {
	for name, env := range map[string]EnvLookup{
		"unset": lookup(nil),
		"empty": lookup(map[string]string{"E2E_CHAIN_SOAK": ""}),
	} {
		t.Run(name, func(t *testing.T) {
			steps, err := PlanWith(testStages(), soakManifests, env)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(steps[0].Features(), ","); got != "plain" {
				t.Fatalf("planned %q, want only plain", got)
			}
			if len(steps[0].Extra) != 0 {
				t.Fatalf("extra budget %v with the feature left out", steps[0].Extra)
			}
		})
	}
}

func TestPlan_isPlanWithNoEnvironment(t *testing.T) {
	steps, err := Plan(testStages(), soakManifests)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(steps[0].Features(), ","); got != "plain" {
		t.Fatalf("Plan planned %q", got)
	}
}

func TestPlanWith_optInFeatureGetsItsDurationOnTopOfTheStage(t *testing.T) {
	steps, err := PlanWith(testStages(), soakManifests, lookup(map[string]string{"E2E_CHAIN_SOAK": "24h"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(steps[0].Features(), ","); got != "plain,soak" {
		t.Fatalf("planned %q", got)
	}
	want := steps[0].Stage.Timeout + Duration(24*time.Hour)
	if got := steps[0].Budget("soak"); got != want {
		t.Fatalf("soak budget %s, want %s", time.Duration(got), time.Duration(want))
	}
	if got := steps[0].Budget("plain"); got != steps[0].Stage.Timeout {
		t.Fatalf("a package that did not opt in keeps the stage's timeout, got %s", time.Duration(got))
	}
}

func TestPlanWith_aValueThatIsNoDurationIsRefused(t *testing.T) {
	for _, bad := range []string{"24", "soon", "-3h", "0s"} {
		_, err := PlanWith(testStages(), soakManifests, lookup(map[string]string{"E2E_CHAIN_SOAK": bad}))
		if err == nil || !strings.Contains(err.Error(), "E2E_CHAIN_SOAK") {
			t.Errorf("%q accepted or the error does not name the variable: %v", bad, err)
		}
	}
}

func TestWorstCase_includesTheOptInDuration(t *testing.T) {
	without, err := PlanWith(testStages(), soakManifests, lookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	with, err := PlanWith(testStages(), soakManifests, lookup(map[string]string{"E2E_CHAIN_SOAK": "48h"}))
	if err != nil {
		t.Fatal(err)
	}
	stage := time.Duration(with[0].Stage.Timeout)
	// The soak adds one destructive package: a stage timeout, the stop grace and its own 48 hours.
	if got, want := WorstCase(with)-WorstCase(without), stage+StopGrace+48*time.Hour; got != want {
		t.Fatalf("the soak adds %s to the worst case, want %s", got, want)
	}
}

func TestRun_optInPackageRunsWithItsOwnBudget(t *testing.T) {
	steps, err := PlanWith(testStages(), soakManifests, lookup(map[string]string{"E2E_CHAIN_SOAK": "6h"}))
	if err != nil {
		t.Fatal(err)
	}
	ex := &fakeExec{}
	if _, err := newRunner(t, ex).Run(context.Background(), steps, Options{}); err != nil {
		t.Fatal(err)
	}
	budgets := map[string]time.Duration{}
	for i, call := range ex.calls {
		budgets[call[len(call)-1]] = ex.budgets[i]
	}
	stage := time.Duration(steps[0].Stage.Timeout)
	if got, want := budgets["./features/soak"], stage+6*time.Hour; got != want {
		t.Fatalf("soak budget %s, want %s (calls %v)", got, want, ex.calls)
	}
	if got := budgets["./features/plain"]; got != stage {
		t.Fatalf("plain budget %s", got)
	}
	args := strings.Join(ex.calls[1], " ")
	if !strings.Contains(args, "-timeout "+(stage+6*time.Hour+StopGrace).String()) {
		t.Fatalf("the test binary's own timeout is not past the soak's budget: %s", args)
	}
}
