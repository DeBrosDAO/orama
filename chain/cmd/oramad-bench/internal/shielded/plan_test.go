package shielded

import (
	"fmt"
	"strings"
	"testing"

	"cosmossdk.io/math"
)

func testParams() Params {
	return Params{
		ActionGas: 250_000, NullifierFee: math.NewInt(1_000_000), MaxActions: 16,
		MaxFeeTopup: math.NewInt(10_000_000), AnchorWindow: 14_400,
	}
}

func TestTransferFee(t *testing.T) {
	// 2 actions at 32 norama per gas: 32 * 2 * 250000 = 16,000,000 base + 2 * 1,000,000 + 1.
	got := TransferFee(math.NewInt(32), testParams(), 2)
	if !got.Equal(math.NewInt(18_000_001)) {
		t.Fatalf("fee %s", got)
	}
	if !TransferFee(math.NewInt(1), testParams(), 16).GT(TransferFee(math.NewInt(1), testParams(), 2)) {
		t.Fatal("a bigger transfer must cost more")
	}
}

func TestPlanValidate(t *testing.T) {
	p := testParams()
	good := Plan{Shields: 2, Transfers: map[int]int{2: 3, 16: 1}, Unshields: 2, Signers: 4}
	if err := good.Validate(p); err != nil {
		t.Fatal(err)
	}
	bad := map[string]Plan{
		"no signers":         {Transfers: map[int]int{2: 1}},
		"one action":         {Signers: 1, Transfers: map[int]int{1: 1}},
		"too many actions":   {Signers: 1, Transfers: map[int]int{17: 1}},
		"negative transfers": {Signers: 1, Transfers: map[int]int{2: -1}},
		"negative shield":    {Signers: 1, Shields: -1},
		"negative unshield":  {Signers: 1, Unshields: -1},
	}
	for name, plan := range bad {
		if err := plan.Validate(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	tight := p
	tight.MaxFeeTopup = math.NewInt(1_000_000)
	if err := (Plan{Signers: 1, Unshields: 1}).Validate(tight); err == nil {
		t.Error("an unshield above the fee top-up cap was accepted")
	}
	dear := p
	dear.NullifierFee = math.NewInt(3_000_000)
	if err := (Plan{Signers: 1, Unshields: 1}).Validate(dear); err == nil {
		t.Error("an unshield that does not cover its nullifier fees was accepted")
	}
	if err := (Plan{Signers: 1}).Validate(tight); err != nil {
		t.Errorf("a plan with no unshields must not depend on the fee top-up cap: %v", err)
	}
}

func TestSeedNotesAndJobsAgree(t *testing.T) {
	plan := Plan{Transfers: map[int]int{2: 3, 4: 2, 16: 1}, Unshields: 5, Signers: 3}
	want := 3*2 + 2*4 + 16 + 5
	if got := plan.SeedNotes(); got != want {
		t.Fatalf("SeedNotes = %d, want %d", got, want)
	}
	notes := 0
	for _, j := range plan.SeedJobs() {
		if len(j.Outputs) == 0 || len(j.Outputs) > SeedOutputsPerBundle {
			t.Fatalf("seed job %s has %d outputs", j.ID, len(j.Outputs))
		}
		notes += len(j.Outputs)
	}
	if notes != want {
		t.Fatalf("seed jobs create %d notes, the spends need %d", notes, want)
	}
}

// pools builds what the factory would return for plan.SeedJobs: each wallet's notes.
func poolsFor(plan Plan) map[uint32][]string {
	pools := map[uint32][]string{}
	for _, j := range plan.SeedJobs() {
		for i, o := range j.Outputs {
			pools[o.Account] = append(pools[o.Account], fmt.Sprintf("note-%s-%d", j.ID, i))
		}
	}
	return pools
}

func signer(i int) (string, error) { return fmt.Sprintf("%040x", i), nil }

func TestSpendJobs_everySeedNoteIsSpentOnceByItsOwner(t *testing.T) {
	plan := Plan{Transfers: map[int]int{2: 3, 4: 2}, Unshields: 4, Signers: 2}
	pools := poolsFor(plan)
	jobs, err := plan.SpendJobs(pools, math.NewInt(32), testParams(), signer)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3+2+4 {
		t.Fatalf("%d jobs", len(jobs))
	}
	seen := map[string]bool{}
	for _, j := range jobs {
		for _, s := range j.Spends {
			if seen[s.Note] {
				t.Fatalf("note %s is spent twice", s.Note)
			}
			seen[s.Note] = true
			if !strings.Contains(s.Note, "note-seed-") {
				t.Fatalf("job %s spends %q", j.ID, s.Note)
			}
		}
	}
	for owner, left := range pools {
		if len(left) != 0 {
			t.Errorf("wallet %d has %d unspent seed notes: the seed is oversized", owner, len(left))
		}
	}
	if jobs[0].ID != "transfer-4-0" {
		t.Fatalf("the largest transfers go first, got %s", jobs[0].ID)
	}
}

func TestSpendJobs_transferKeepsValueBalanced(t *testing.T) {
	plan := Plan{Transfers: map[int]int{4: 1}, Signers: 1}
	jobs, err := plan.SpendJobs(poolsFor(plan), math.NewInt(32), testParams(), signer)
	if err != nil {
		t.Fatal(err)
	}
	job := jobs[0]
	var out uint64
	for _, o := range job.Outputs {
		out += o.Value
	}
	in := uint64(len(job.Spends)) * NoteValue
	fee := TransferFee(math.NewInt(32), testParams(), 4)
	if !math.NewIntFromUint64(in - out).Equal(fee) {
		t.Fatalf("a transfer leaves %d, want its fee %s", in-out, fee)
	}
	if job.Outputs[0].Account == job.Spends[0].Account {
		t.Fatal("a transfer must pay another wallet")
	}
}

func TestSpendJobs_unshieldCarriesItsSignerAndLeavesTheAmount(t *testing.T) {
	plan := Plan{Unshields: 2, Signers: 2}
	jobs, err := plan.SpendJobs(poolsFor(plan), math.NewInt(1), testParams(), signer)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := range jobs {
		if j.Kind != KindUnshield || j.UnshieldSigner != fmt.Sprintf("%040x", i) {
			t.Fatalf("job %d: %+v", i, j)
		}
		if NoteValue-j.Outputs[0].Value != UnshieldAmount {
			t.Fatalf("job %d leaves %d, want %d", i, NoteValue-j.Outputs[0].Value, UnshieldAmount)
		}
	}
}

func TestSpendJobs_errors(t *testing.T) {
	plan := Plan{Transfers: map[int]int{2: 1}, Signers: 1}
	if _, err := plan.SpendJobs(map[uint32][]string{}, math.NewInt(1), testParams(), signer); err == nil {
		t.Error("a job with no seed notes was built")
	}
	broke := math.NewInt(1_000_000_000)
	if _, err := plan.SpendJobs(poolsFor(plan), broke, testParams(), signer); err == nil {
		t.Error("a fee larger than the notes was accepted")
	}
	un := Plan{Unshields: 1, Signers: 1}
	failing := func(int) (string, error) { return "", fmt.Errorf("no such account") }
	if _, err := un.SpendJobs(poolsFor(un), math.NewInt(1), testParams(), failing); err == nil {
		t.Error("a signer lookup error was swallowed")
	}
}

func TestEmptyPlanBuildsNothing(t *testing.T) {
	plan := Plan{Signers: 1}
	if plan.SeedNotes() != 0 || len(plan.SeedJobs()) != 0 {
		t.Fatal("an empty plan asked for seed notes")
	}
	jobs, err := plan.SpendJobs(map[uint32][]string{}, math.NewInt(1), testParams(), signer)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("got %d jobs, %v", len(jobs), err)
	}
}

func TestUnshieldSigner_matchesTheJobOrder(t *testing.T) {
	plan := Plan{Transfers: map[int]int{2: 3}, Unshields: 4, Signers: 3}
	for i := 0; i < 4; i++ {
		if got, want := unshieldSigner(3+i, plan), i%3; got != want {
			t.Errorf("unshield %d: signer %d, want %d", i, got, want)
		}
	}
}
