package app_test

import (
	stded25519 "crypto/ed25519"
	"strings"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	oramatx "github.com/DeBrosOfficial/network/chain/client/tx"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	housetypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// The wallet-flow suite drives what a RootWallet user does, through the same
// transaction builder the CLI and the SDK use (chain/client/tx): every
// transaction is built and signed with SIGN_MODE_DIRECT under a secp256k1
// account, encoded, and delivered through FinalizeBlock, so it crosses the
// ante chain (signature, fee from earnings, bond top-up) and the message
// router like a real one. State is read back from the keepers afterwards.
//
// The chain refuses public user-to-user norama sends, and without the Orchard
// verifiers linked it refuses every shielded bundle, so the payment tests pin
// what a wallet can do on this build: pay fees from earnings, move value only in
// factory denoms, and turn earnings into stake, bonds, hot-key funds and market
// proceeds.

const (
	oneOrama   = int64(params.NoramaPerOrama)
	flowGas    = uint64(1_000_000)
	flowCredit = int64(1_000) // ORAMA credited to a funded wallet
)

// flow is a real app plus a builder and wallets.
type flow struct {
	*wiringChain
	builder *oramatx.Builder
}

type wallet struct {
	acc  oramatx.Account
	addr sdk.AccAddress
}

func newFlow(t *testing.T) *flow {
	t.Helper()
	b, err := oramatx.New(buildTestApp(t).TxConfig())
	require.NoError(t, err)
	return &flow{wiringChain: newWiringChain(t), builder: b}
}

// newWallet creates a secp256k1 account on chain with no balance.
func (f *flow) newWallet() wallet {
	f.t.Helper()
	priv := secp256k1.GenPrivKey()
	acc, err := oramatx.DeriveAccount(priv.Bytes())
	require.NoError(f.t, err)
	addr := sdk.AccAddress(priv.PubKey().Address())
	f.write(func(ctx sdk.Context) {
		f.app.AccountKeeper.SetAccount(ctx, f.app.AccountKeeper.NewAccountWithAddress(ctx, addr))
	})
	return wallet{acc: acc, addr: addr}
}

// fundEarnings credits ORAMA to the wallet's earnings account.
func (f *flow) fundEarnings(w wallet, orama int64) {
	f.t.Helper()
	coin := sdk.NewCoin(params.BaseDenom, math.NewInt(orama).MulRaw(oneOrama))
	f.write(func(ctx sdk.Context) {
		require.NoError(f.t, f.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, sdk.NewCoins(coin)))
		require.NoError(f.t, f.app.FeesKeeper.CreditEarnings(ctx, emissiontypes.ModuleName, w.addr, coin))
	})
}

// fundBank gives the wallet a spendable bank balance.
func (f *flow) fundBank(w wallet, orama int64) {
	f.t.Helper()
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(orama).MulRaw(oneOrama)))
	f.write(func(ctx sdk.Context) {
		require.NoError(f.t, f.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, coins))
		require.NoError(f.t, f.app.BankKeeper.SendCoinsFromModuleToAccount(ctx, emissiontypes.ModuleName, w.addr, coins))
	})
}

// deliver builds msgs with the wallet's account, signs them, and finalizes
// them alone in the next block.
func (f *flow) deliver(w wallet, msgs ...sdk.Msg) *abci.ExecTxResult {
	f.t.Helper()
	return f.deliverGas(w, flowGas, msgs...)
}

func (f *flow) deliverGas(w wallet, gas uint64, msgs ...sdk.Msg) *abci.ExecTxResult {
	f.t.Helper()
	acct := f.app.AccountKeeper.GetAccount(f.app.NewContext(true), w.addr)
	require.NotNil(f.t, acct, "wallet has no account")
	raw, err := f.builder.Build(w.acc, oramatx.Unsigned{
		ChainID: testChainID, AccountNumber: acct.GetAccountNumber(), Sequence: acct.GetSequence(),
		GasLimit: gas, Fee: sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, int64(gas))), Msgs: msgs,
	})
	require.NoError(f.t, err)
	f.height++
	resp := finalize(f.t, f.app, f.height, f.at(f.height), raw)
	require.Len(f.t, resp.TxResults, 1)
	return resp.TxResults[0]
}

func requireOK(t *testing.T, res *abci.ExecTxResult) {
	t.Helper()
	require.Zero(t, res.Code, "tx failed: %s", res.Log)
}

// requireRejected asserts the transaction failed and its log names why.
func requireRejected(t *testing.T, res *abci.ExecTxResult, why string) {
	t.Helper()
	require.NotZero(t, res.Code, "tx unexpectedly succeeded")
	require.Contains(t, strings.ToLower(res.Log), strings.ToLower(why))
}

func (f *flow) earnings(addr sdk.AccAddress) math.Int {
	f.t.Helper()
	v, err := f.app.FeesKeeper.GetEarnings(f.app.NewContext(true), addr)
	require.NoError(f.t, err)
	return v
}

func (f *flow) bank(addr sdk.AccAddress, denom string) math.Int {
	return f.app.BankKeeper.GetBalance(f.app.NewContext(true), addr, denom).Amount
}

func (f *flow) requireInvariants() {
	f.t.Helper()
	ctx := f.app.NewContext(true)
	fees, err := f.app.FeesKeeper.CheckInvariants(ctx)
	require.NoError(f.t, err)
	require.True(f.t, fees.EarningsMatchModule && fees.DepositsMatchModule && fees.FeesBalance, fees.Detail)
	nodes, err := f.app.NodesKeeper.CheckInvariants(ctx)
	require.NoError(f.t, err)
	require.True(f.t, nodes.BalanceMatches, nodes.Detail)
}

func norama(n int64) math.Int { return math.NewInt(n).MulRaw(oneOrama) }

// feeOf is the fee n default-gas transactions paid, in norama: the base fee is
// 1 norama per gas unit and deliver pays exactly that.
func feeOf(n int64) int64 { return n * int64(flowGas) }

// ---- payments ----

func TestWalletFlow_feeFallsBackToEarningsWhenTheBankBalanceIsShort(t *testing.T) {
	f := newFlow(t)
	alice := f.newWallet()
	f.fundEarnings(alice, flowCredit)
	before := f.earnings(alice.addr)
	require.True(t, f.bank(alice.addr, params.BaseDenom).IsZero(), "the wallet holds no bank balance")

	res := f.deliver(alice, &nodestypes.MsgRegisterOperator{Operator: alice.addr.String()})
	requireOK(t, res)

	spent := before.Sub(f.earnings(alice.addr))
	require.True(t, spent.IsPositive(), "the fee came out of earnings")
	require.True(t, spent.GTE(math.NewInt(res.GasUsed)), "the fee covers the gas used: spent %s for %d gas", spent, res.GasUsed)
	require.True(t, f.bank(alice.addr, params.BaseDenom).IsZero(), "the bank balance is untouched")
	f.requireInvariants()
}

// The fee is taken from the bank balance first; earnings are the fallback. The
// other tests state their expected bank balances net of that fee.
func TestWalletFlow_feeIsTakenFromTheBankBalanceWhenItCoversIt(t *testing.T) {
	f := newFlow(t)
	alice := f.newWallet()
	f.fundEarnings(alice, flowCredit)
	f.fundBank(alice, 10)
	earningsBefore := f.earnings(alice.addr)

	requireOK(t, f.deliver(alice, &nodestypes.MsgRegisterOperator{Operator: alice.addr.String()}))

	require.Equal(t, norama(10).SubRaw(feeOf(1)).String(), f.bank(alice.addr, params.BaseDenom).String(), "one fee came out of the bank")
	require.True(t, f.earnings(alice.addr).Equal(earningsBefore), "earnings were not touched")
}

func TestWalletFlow_aTransactionWithoutEarningsOrBankCannotPayItsFee(t *testing.T) {
	f := newFlow(t)
	broke := f.newWallet()

	res := f.deliver(broke, &nodestypes.MsgRegisterOperator{Operator: broke.addr.String()})
	require.NotZero(t, res.Code, "a fee with nothing to pay it from must be refused")
	_, err := f.app.NodesKeeper.GetOperator(f.app.NewContext(true), broke.addr.String())
	require.Error(t, err, "the refused transaction registered nothing")
}

func TestWalletFlow_publicUserToUserNoramaSendIsRefused(t *testing.T) {
	f := newFlow(t)
	alice, bob := f.newWallet(), f.newWallet()
	f.fundEarnings(alice, flowCredit)
	f.fundBank(alice, 10)

	res := f.deliver(alice, banktypes.NewMsgSend(alice.addr, bob.addr, sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, oneOrama))))

	requireRejected(t, res, "public")
	require.True(t, f.bank(bob.addr, params.BaseDenom).IsZero(), "the recipient received nothing")
	require.Equal(t, norama(10).SubRaw(feeOf(1)).String(), f.bank(alice.addr, params.BaseDenom).String(), "the sender kept its balance, less the fee the failed transaction still paid")
}

// The wallet builds and signs the shielded messages with the same builder as every other message.
// A node with neither verifier linked, which is what these tests run, refuses every bundle: the
// fee is taken, the pool is untouched, nothing is accepted without both verifiers (an orchardffi
// build with the library linked refuses on the signature instead, the flow's chain ID being no
// vector's: either way the bundle is "refused"). The real proofs
// go through shielded_real_test.go and shielded_wallet_test.go (the orchardffi build).
// requireShieldedRefused asserts a shielded message failed because no bundle is accepted here: the
// tree cannot be hashed without the library ("not linked"), or a linked verifier refused the bundle.
func requireShieldedRefused(t *testing.T, res *abci.ExecTxResult) {
	t.Helper()
	require.NotZero(t, res.Code, "tx unexpectedly succeeded")
	log := strings.ToLower(res.Log)
	require.True(t, strings.Contains(log, "not linked") || strings.Contains(log, "refused"), res.Log)
}

func TestWalletFlow_shieldedMessagesAreRegisteredSignedByTheWalletAndRefusedWithoutBothVerifiers(t *testing.T) {
	f := newFlow(t)
	registered := map[string]bool{}
	for _, url := range f.app.InterfaceRegistry().ListImplementations(sdk.MsgInterfaceProtoName) {
		registered[url] = true
	}
	for _, url := range []string{
		"/orama.shielded.v1.MsgShieldedTransfer", "/orama.shielded.v1.MsgShield",
		"/orama.shielded.v1.MsgShieldEarnings", "/orama.shielded.v1.MsgUnshield",
	} {
		require.True(t, registered[url], "%s is not registered", url)
	}

	alice := f.newWallet()
	f.fundBank(alice, 10)
	bundle := loadVector(t, "ironwood-1-action")
	signer := alice.addr.String()
	start := f.bank(alice.addr, params.BaseDenom)

	shield := f.deliver(alice, &shieldedtypes.MsgShield{Signer: signer, Bundle: bundle})
	requireShieldedRefused(t, shield)
	earnings := f.deliver(alice, &shieldedtypes.MsgShieldEarnings{Signer: signer, Bundle: bundle})
	requireShieldedRefused(t, earnings)
	unshield := f.deliver(alice, &shieldedtypes.MsgUnshield{
		Signer: signer, Bundle: loadVector(t, "ironwood-unshield"), Target: shieldedtypes.UnshieldTargetFeeTopup,
	})
	// The vector's 4900 does not cover the default nullifier fee: a cheap check refuses it before any proof.
	requireRejected(t, unshield, "nullifier fees")
	require.Equal(t, start.SubRaw(3*feeOf(1)).String(), f.bank(alice.addr, params.BaseDenom).String(), "each refused message still paid its fee, and nothing else moved")

	// A shielded message shares its tx with nothing.
	two := f.deliver(alice,
		&shieldedtypes.MsgShield{Signer: signer, Bundle: bundle},
		&shieldedtypes.MsgShield{Signer: signer, Bundle: bundle})
	requireRejected(t, two, "only message")

	_, err := f.app.ShieldedKeeper.Pools.Get(f.app.NewContext(true), poolKey())
	require.Error(t, err, "no pool exists: nothing was accepted")
	f.requireInvariants()
}

// ---- stake ----

func (f *flow) committeeValidator() sdk.ValAddress {
	f.t.Helper()
	vals, err := f.app.StakingKeeper.GetAllValidators(f.app.NewContext(true))
	require.NoError(f.t, err)
	require.NotEmpty(f.t, vals)
	addr, err := sdk.ValAddressFromBech32(vals[0].OperatorAddress)
	require.NoError(f.t, err)
	return addr
}

func TestWalletFlow_delegateAndUndelegateFromEarnings(t *testing.T) {
	f := newFlow(t)
	alice := f.newWallet()
	f.fundEarnings(alice, 10)
	val := f.committeeValidator()
	stake := sdk.NewCoin(params.BaseDenom, norama(2))
	earningsBefore := f.earnings(alice.addr)

	requireOK(t, f.deliver(alice, stakingtypes.NewMsgDelegate(alice.addr.String(), val.String(), stake)))

	del, err := f.app.StakingKeeper.GetDelegation(f.app.NewContext(true), alice.addr, val)
	require.NoError(t, err)
	valRec, err := f.app.StakingKeeper.GetValidator(f.app.NewContext(true), val)
	require.NoError(t, err)
	require.True(t, valRec.TokensFromShares(del.Shares).TruncateInt().Equal(stake.Amount), "the stake is delegated")
	require.True(t, f.bank(alice.addr, params.BaseDenom).IsZero(), "the stake never passed through a bank balance the wallet held")
	require.True(t, earningsBefore.Sub(f.earnings(alice.addr)).GTE(stake.Amount), "the stake and the fee came out of earnings")

	requireOK(t, f.deliver(alice, stakingtypes.NewMsgUndelegate(alice.addr.String(), val.String(), stake)))
	unbonding, err := f.app.StakingKeeper.GetUnbondingDelegation(f.app.NewContext(true), alice.addr, val)
	require.NoError(t, err)
	require.Len(t, unbonding.Entries, 1, "the undelegated stake waits out the unbonding period")
	f.requireInvariants()
}

func TestWalletFlow_delegationBelowTheMinimumIsRefused(t *testing.T) {
	f := newFlow(t)
	alice := f.newWallet()
	f.fundEarnings(alice, 10)
	res := f.deliver(alice, stakingtypes.NewMsgDelegate(alice.addr.String(), f.committeeValidator().String(), sdk.NewInt64Coin(params.BaseDenom, 1)))
	require.NotZero(t, res.Code, "a dust delegation must be refused")
}

// ---- x/houses ----

func (f *flow) seedVotingProposal() uint64 {
	f.t.Helper()
	var id uint64
	f.write(func(ctx sdk.Context) {
		// The parameter tier is closed at bootstrap, so a wallet cannot submit a
		// proposal yet; a proposal that is already voting is seeded directly.
		id = 1
		require.NoError(f.t, f.app.HousesKeeper.Proposals.Set(ctx, id, housetypes.Proposal{
			Id: id, Proposer: f.committeeValidatorAcc().String(), Status: housetypes.ProposalStatus_VOTING,
			SubmitUnixNano:    ctx.BlockTime().UnixNano(),
			VotingEndUnixNano: ctx.BlockTime().Add(24 * time.Hour).UnixNano(),
			Content:           housetypes.ProposalContent{SoftwareUpgrade: &housetypes.SoftwareUpgrade{Name: "v2", Height: 1_000_000}},
		}))
	})
	return id
}

func (f *flow) committeeValidatorAcc() sdk.AccAddress { return sdk.AccAddress(f.committeeValidator()) }

func TestWalletFlow_voteInTheTokenHouse(t *testing.T) {
	f := newFlow(t)
	alice := f.newWallet()
	f.fundEarnings(alice, 10)
	id := f.seedVotingProposal()

	requireOK(t, f.deliver(alice, &housetypes.MsgVoteToken{Voter: alice.addr.String(), ProposalId: id, Option: housetypes.VoteOption_YES}))
	vote, err := f.app.HousesKeeper.TokenVotes.Get(f.app.NewContext(true), collections.Join(id, alice.addr.String()))
	require.NoError(t, err)
	require.Equal(t, housetypes.VoteOption_YES, vote.Option)

	res := f.deliver(alice, &housetypes.MsgVoteToken{Voter: alice.addr.String(), ProposalId: id, Option: housetypes.VoteOption_NO})
	requireRejected(t, res, "already")
	res = f.deliver(alice, &housetypes.MsgVoteToken{Voter: alice.addr.String(), ProposalId: 99, Option: housetypes.VoteOption_YES})
	require.NotZero(t, res.Code, "a vote on a proposal that does not exist must fail")
}

func TestWalletFlow_operatorHouseVoteNeedsAHouseSeat(t *testing.T) {
	f := newFlow(t)
	alice := f.newWallet()
	f.fundEarnings(alice, 10)
	id := f.seedVotingProposal()

	res := f.deliver(alice, &housetypes.MsgVoteOperator{Voter: alice.addr.String(), ProposalId: id, Option: housetypes.VoteOption_YES})
	require.NotZero(t, res.Code, "an account outside the operator house cannot vote in it")
}

func TestWalletFlow_proposalsStayClosedAtBootstrapAndABondLocksAndUnlocks(t *testing.T) {
	f := newFlow(t)
	alice := f.newWallet()
	f.fundEarnings(alice, 10)
	f.fundBank(alice, 20)

	res := f.deliver(alice, &housetypes.MsgSubmitProposal{
		Proposer: alice.addr.String(),
		Content:  housetypes.ProposalContent{SoftwareUpgrade: &housetypes.SoftwareUpgrade{Name: "v2", Height: 1_000_000}},
	})
	requireRejected(t, res, "tier")

	bond := norama(5)
	requireOK(t, f.deliver(alice, &housetypes.MsgLockHouseBond{Signer: alice.addr.String(), Amount: bond}))
	require.Equal(t, norama(15).SubRaw(feeOf(2)).String(), f.bank(alice.addr, params.BaseDenom).String(), "the bond left the bank balance")
	requireOK(t, f.deliver(alice, &housetypes.MsgUnlockHouseBond{Signer: alice.addr.String()}))
	require.Equal(t, norama(20).SubRaw(feeOf(3)).String(), f.bank(alice.addr, params.BaseDenom).String(), "the bond came back")
}

// ---- x/nodes ----

func TestWalletFlow_registerNodeBondFromEarningsAndFundHotKey(t *testing.T) {
	f := newFlow(t)
	op := f.newWallet()
	f.fundEarnings(op, flowCredit)
	hotPriv := secp256k1.GenPrivKey()
	hot := sdk.AccAddress(hotPriv.PubKey().Address())
	hotSig, err := hotPriv.Sign(nodestypes.BindingSignBytes(testChainID, op.addr.String(), nodestypes.HotKeyService, hotPriv.PubKey().Bytes()))
	require.NoError(t, err)
	torPub, torPriv, err := stded25519.GenerateKey(nil)
	require.NoError(t, err)

	requireOK(t, f.deliver(op,
		&nodestypes.MsgRegisterOperator{Operator: op.addr.String()},
		&nodestypes.MsgRegisterNode{
			Operator: op.addr.String(), NodeId: "wallet-node", Roles: []nodestypes.Role{nodestypes.RoleRelay}, HotKey: hot.String(),
			Bindings: []nodestypes.Binding{{
				Service: nodestypes.HotKeyService, KeyType: nodestypes.KeyTypeSecp256k1, Pubkey: hotPriv.PubKey().Bytes(), Signature: hotSig,
			}, {
				Service: "tor", KeyType: nodestypes.KeyTypeEd25519, Pubkey: torPub,
				Signature: stded25519.Sign(torPriv, nodestypes.BindingSignBytes(testChainID, op.addr.String(), "tor", torPub)),
			}},
			Endpoints: []string{"https://wallet-node.example:443"}, RegionHint: "eu-1",
		}))

	bond := norama(1)
	earningsBefore := f.earnings(op.addr)
	requireOK(t, f.deliver(op, &nodestypes.MsgBondNode{Operator: op.addr.String(), NodeId: "wallet-node", Role: nodestypes.RoleRelay, Amount: bond}))
	node, err := f.app.NodesKeeper.GetNode(f.app.NewContext(true), "wallet-node")
	require.NoError(t, err)
	require.Equal(t, bond.String(), node.Bonds[0].Amount.String(), "the bond is on the node")
	require.True(t, f.bank(op.addr, params.BaseDenom).IsZero(), "the operator held no bank balance: the bond came from earnings")
	require.True(t, earningsBefore.Sub(f.earnings(op.addr)).GTE(bond), "earnings paid the bond")

	fund := norama(3)
	hotEarnings := f.earnings(hot)
	requireOK(t, f.deliver(op, &nodestypes.MsgFundHotKey{Operator: op.addr.String(), NodeId: "wallet-node", Amount: fund}))
	feeBalance, err := f.app.FeesKeeper.GetFeeBalance(f.app.NewContext(true), hot)
	require.NoError(t, err)
	require.True(t, feeBalance.Equal(fund), "the hot key's fee-only balance received the funds")
	require.True(t, f.earnings(hot).Equal(hotEarnings), "nothing reached the hot key's spendable earnings")

	other := f.newWallet()
	f.fundEarnings(other, 10)
	res := f.deliver(other, &nodestypes.MsgFundHotKey{Operator: other.addr.String(), NodeId: "wallet-node", Amount: fund})
	require.NotZero(t, res.Code, "only the node's own operator can fund its hot key")
	f.requireInvariants()
}

func TestWalletFlow_aBindingSignedForAnotherOperatorIsRefused(t *testing.T) {
	f := newFlow(t)
	op, thief := f.newWallet(), f.newWallet()
	f.fundEarnings(thief, flowCredit)
	torPub, torPriv, err := stded25519.GenerateKey(nil)
	require.NoError(t, err)

	res := f.deliver(thief,
		&nodestypes.MsgRegisterOperator{Operator: thief.addr.String()},
		&nodestypes.MsgRegisterNode{
			Operator: thief.addr.String(), NodeId: "stolen", Roles: []nodestypes.Role{nodestypes.RoleRelay},
			HotKey: sdk.AccAddress(secp256k1.GenPrivKey().PubKey().Address()).String(),
			Bindings: []nodestypes.Binding{{
				Service: "tor", KeyType: nodestypes.KeyTypeEd25519, Pubkey: torPub,
				Signature: stded25519.Sign(torPriv, nodestypes.BindingSignBytes(testChainID, op.addr.String(), "tor", torPub)),
			}},
			Endpoints: []string{"https://x.example:443"},
		})
	require.NotZero(t, res.Code, "a binding for another operator must not register the key")
}

// ---- x/token ----

func (f *flow) createToken(creator wallet, sub string, msg *tokentypes.MsgCreateToken) string {
	f.t.Helper()
	msg.Creator, msg.Subdenom = creator.addr.String(), sub
	if msg.Name == "" {
		msg.Name, msg.Symbol = "Token "+sub, strings.ToUpper(sub)
	}
	requireOK(f.t, f.deliver(creator, msg))
	return tokentypes.Denom(creator.addr.String(), sub)
}

func TestWalletFlow_tokenCreateMintTransferBurn(t *testing.T) {
	f := newFlow(t)
	alice, bob := f.newWallet(), f.newWallet()
	f.fundEarnings(alice, flowCredit)
	f.fundBank(alice, flowCredit)
	f.fundEarnings(bob, 10)
	denom := f.createToken(alice, "gold", &tokentypes.MsgCreateToken{Mint: true})

	requireOK(t, f.deliver(alice, &tokentypes.MsgMint{Sender: alice.addr.String(), Denom: denom, Recipient: alice.addr.String(), Amount: math.NewInt(1000)}))
	requireOK(t, f.deliver(alice, &tokentypes.MsgTransfer{Sender: alice.addr.String(), From: alice.addr.String(), To: bob.addr.String(), Denom: denom, Amount: math.NewInt(400)}))
	require.Equal(t, "400", f.bank(bob.addr, denom).String())
	require.Equal(t, "600", f.bank(alice.addr, denom).String())

	requireOK(t, f.deliver(bob, &tokentypes.MsgBurn{Sender: bob.addr.String(), Denom: denom, Amount: math.NewInt(100)}))
	require.Equal(t, "300", f.bank(bob.addr, denom).String())

	res := f.deliver(bob, &tokentypes.MsgMint{Sender: bob.addr.String(), Denom: denom, Recipient: bob.addr.String(), Amount: math.NewInt(1)})
	require.NotZero(t, res.Code, "only the creator mints")
	res = f.deliver(bob, &tokentypes.MsgTransfer{Sender: bob.addr.String(), From: alice.addr.String(), To: bob.addr.String(), Denom: denom, Amount: math.NewInt(1)})
	require.NotZero(t, res.Code, "no one moves another holder's tokens without the delegate power")
	f.requireInvariants()
}

func TestWalletFlow_tokenPowersAreEnforced(t *testing.T) {
	f := newFlow(t)
	alice, bob, carol := f.newWallet(), f.newWallet(), f.newWallet()
	for _, w := range []wallet{alice, bob, carol} {
		f.fundEarnings(w, flowCredit)
	}
	f.fundBank(alice, flowCredit)
	denom := f.createToken(alice, "ice", &tokentypes.MsgCreateToken{Mint: true, Freeze: true, Pause: true, PermanentDelegate: carol.addr.String()})
	requireOK(t, f.deliver(alice, &tokentypes.MsgMint{Sender: alice.addr.String(), Denom: denom, Recipient: bob.addr.String(), Amount: math.NewInt(100)}))

	// freeze: the creator freezes bob, and bob cannot move his tokens
	requireOK(t, f.deliver(alice, &tokentypes.MsgSetFrozen{Sender: alice.addr.String(), Denom: denom, Account: bob.addr.String(), Frozen: true}))
	res := f.deliver(bob, &tokentypes.MsgTransfer{Sender: bob.addr.String(), From: bob.addr.String(), To: alice.addr.String(), Denom: denom, Amount: math.NewInt(1)})
	require.NotZero(t, res.Code, "a frozen holder cannot transfer")
	requireOK(t, f.deliver(alice, &tokentypes.MsgSetFrozen{Sender: alice.addr.String(), Denom: denom, Account: bob.addr.String(), Frozen: false}))
	requireOK(t, f.deliver(bob, &tokentypes.MsgTransfer{Sender: bob.addr.String(), From: bob.addr.String(), To: alice.addr.String(), Denom: denom, Amount: math.NewInt(1)}))

	// pause: nobody transfers while paused
	requireOK(t, f.deliver(alice, &tokentypes.MsgSetPaused{Sender: alice.addr.String(), Denom: denom, Paused: true}))
	res = f.deliver(bob, &tokentypes.MsgTransfer{Sender: bob.addr.String(), From: bob.addr.String(), To: alice.addr.String(), Denom: denom, Amount: math.NewInt(1)})
	require.NotZero(t, res.Code, "a paused token cannot be transferred")
	requireOK(t, f.deliver(alice, &tokentypes.MsgSetPaused{Sender: alice.addr.String(), Denom: denom, Paused: false}))

	// permanent delegate: carol moves bob's tokens
	requireOK(t, f.deliver(carol, &tokentypes.MsgTransfer{Sender: carol.addr.String(), From: bob.addr.String(), To: carol.addr.String(), Denom: denom, Amount: math.NewInt(50)}))
	require.Equal(t, "50", f.bank(carol.addr, denom).String())

	// renounce: after the creator gives up freeze, it can no longer freeze
	requireOK(t, f.deliver(alice, &tokentypes.MsgRenounce{Sender: alice.addr.String(), Denom: denom, Extension: tokentypes.EXTENSION_FREEZE}))
	res = f.deliver(alice, &tokentypes.MsgSetFrozen{Sender: alice.addr.String(), Denom: denom, Account: bob.addr.String(), Frozen: true})
	require.NotZero(t, res.Code, "a renounced power cannot be used")
}

func TestWalletFlow_nonTransferableTokenStaysWhereItWasMinted(t *testing.T) {
	f := newFlow(t)
	alice, bob := f.newWallet(), f.newWallet()
	f.fundEarnings(alice, flowCredit)
	f.fundBank(alice, flowCredit)
	denom := f.createToken(alice, "badge", &tokentypes.MsgCreateToken{Mint: true, NonTransferable: true})
	requireOK(t, f.deliver(alice, &tokentypes.MsgMint{Sender: alice.addr.String(), Denom: denom, Recipient: alice.addr.String(), Amount: math.NewInt(5)}))

	res := f.deliver(alice, &tokentypes.MsgTransfer{Sender: alice.addr.String(), From: alice.addr.String(), To: bob.addr.String(), Denom: denom, Amount: math.NewInt(1)})
	require.NotZero(t, res.Code, "a non-transferable token cannot move")
	require.True(t, f.bank(bob.addr, denom).IsZero())
}

// A creator's own earnings fund its token's creation fee and metadata deposit
// (C2): a wallet with no bank balance can create a token, and one whose
// earnings cannot cover the fee and deposit is refused.
func TestWalletFlow_tokenCreationIsFundedFromEarnings(t *testing.T) {
	f := newFlow(t)
	rich, poor := f.newWallet(), f.newWallet()
	f.fundEarnings(rich, flowCredit)
	f.fundEarnings(poor, 1)

	requireOK(t, f.deliver(rich, &tokentypes.MsgCreateToken{Creator: rich.addr.String(), Subdenom: "fromearnings", Name: "E", Symbol: "E"}))
	res := f.deliver(poor, &tokentypes.MsgCreateToken{Creator: poor.addr.String(), Subdenom: "nofunds", Name: "N", Symbol: "N"})
	requireRejected(t, res, "insufficient")
}

func TestWalletFlow_shieldableIsOneWayOpenToAnyoneAndRefusedForTokensWithPowers(t *testing.T) {
	f := newFlow(t)
	alice, bob := f.newWallet(), f.newWallet()
	for _, w := range []wallet{alice, bob} {
		f.fundEarnings(w, flowCredit)
	}
	f.fundBank(alice, flowCredit)
	plain := f.createToken(alice, "veil", &tokentypes.MsgCreateToken{})
	powered := f.createToken(alice, "frozen", &tokentypes.MsgCreateToken{Freeze: true})

	requireOK(t, f.deliver(bob, &tokentypes.MsgSetShieldable{Sender: bob.addr.String(), Denom: plain}))
	requireOK(t, f.deliver(alice, &tokentypes.MsgSetShieldable{Sender: alice.addr.String(), Denom: plain}))
	res := f.deliver(alice, &tokentypes.MsgSetShieldable{Sender: alice.addr.String(), Denom: powered})
	require.NotZero(t, res.Code, "a token whose creator can freeze holders cannot enter the shielded pool")
	res = f.deliver(alice, &tokentypes.MsgSetShieldable{Sender: alice.addr.String(), Denom: "factory/" + alice.addr.String() + "/missing"})
	require.NotZero(t, res.Code, "a token that does not exist cannot be made shieldable")
}

// ---- x/cnft and x/market ----

func (f *flow) leafBody(creator, owner sdk.AccAddress, asset []byte, cid string, nonce uint64) cnfttypes.Leaf {
	return cnfttypes.Leaf{
		AssetId: asset, Owner: owner.String(), MetadataCid: cid,
		CreatorHash: cnfttypes.CreatorHash(creator), Nonce: nonce, HashId: cnfttypes.HashIDSHA256,
	}
}

func (f *flow) proofFor(leaf cnfttypes.Leaf, depth uint32) cnfttypes.MerkleProof {
	f.t.Helper()
	hash, err := cnfttypes.HashLeaf(leaf)
	require.NoError(f.t, err)
	siblings, root, err := cnfttypes.Proof([][]byte{hash}, 0, depth)
	require.NoError(f.t, err)
	return cnfttypes.MerkleProof{Root: root, Index: 0, Siblings: siblings}
}

func TestWalletFlow_mintTransferListAndBuyACompressedNFT(t *testing.T) {
	const depth = uint32(4)
	f := newFlow(t)
	creator, seller, buyer, thief := f.newWallet(), f.newWallet(), f.newWallet(), f.newWallet()
	for _, w := range []wallet{creator, seller, buyer, thief} {
		f.fundEarnings(w, flowCredit)
	}
	price := norama(10)
	f.fundBank(buyer, 20)

	requireOK(t, f.deliver(creator, &cnfttypes.MsgCreateCollection{Creator: creator.addr.String(), Name: "art", RoyaltyBps: 500}))
	requireOK(t, f.deliver(creator, &cnfttypes.MsgCreateTree{Creator: creator.addr.String(), CollectionId: 1, Depth: depth, Buffer: 8}))
	tree, err := f.app.CnftKeeper.GetTree(f.app.NewContext(true), 1)
	require.NoError(t, err)

	asset := []byte("0123456789abcdef0123456789abcdef")
	minted := f.leafBody(creator.addr, seller.addr, asset, "bafy-art", 0)
	requireOK(t, f.deliver(creator, &cnfttypes.MsgMint{
		Creator: creator.addr.String(), TreeId: 1, Root: tree.Root(),
		Leaves: []cnfttypes.MintLeaf{{AssetId: asset, Owner: seller.addr.String(), MetadataCid: "bafy-art"}},
	}))

	// only the owner transfers: a thief holding a valid proof is refused
	res := f.deliver(thief, &cnfttypes.MsgTransfer{Signer: thief.addr.String(), TreeId: 1, Current: minted, NewOwner: thief.addr.String(), Proof: f.proofFor(minted, depth)})
	require.NotZero(t, res.Code, "a non-owner cannot transfer the asset")

	// the owner lists it, transfers nothing yet, and the buyer settles at the list price
	requireOK(t, f.deliver(seller, &markettypes.MsgList{Seller: seller.addr.String(), TreeId: 1, Leaf: minted, Proof: f.proofFor(minted, depth), Price: price}))
	sellerBefore, creatorBefore := f.earnings(seller.addr), f.earnings(creator.addr)
	requireOK(t, f.deliver(buyer, &markettypes.MsgSettle{Signer: buyer.addr.String(), ListingId: 1, Leaf: minted, Proof: f.proofFor(minted, depth)}))

	royalty := price.MulRaw(500).QuoRaw(10_000)
	require.True(t, f.earnings(creator.addr).Sub(creatorBefore).Equal(royalty), "the royalty is paid to the collection creator's earnings")
	require.True(t, f.earnings(seller.addr).Sub(sellerBefore).Equal(price.Sub(royalty)), "the seller's earnings get the rest")
	require.Equal(t, norama(20).Sub(price).SubRaw(feeOf(1)).String(), f.bank(buyer.addr, params.BaseDenom).String(), "the buyer paid the price and the fee from the bank")

	bought := minted
	bought.Owner, bought.Delegate, bought.Nonce = buyer.addr.String(), "", 1
	_, err = f.app.CnftKeeper.ProveOwned(f.app.NewContext(true), 1, bought, f.proofFor(bought, depth), buyer.addr)
	require.NoError(t, err, "the buyer owns the asset after settlement")
	f.requireInvariants()
}

func TestWalletFlow_transferACompressedNFTToAnotherWallet(t *testing.T) {
	const depth = uint32(4)
	f := newFlow(t)
	creator, alice, bob := f.newWallet(), f.newWallet(), f.newWallet()
	for _, w := range []wallet{creator, alice} {
		f.fundEarnings(w, flowCredit)
	}
	requireOK(t, f.deliver(creator, &cnfttypes.MsgCreateCollection{Creator: creator.addr.String(), Name: "art", RoyaltyBps: 0}))
	requireOK(t, f.deliver(creator, &cnfttypes.MsgCreateTree{Creator: creator.addr.String(), CollectionId: 1, Depth: depth, Buffer: 8}))
	tree, err := f.app.CnftKeeper.GetTree(f.app.NewContext(true), 1)
	require.NoError(t, err)
	asset := []byte("fedcba9876543210fedcba9876543210")
	minted := f.leafBody(creator.addr, alice.addr, asset, "bafy-1", 0)
	requireOK(t, f.deliver(creator, &cnfttypes.MsgMint{
		Creator: creator.addr.String(), TreeId: 1, Root: tree.Root(),
		Leaves: []cnfttypes.MintLeaf{{AssetId: asset, Owner: alice.addr.String(), MetadataCid: "bafy-1"}},
	}))

	requireOK(t, f.deliver(alice, &cnfttypes.MsgTransfer{Signer: alice.addr.String(), TreeId: 1, Current: minted, NewOwner: bob.addr.String(), Proof: f.proofFor(minted, depth)}))

	moved := minted
	moved.Owner, moved.Nonce = bob.addr.String(), 1
	_, err = f.app.CnftKeeper.ProveOwned(f.app.NewContext(true), 1, moved, f.proofFor(moved, depth), bob.addr)
	require.NoError(t, err, "the new owner holds the asset")

	// the old proof is stale: replaying the same transfer must fail
	res := f.deliver(alice, &cnfttypes.MsgTransfer{Signer: alice.addr.String(), TreeId: 1, Current: minted, NewOwner: alice.addr.String(), Proof: f.proofFor(minted, depth)})
	require.NotZero(t, res.Code, "a transferred asset cannot be transferred again by its old owner")
}

// ---- ordering and replay ----

func TestWalletFlow_aReplayedTransactionIsRefused(t *testing.T) {
	f := newFlow(t)
	alice := f.newWallet()
	f.fundEarnings(alice, flowCredit)
	acct := f.app.AccountKeeper.GetAccount(f.app.NewContext(true), alice.addr)
	raw, err := f.builder.Build(alice.acc, oramatx.Unsigned{
		ChainID: testChainID, AccountNumber: acct.GetAccountNumber(), Sequence: acct.GetSequence(),
		GasLimit: flowGas, Fee: sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, int64(flowGas))),
		Msgs: []sdk.Msg{&nodestypes.MsgRegisterOperator{Operator: alice.addr.String()}},
	})
	require.NoError(t, err)

	f.height++
	first := finalize(t, f.app, f.height, f.at(f.height), raw)
	requireOK(t, first.TxResults[0])
	f.height++
	second := finalize(t, f.app, f.height, f.at(f.height), raw)
	require.NotZero(t, second.TxResults[0].Code, "the same signed bytes must not apply twice")
}

func TestWalletFlow_aTransactionForAnotherChainIsRefused(t *testing.T) {
	f := newFlow(t)
	alice := f.newWallet()
	f.fundEarnings(alice, flowCredit)
	acct := f.app.AccountKeeper.GetAccount(f.app.NewContext(true), alice.addr)
	raw, err := f.builder.Build(alice.acc, oramatx.Unsigned{
		ChainID: "some-other-chain-1", AccountNumber: acct.GetAccountNumber(), Sequence: acct.GetSequence(),
		GasLimit: flowGas, Fee: sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, int64(flowGas))),
		Msgs: []sdk.Msg{&nodestypes.MsgRegisterOperator{Operator: alice.addr.String()}},
	})
	require.NoError(t, err)
	f.height++
	resp := finalize(t, f.app, f.height, f.at(f.height), raw)
	require.NotZero(t, resp.TxResults[0].Code, "a signature over another chain id must not verify")
}

// the harness builds wallets whose address prefix is the chain's
func TestWalletFlow_walletAddressesAreOramaAddresses(t *testing.T) {
	f := newFlow(t)
	w := f.newWallet()
	require.True(t, strings.HasPrefix(w.acc.Address, params.Bech32Prefix+"1"))
	require.Equal(t, w.addr.String(), w.acc.Address)
}
