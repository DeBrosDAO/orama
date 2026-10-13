package shielded

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// fakeProver stands in for the Rust factory: it frames bundles whose commitments are a running
// counter, and reports the notes a shield creates.
type fakeProver struct {
	counter   byte
	shields   [][]ShieldJob
	spendJobs [][]SpendJob
	failSpend error
	leavesGot []string
	rootGot   string
}

func (f *fakeProver) bundle(actions int) string {
	cmxs := make([]byte, actions)
	for i := range cmxs {
		f.counter++
		cmxs[i] = f.counter
	}
	return hex.EncodeToString(fakeBundle(cmxs...))
}

func (f *fakeProver) Shields(_ context.Context, _ string, jobs []ShieldJob) ([]Built, error) {
	f.shields = append(f.shields, jobs)
	var out []Built
	for _, j := range jobs {
		b := Built{ID: j.ID, Actions: max(len(j.Outputs), MinActions), Bundle: ""}
		b.Bundle = f.bundle(b.Actions)
		for i, o := range j.Outputs {
			b.Notes = append(b.Notes, NoteOut{Account: o.Account, Value: o.Value, Action: i, Note: fmt.Sprintf("note-%s-%d", j.ID, i)})
		}
		out = append(out, b)
	}
	return out, nil
}

func (f *fakeProver) Spends(_ context.Context, _ string, leaves []string, root string, jobs []SpendJob) (Response, error) {
	f.leavesGot, f.rootGot = leaves, root
	f.spendJobs = append(f.spendJobs, jobs)
	if f.failSpend != nil {
		return Response{}, f.failSpend
	}
	resp := Response{Anchor: root}
	for _, j := range jobs {
		resp.Built = append(resp.Built, Built{ID: j.ID, Actions: max(len(j.Spends), MinActions), Bundle: f.bundle(max(len(j.Spends), MinActions))})
	}
	return resp, nil
}

// fakeChain records the seed bundles it is asked to submit and serves them back as block 2.
type fakeChain struct {
	submitted [][]byte
	treeSize  uint64
	submitErr error
}

func (c *fakeChain) ChainID() string                             { return "orama-localnet-1" }
func (c *fakeChain) GenesisHash(context.Context) (string, error) { return "GENESIS", nil }
func (c *fakeChain) Params(context.Context) (Params, error)      { return testParams(), nil }
func (c *fakeChain) GasPrice(context.Context) (math.Int, error)  { return math.NewInt(32), nil }
func (c *fakeChain) SignerAddress(i int) (string, error)         { return fmt.Sprintf("%040x", i), nil }

func (c *fakeChain) SubmitShields(_ context.Context, bundles [][]byte) error {
	c.submitted = append(c.submitted, bundles...)
	return c.submitErr
}

func (c *fakeChain) Tip(context.Context) (Tip, error) {
	return Tip{Height: 3, TreeSize: c.treeSize, Root: "00ff"}, nil
}

func (c *fakeChain) Blocks() BlockSource {
	src := fakeSource{1: nil, 3: nil}
	var recs []TxRecord
	for _, b := range c.submitted {
		recs = append(recs, TxRecord{Msgs: []sdk.Msg{&types.MsgShield{Bundle: b}}})
	}
	src[2] = recs
	return src
}

func buildPlan() Plan {
	return Plan{Shields: 3, Transfers: map[int]int{2: 2, 4: 1}, Unshields: 2, Signers: 3}
}

func newChain(t *testing.T, plan Plan, prover *fakeProver) *fakeChain {
	t.Helper()
	ch := &fakeChain{}
	// Seed bundles are framed with SeedJobs' action counts; count the commitments they hold.
	for _, j := range plan.SeedJobs() {
		ch.treeSize += uint64(max(len(j.Outputs), MinActions))
	}
	return ch
}

func TestBuild_producesACacheThatValidatesAgainstTheChainItWasBuiltOn(t *testing.T) {
	plan, prover := buildPlan(), &fakeProver{}
	ch := newChain(t, plan, prover)
	cache, err := Build(context.Background(), ch, plan, Options{Prover: prover, AccountSeed: "seed"})
	if err != nil {
		t.Fatal(err)
	}
	if got := cache.Unused(EntryTransfer, 2); got != 2 {
		t.Errorf("%d two-action transfers", got)
	}
	if got := cache.Unused(EntryTransfer, 4); got != 1 {
		t.Errorf("%d four-action transfers", got)
	}
	if got := cache.Unused(EntryUnshield, 0); got != 2 {
		t.Errorf("%d unshields", got)
	}
	if got := cache.Unused(EntryShield, 0); got != 3 {
		t.Errorf("%d shields", got)
	}
	state := ChainState{ChainID: "orama-localnet-1", GenesisHash: "GENESIS", Height: 3, TreeSize: ch.treeSize}
	if err := cache.Validate(state, DefaultAnchorMargin); err != nil {
		t.Fatalf("a cache just built is stale: %v", err)
	}
	if cache.AnchorHeight != 2 || cache.TreeSize != ch.treeSize || cache.Anchor != "00ff" {
		t.Errorf("anchor %d / size %d / root %s", cache.AnchorHeight, cache.TreeSize, cache.Anchor)
	}
	if uint64(len(prover.leavesGot)) != ch.treeSize || prover.rootGot != "00ff" {
		t.Errorf("the prover was given %d leaves and root %q", len(prover.leavesGot), prover.rootGot)
	}
	for _, e := range cache.Entries {
		switch e.Kind {
		case EntryTransfer:
			if e.Account != Signerless {
				t.Errorf("transfer %s has a signer", e.ID)
			}
		case EntryUnshield, EntryShield:
			if e.Account < 0 || e.Account >= plan.Signers {
				t.Errorf("%s %s signer %d is outside the accounts", e.Kind, e.ID, e.Account)
			}
		}
	}
}

func TestBuild_refusesATreeTheScanDoesNotReproduce(t *testing.T) {
	plan, prover := buildPlan(), &fakeProver{}
	ch := newChain(t, plan, prover)
	ch.treeSize += 5 // the chain holds notes the blocks do not show
	_, err := Build(context.Background(), ch, plan, Options{Prover: prover})
	if err == nil || len(prover.spendJobs) != 0 {
		t.Fatalf("built spends against a tree it could not reproduce: %v", err)
	}
}

func TestBuild_reportsAChainThatWillNotTakeTheSeeds(t *testing.T) {
	plan, prover := buildPlan(), &fakeProver{}
	ch := newChain(t, plan, prover)
	ch.submitErr = errors.New("shielded bundle refused: verifier not linked")
	_, err := Build(context.Background(), ch, plan, Options{Prover: prover})
	if err == nil || len(prover.spendJobs) != 0 {
		t.Fatalf("got %v", err)
	}
}

func TestBuild_proverFailureIsReturned(t *testing.T) {
	plan := buildPlan()
	prover := &fakeProver{failSpend: errors.New("out of memory")}
	if _, err := Build(context.Background(), newChain(t, plan, prover), plan, Options{Prover: prover}); err == nil {
		t.Fatal("a prover failure was swallowed")
	}
}

func TestBuild_shieldsOnlyPlanNeedsNoSeedAndNoAnchor(t *testing.T) {
	plan, prover := Plan{Shields: 4, Signers: 2}, &fakeProver{}
	ch := &fakeChain{}
	cache, err := Build(context.Background(), ch, plan, Options{Prover: prover})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.submitted) != 0 || len(prover.spendJobs) != 0 || cache.Anchor != "" || cache.Unused(EntryShield, 0) != 4 {
		t.Fatalf("submitted %d, spends %d, anchor %q, shields %d", len(ch.submitted), len(prover.spendJobs), cache.Anchor, cache.Unused(EntryShield, 0))
	}
}

func TestBuild_rejectsAPlanTheChainCannotTake(t *testing.T) {
	_, err := Build(context.Background(), &fakeChain{}, Plan{Signers: 1, Transfers: map[int]int{99: 1}}, Options{Prover: &fakeProver{}})
	if err == nil {
		t.Fatal("a 99-action transfer was planned")
	}
}
