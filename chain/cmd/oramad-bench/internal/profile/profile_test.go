package profile

import (
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/gen"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/report"
)

// A step the report would refuse to judge is a step that proves nothing: every step of the full
// profile, which exists to find saturation, must be long enough to be judged at a block interval of
// up to 1.5 s (the 1 s timeout_commit the bench uses, plus the time to execute a block).
func TestFullProfileStepsAreLongEnoughToBeJudged(t *testing.T) {
	const blockInterval = 1.5
	for _, tp := range Full().Types {
		for _, s := range tp.Steps {
			if s.Duration.Seconds()/blockInterval < report.MinIntervalsToJudge {
				t.Errorf("%s: a %s step holds only %.0f block intervals", tp.Type, s.Duration, s.Duration.Seconds()/blockInterval)
			}
		}
	}
}

func TestBuiltInProfilesAreValidAndNameRealTypes(t *testing.T) {
	known := map[string]bool{}
	for _, n := range gen.Names() {
		known[n] = true
	}
	for _, name := range []string{"smoke", "full"} {
		p, err := ByName(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, tp := range p.Types {
			if !known[tp.Type] {
				t.Errorf("%s runs %q, which is not a transaction type", name, tp.Type)
			}
		}
	}
	if _, err := ByName("nightly"); err == nil {
		t.Fatal("an unknown profile was accepted")
	}
}

func TestEveryGeneratorIsInTheFullProfile(t *testing.T) {
	in := map[string]bool{}
	for _, tp := range Full().Types {
		in[tp.Type] = true
	}
	for _, n := range gen.Names() {
		if !in[n] {
			t.Errorf("transaction type %s is not in the full profile", n)
		}
	}
}

func TestStepTxs(t *testing.T) {
	cases := []struct {
		step Step
		want int
	}{
		{open(20, 10*time.Second), 200},
		{open(0.5, 15*time.Second), 8},
		{closed(16, 10*time.Second), 160},
		{closed(1024, 90*time.Second), MaxClosedTxs},
	}
	for _, c := range cases {
		if got := c.step.Txs(); got != c.want {
			t.Errorf("%+v: %d txs, want %d", c.step, got, c.want)
		}
	}
}

func TestStepValidate(t *testing.T) {
	bad := []Step{
		{Mode: report.ModeOpen, Rate: 0, Duration: time.Second},
		{Mode: report.ModeOpen, Rate: -1, Duration: time.Second},
		{Mode: report.ModeClosed, Window: 0, Duration: time.Second},
		{Mode: report.ModeOpen, Rate: 1},
		{Mode: "burst", Rate: 1, Duration: time.Second},
	}
	for _, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("%+v accepted", s)
		}
	}
	if err := open(1, time.Second).Validate(); err != nil {
		t.Error(err)
	}
}

func TestProfileValidate(t *testing.T) {
	if err := (Profile{Name: "x", Accounts: 1, Types: Smoke().Types}).Validate(); err == nil {
		t.Error("one account accepted")
	}
	if err := (Profile{Name: "x", Accounts: 4}).Validate(); err == nil {
		t.Error("no types accepted")
	}
	if err := (Profile{Name: "x", Accounts: 4, Types: []TypePlan{{Type: gen.BankSend}}}).Validate(); err == nil {
		t.Error("a type with no steps accepted")
	}
}

func TestOnly(t *testing.T) {
	p, err := Smoke().Only([]string{gen.MarketList, gen.BankSend})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Types) != 2 || p.Types[0].Type != gen.BankSend {
		t.Fatalf("types %+v: the profile's order must be kept", p.Types)
	}
	if _, err := Smoke().Only([]string{"bank-sned"}); err == nil {
		t.Error("a misspelt type was accepted")
	}
	all, err := Smoke().Only(nil)
	if err != nil || len(all.Types) != len(Smoke().Types) {
		t.Fatalf("no names must keep every type: %v", err)
	}
}

func TestShieldedPlanCountsTheProfilesShieldedTypes(t *testing.T) {
	plan := Smoke().ShieldedPlan(16)
	// transfer-2: 1 tx/s for 6s = 6 (+1 spare); transfer-4: 3 (+1); transfer-8: 1 (+1).
	if plan.Transfers[2] != 7 || plan.Transfers[4] != 4 || plan.Transfers[8] != 2 || plan.Transfers[16] != 0 {
		t.Fatalf("transfers %v", plan.Transfers)
	}
	if plan.Shields != 5 || plan.Unshields != 5 || plan.Signers != 16 {
		t.Fatalf("shields %d unshields %d signers %d", plan.Shields, plan.Unshields, plan.Signers)
	}
	none, _ := Smoke().Only([]string{gen.BankSend})
	if p := none.ShieldedPlan(4); p.SeedNotes() != 0 || p.Shields != 0 {
		t.Fatalf("a profile with no shielded type planned shielded bundles: %+v", p)
	}
}
