//go:build e2e_fleet

package chaineconomics

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// minEligibleOperators is the operator-house size a tier needs (docs/CHAIN.md
// "x/houses": 21 members).
const minEligibleOperators = 21

// tiersView is `oramad query houses tiers`.
type tiersView struct {
	ParameterOpen     bool      `json:"parameter_open"`
	StructuralOpen    bool      `json:"structural_open"`
	Lambda            chain.Dec `json:"lambda"`
	BondedStake       chain.Int `json:"bonded_stake"`
	EligibleOperators chain.Int `json:"eligible_operators"`
}

func housesMsg(typ string, fields map[string]any) chain.Msg {
	return chain.NewMsg("/orama.houses.v1."+typ, fields)
}

// TestHouses_tiersClosedOnASmallNetwork: nobody governs during bootstrap. The
// run has three validators, far below bootstrap_exit_stake (271,000 ORAMA)
// and without a 21-member operator house (no operator has 90 days of
// service), so both tiers are closed (docs/CHAIN.md "x/houses").
func TestHouses_tiersClosedOnASmallNetwork(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	var v tiersView
	c.Query(t, c.Node(t, 0), &v, "houses", "tiers")
	if v.ParameterOpen || v.StructuralOpen {
		t.Errorf("tiers open: parameter=%v structural=%v (bonded %s, eligible %s, lambda %v)",
			v.ParameterOpen, v.StructuralOpen, v.BondedStake.String(), v.EligibleOperators.String(), v.Lambda.Float())
	}
	if v.EligibleOperators.Int64() >= minEligibleOperators {
		t.Errorf("%s eligible operators on a three-node run", v.EligibleOperators.String())
	}
	var p struct {
		Params struct {
			BootstrapExitStake chain.Int `json:"bootstrap_exit_stake"`
			HouseBond          chain.Int `json:"house_bond"`
		} `json:"params"`
	}
	c.Query(t, c.Node(t, 0), &p, "houses", "params")
	if p.Params.BootstrapExitStake.Cmp(chain.Orama(271_000)) != 0 || p.Params.HouseBond.Cmp(chain.Orama(1000)) != 0 {
		t.Errorf("bootstrap_exit_stake %s, house_bond %s; want 271000 and 1000 ORAMA", p.Params.BootstrapExitStake.String(), p.Params.HouseBond.String())
	}
}

// TestHouses_proposalsRefusedWhileTiersClosed: a well-formed parameter
// proposal and a well-formed structural (software upgrade) proposal are both
// refused with ErrTierClosed, and no proposal exists afterwards; a malformed
// content is refused before the tier check.
func TestHouses_proposalsRefusedWhileTiersClosed(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	param := map[string]any{"parameter_change": map[string]any{"token_quorum": "0.4", "token_pass_threshold": "0.5",
		"voting_period_seconds": "604800", "house_bond": chain.Orama(1000).String(), "max_eligible_per_prefix16": 3, "max_eligible_per_asn": 5}}
	upgrade := map[string]any{"software_upgrade": map[string]any{"name": "e2e-never", "height": "999999999"}}
	for name, content := range map[string]map[string]any{"parameter": param, "structural": upgrade} {
		r := c.Submit(t, k, chain.TxOptions{}, housesMsg("MsgSubmitProposal", map[string]any{"proposer": k.Address, "content": content}))
		chain.RequireRefused(t, name+" proposal", r, "governance tier is closed")
	}
	empty := c.Submit(t, k, chain.TxOptions{}, housesMsg("MsgSubmitProposal", map[string]any{"proposer": k.Address, "content": map[string]any{}}))
	chain.RequireRefused(t, "proposal with no action", empty, "must set exactly one action")
	both := map[string]any{"parameter_change": param["parameter_change"], "software_upgrade": upgrade["software_upgrade"]}
	two := c.Submit(t, k, chain.TxOptions{}, housesMsg("MsgSubmitProposal", map[string]any{"proposer": k.Address, "content": both}))
	chain.RequireRefused(t, "proposal with two actions", two, "must set exactly one action")
	outOfBounds := map[string]any{"parameter_change": map[string]any{"token_quorum": "0.4", "token_pass_threshold": "0.5",
		"voting_period_seconds": "604800", "house_bond": "0", "max_eligible_per_prefix16": 3, "max_eligible_per_asn": 5}}
	oob := c.Submit(t, k, chain.TxOptions{}, housesMsg("MsgSubmitProposal", map[string]any{"proposer": k.Address, "content": outOfBounds}))
	chain.RequireRefused(t, "parameter change with a zero house bond", oob, "house_bond must be in")
	if out := c.QueryFails(t, k.Node, "houses", "proposal", "1"); !chain.NotFound(out) {
		t.Errorf("proposal 1 exists on a chain whose tiers never opened: %s", out)
	}
	c.RequireInvariants(t, "refused proposals")
}

// TestHouses_votesAndExecutionNeedAProposal: votes of either house and
// execution name a proposal; with none (no tier ever opened) each is refused,
// as is a vote with no option (VOTE_OPTION_UNSPECIFIED). ExecuteProposal
// before a timelock can only be reached with a passed proposal, which this
// chain cannot produce.
func TestHouses_votesAndExecutionNeedAProposal(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	cases := map[string]chain.Msg{
		"token vote":    housesMsg("MsgVoteToken", map[string]any{"voter": k.Address, "proposal_id": "424242", "option": "YES"}),
		"operator vote": housesMsg("MsgVoteOperator", map[string]any{"voter": k.Address, "proposal_id": "424242", "option": "NO"}),
		"execute":       housesMsg("MsgExecuteProposal", map[string]any{"signer": k.Address, "proposal_id": "424242"}),
	}
	for name, m := range cases {
		chain.RequireRefused(t, name+" on no proposal", c.Submit(t, k, chain.TxOptions{}, m), "failed to load proposal 424242")
	}
	unspecified := housesMsg("MsgVoteToken", map[string]any{"voter": k.Address, "proposal_id": "424242", "option": "VOTE_OPTION_UNSPECIFIED"})
	chain.RequireRefused(t, "vote with no option", c.Submit(t, k, chain.TxOptions{}, unspecified), "vote option is empty")
}

// TestHouses_bondNeedsBankAndUnlockNeedsBond: the house bond is locked from
// the BANK balance (x/houses/keeper/bond.go), which no run account holds, so
// a lock is refused with insufficient funds; a zero lock is refused; and
// unlocking without a bond is refused. The HouseBond query has nothing for
// the account (asked through abci_query: the CLI has no command for it).
func TestHouses_bondNeedsBankAndUnlockNeedsBond(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	lock := func(amount string) chain.Result {
		return c.Submit(t, k, chain.TxOptions{}, housesMsg("MsgLockHouseBond", map[string]any{"signer": k.Address, "amount": amount}))
	}
	chain.RequireRefused(t, "lock 1000 ORAMA from an empty bank", lock(chain.Orama(1000).String()), "failed to lock house bond", "insufficient funds")
	chain.RequireRefused(t, "lock zero", lock("0"), "house bond lock must be positive")
	unlock := c.Submit(t, k, chain.TxOptions{}, housesMsg("MsgUnlockHouseBond", map[string]any{"signer": k.Address}))
	chain.RequireRefused(t, "unlock without a bond", unlock, "failed to load house bond")
	a := c.ABCIQuery(t, k.Node, "/orama.houses.v1.Query/HouseBond", chain.PB{}.Text(1, k.Address))
	if a.Code == 0 || !chain.NotFound(a.Log) {
		t.Errorf("HouseBond of an account with no bond: code %d log %q", a.Code, a.Log)
	}
	c.RequireInvariants(t, "refused house bonds")
}

// TestHouses_voteAndEnactedQueries: Vote needs a proposal id and a voter and
// finds nothing on a chain with no proposal; Enacted is the empty record
// (no structural decision was ever enacted: no upgrade scheduled). Both are
// asked through abci_query.
func TestHouses_voteAndEnactedQueries(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	k := c.Validator(t, n)
	missing := c.ABCIQuery(t, n, "/orama.houses.v1.Query/Vote", chain.PB{})
	if missing.Code == 0 || !strings.Contains(missing.Log, "proposal_id and voter are required") {
		t.Errorf("Vote with an empty request: code %d log %q", missing.Code, missing.Log)
	}
	none := c.ABCIQuery(t, n, "/orama.houses.v1.Query/Vote", chain.PB{}.Uint(1, 1).Text(2, k.Address))
	if none.Code == 0 || !chain.NotFound(none.Log) {
		t.Errorf("Vote on a proposal that does not exist: code %d log %q", none.Code, none.Log)
	}
	en := c.ABCIQuery(t, n, "/orama.houses.v1.Query/Enacted", chain.PB{})
	if en.Code != 0 {
		t.Fatalf("Enacted: code %d log %q", en.Code, en.Log)
	}
	f, err := chain.DecodePB(en.Value)
	if err != nil {
		t.Fatalf("Enacted answer: %v", err)
	}
	rec, _ := f.Msg(1)
	if _, scheduled := rec.Msg(enactedScheduledUpgradeField); scheduled {
		t.Errorf("an upgrade is scheduled on a chain whose structural tier never opened")
	}
}

// enactedScheduledUpgradeField is the field number of
// orama.houses.v1.Enacted.scheduled_upgrade.
const enactedScheduledUpgradeField = 1
