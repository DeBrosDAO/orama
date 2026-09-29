//go:build cgo && !nowasm

package app_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	shieldedpolicy "github.com/DeBrosOfficial/network/chain/x/shielded/policy"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
)

// Genesis code ids, in manifest order.
const (
	codeCW20     = 1
	codeCW721    = 2
	codeEscrow   = 3
	codeMultisig = 4
	codeVesting  = 5
)

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}

func TestStandardCW20_instantiateTransferAndQuery(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 2})
	alice, bob := c.users[0], c.users[1]
	token := c.instantiate(alice, codeCW20, map[string]any{
		"name": "Gold", "symbol": "GLD", "decimals": 6,
		"initial_balances": []map[string]string{{"address": alice.addr.String(), "amount": "1000"}},
	}, nil)

	c.mustExec(alice, token, map[string]any{"transfer": map[string]string{"recipient": bob.addr.String(), "amount": "250"}}, nil)

	require.JSONEq(t, `{"balance":"750"}`, string(c.smart(token, map[string]any{"balance": map[string]string{"address": alice.addr.String()}})))
	require.JSONEq(t, `{"balance":"250"}`, string(c.smart(token, map[string]any{"balance": map[string]string{"address": bob.addr.String()}})))

	over := c.exec(alice, token, map[string]any{"transfer": map[string]string{"recipient": bob.addr.String(), "amount": "9999"}}, nil)
	require.NotZero(t, over.Code, "a transfer above the balance fails")
}

func TestStandardCW721_mintTransferAndQuery(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 2})
	alice, bob := c.users[0], c.users[1]
	nft := c.instantiate(alice, codeCW721, map[string]any{
		"name": "Art", "symbol": "ART", "collection_info_extension": nil,
		"minter": alice.addr.String(), "creator": alice.addr.String(), "withdraw_address": nil,
	}, nil)

	c.mustExec(alice, nft, map[string]any{"mint": map[string]any{"token_id": "1", "owner": alice.addr.String(), "token_uri": "ipfs://one", "extension": nil}}, nil)
	require.Contains(t, string(c.smart(nft, map[string]any{"owner_of": map[string]string{"token_id": "1"}})), alice.addr.String())

	c.mustExec(alice, nft, map[string]any{"transfer_nft": map[string]string{"recipient": bob.addr.String(), "token_id": "1"}}, nil)
	require.Contains(t, string(c.smart(nft, map[string]any{"owner_of": map[string]string{"token_id": "1"}})), bob.addr.String())

	stolen := c.exec(alice, nft, map[string]any{"transfer_nft": map[string]string{"recipient": alice.addr.String(), "token_id": "1"}}, nil)
	require.NotZero(t, stolen.Code, "the previous owner cannot take the token back")
	notMinter := c.exec(bob, nft, map[string]any{"mint": map[string]any{"token_id": "2", "owner": bob.addr.String(), "extension": nil}}, nil)
	require.NotZero(t, notMinter.Code, "only the minter mints")
}

func TestStandardMultisig_proposeVoteExecute(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 3})
	alice, bob, carol := c.users[0], c.users[1], c.users[2]
	multisig := c.instantiate(alice, codeMultisig, map[string]any{
		"voters":            []map[string]any{{"addr": alice.addr.String(), "weight": 1}, {"addr": bob.addr.String(), "weight": 1}},
		"threshold":         map[string]any{"absolute_count": map[string]any{"weight": 2}},
		"max_voting_period": map[string]any{"height": 1000},
	}, nil)

	c.mustExec(alice, multisig, map[string]any{"propose": map[string]any{"title": "noop", "description": "nothing", "msgs": []any{}}}, nil)
	early := c.exec(alice, multisig, map[string]any{"execute": map[string]any{"proposal_id": 1}}, nil)
	require.NotZero(t, early.Code, "one vote is below the threshold")

	outsider := c.exec(carol, multisig, map[string]any{"vote": map[string]any{"proposal_id": 1, "vote": "yes"}}, nil)
	require.NotZero(t, outsider.Code, "a non-member cannot vote")

	c.mustExec(bob, multisig, map[string]any{"vote": map[string]any{"proposal_id": 1, "vote": "yes"}}, nil)
	c.mustExec(carol, multisig, map[string]any{"execute": map[string]any{"proposal_id": 1}}, nil)
	require.Contains(t, string(c.smart(multisig, map[string]any{"proposal": map[string]any{"proposal_id": 1}})), `"status":"executed"`)
}

// TestStandardMultisig_cannotPayNoramaToAUser holds ORAMA in a multisig and passes a proposal that
// bank-sends it to a user. The proposal executes as the multisig contract, so the send restriction
// refuses it: a standard contract is no way around the mandatory-shielding rule.
func TestStandardMultisig_cannotPayNoramaToAUser(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 3})
	alice, bob, carol := c.users[0], c.users[1], c.users[2]
	multisig := c.instantiate(alice, codeMultisig, map[string]any{
		"voters":            []map[string]any{{"addr": alice.addr.String(), "weight": 1}, {"addr": bob.addr.String(), "weight": 1}},
		"threshold":         map[string]any{"absolute_count": map[string]any{"weight": 2}},
		"max_voting_period": map[string]any{"height": 1000},
	}, nil)
	c.fund(multisig, 50*params.NoramaPerOrama)

	pay := map[string]any{"bank": map[string]any{"send": map[string]any{
		"to_address": carol.addr.String(), "amount": []map[string]string{{"denom": params.BaseDenom, "amount": "1000000000"}},
	}}}
	c.mustExec(alice, multisig, map[string]any{"propose": map[string]any{"title": "pay carol", "description": "x", "msgs": []any{pay}}}, nil)
	c.mustExec(bob, multisig, map[string]any{"vote": map[string]any{"proposal_id": 1, "vote": "yes"}}, nil)

	res := c.exec(alice, multisig, map[string]any{"execute": map[string]any{"proposal_id": 1}}, nil)
	require.NotZero(t, res.Code)
	require.Contains(t, res.Log, shieldedpolicy.ErrPublicPayment.Error())
	require.True(t, c.app.BankKeeper.GetBalance(c.ctx(), carol.addr, params.BaseDenom).Amount.Equal(math.NewInt(1_000*params.NoramaPerOrama)),
		"carol still holds only her genesis funding")
}

// createGold creates factory/{alice}/gold with mint authority and mints amount of it to alice,
// through real transactions.
func (c *wasmChain) createGold(alice wasmUser, amount int64) string {
	c.t.Helper()
	c.mustDeliver(alice, &tokentypes.MsgCreateToken{
		Creator: alice.addr.String(), Subdenom: "gold", Name: "Gold", Symbol: "GLD", Description: "test gold", Mint: true,
	})
	denom := tokentypes.Denom(alice.addr.String(), "gold")
	c.mustDeliver(alice, &tokentypes.MsgMint{Sender: alice.addr.String(), Denom: denom, Recipient: alice.addr.String(), Amount: math.NewInt(amount)})
	return denom
}

func (c *wasmChain) balance(addr sdk.AccAddress, denom string) math.Int {
	return c.app.BankKeeper.GetBalance(c.ctx(), addr, denom).Amount
}

func TestStandardEscrow_approveReleasesATokenToTheRecipient(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 3})
	alice, bob, carol := c.users[0], c.users[1], c.users[2]
	gold := c.createGold(alice, 1_000)
	escrow := c.instantiate(alice, codeEscrow, map[string]any{}, nil)

	create := map[string]any{"create": map[string]any{
		"id": "deal-one", "arbiter": bob.addr.String(), "recipient": carol.addr.String(),
		"title": "t", "description": "d", "end_height": nil, "end_time": nil, "cw20_whitelist": nil,
	}}
	c.mustExec(alice, escrow, create, sdk.NewCoins(sdk.NewCoin(gold, math.NewInt(100))))
	require.True(t, c.balance(escrow, gold).Equal(math.NewInt(100)))

	stranger := c.exec(carol, escrow, map[string]any{"approve": map[string]string{"id": "deal-one"}}, nil)
	require.NotZero(t, stranger.Code, "only the arbiter approves")

	c.mustExec(bob, escrow, map[string]any{"approve": map[string]string{"id": "deal-one"}}, nil)
	require.True(t, c.balance(carol.addr, gold).Equal(math.NewInt(100)))
	require.True(t, c.balance(escrow, gold).IsZero())
}

// TestStandardEscrow_noramaCannotLeaveToAUser escrows ORAMA. Both release paths pay a user with a
// bank send, which the send restriction refuses, so the ORAMA stays in the escrow. This is the
// stock escrow's limit on this chain: it releases ORAMA only to a contract, and user-facing
// payouts of ORAMA must go through the earnings binding.
func TestStandardEscrow_noramaCannotLeaveToAUser(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 3})
	alice, bob, carol := c.users[0], c.users[1], c.users[2]
	escrow := c.instantiate(alice, codeEscrow, map[string]any{}, nil)
	c.mustExec(alice, escrow, map[string]any{"create": map[string]any{
		"id": "orama-deal", "arbiter": bob.addr.String(), "recipient": carol.addr.String(),
		"title": "t", "description": "d", "end_height": nil, "end_time": nil, "cw20_whitelist": nil,
	}}, norama(5*params.NoramaPerOrama))

	approve := c.exec(bob, escrow, map[string]any{"approve": map[string]string{"id": "orama-deal"}}, nil)
	require.NotZero(t, approve.Code)
	require.Contains(t, approve.Log, shieldedpolicy.ErrPublicPayment.Error())
	refund := c.exec(bob, escrow, map[string]any{"refund": map[string]string{"id": "orama-deal"}}, nil)
	require.NotZero(t, refund.Code)
	require.True(t, c.balance(escrow, params.BaseDenom).Equal(math.NewInt(5*params.NoramaPerOrama)))
}

func TestStandardVesting_distributesATokenOverTime(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 2})
	alice, carol := c.users[0], c.users[1]
	gold := c.createGold(alice, 1_000)
	vest := c.instantiate(alice, codeVesting, map[string]any{
		"owner": alice.addr.String(), "recipient": carol.addr.String(), "title": "t", "description": nil,
		"total": "1000", "denom": map[string]string{"native": gold}, "schedule": "saturating_linear",
		"start_time": nil, "vesting_duration_seconds": 100, "unbonding_duration_seconds": 0,
	}, sdk.NewCoins(sdk.NewCoin(gold, math.NewInt(1_000))))

	c.blocks(5)
	c.mustExec(alice, vest, map[string]any{"distribute": map[string]any{}}, nil)
	got := c.balance(carol.addr, gold)
	require.True(t, got.IsPositive(), "some has vested")
	require.True(t, got.LT(math.NewInt(400)), "and not all of it: %s", got)
	require.True(t, got.Add(c.balance(vest, gold)).Equal(math.NewInt(1_000)))

	// Nobody but the owner can cancel.
	require.NotZero(t, c.exec(carol, vest, map[string]any{"cancel": map[string]any{}}, nil).Code)
}

// TestStandardVesting_noramaCannotBeVestedToAUser tries to vest ORAMA to a user. For the chain's
// native denom the vesting contract's instantiate sends a distribution SetWithdrawAddress, which
// this chain refuses (it would redirect staking rewards), so an ORAMA vest cannot be created. A
// vest of a user token can (see TestStandardVesting_distributesATokenOverTime).
func TestStandardVesting_noramaCannotBeVestedToAUser(t *testing.T) {
	c := newWasmChain(t, wasmChainOptions{users: 2})
	alice, carol := c.users[0], c.users[1]
	raw, err := json.Marshal(map[string]any{
		"owner": alice.addr.String(), "recipient": carol.addr.String(), "title": "t", "description": nil,
		"total": "3000000000", "denom": map[string]string{"native": params.BaseDenom}, "schedule": "saturating_linear",
		"start_time": nil, "vesting_duration_seconds": 100, "unbonding_duration_seconds": 0,
	})
	require.NoError(t, err)
	res := c.deliver(alice, &wasmtypes.MsgInstantiateContract{
		Sender: alice.addr.String(), CodeID: codeVesting, Label: "vest", Msg: raw, Funds: norama(3 * params.NoramaPerOrama),
	})
	require.NotZero(t, res.Code)
	require.Contains(t, res.Log, "set_withdraw_address")
	require.True(t, c.balance(alice.addr, params.BaseDenom).GT(math.NewInt(900*params.NoramaPerOrama)), "the funds went back with the failed tx")
}
