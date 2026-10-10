package app_test

import (
	"bytes"
	stded25519 "crypto/ed25519"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/piece"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// earningsFixture is a chain with one signer that holds only earnings: no bank balance, the case
// the bond top-up exists for. gas x base fee 1 is what each of its transactions pays as a fee.
type earningsFixture struct {
	app     *app.OramaApp
	key     cryptotypes.PrivKey
	addr    sdk.AccAddress
	genesis time.Time
	height  int64
	credit  math.Int
	gas     uint64
	cons    cryptotypes.PubKey
}

func newEarningsFixture(t *testing.T, credit int64) *earningsFixture {
	t.Helper()
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, keys := committeeGenesis(t, oramaApp, 1, 365)
	key := ed25519.GenPrivKey()
	addAuthAccount(t, oramaApp, genState, key)
	initChain(t, oramaApp, genState, 4_000_000, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	f := &earningsFixture{
		app: oramaApp, key: key, addr: sdk.AccAddress(key.PubKey().Address()), genesis: genesisTime,
		height: 2, credit: math.NewInt(credit), gas: 300_000, cons: &ed25519.PubKey{Key: keys[0].cons.PubKey().Bytes()},
	}
	fundCtx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second)})
	require.NoError(t, oramaApp.BankKeeper.MintCoins(fundCtx, emissiontypes.ModuleName, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, f.credit))))
	require.NoError(t, oramaApp.FeesKeeper.CreditEarnings(fundCtx, emissiontypes.ModuleName, f.addr, sdk.NewCoin(params.BaseDenom, f.credit)))
	writeCache(t, fundCtx)
	_, err := oramaApp.Commit()
	require.NoError(t, err)
	return f
}

// run signs msgs, finalizes them in the next block and returns the first result.
func (f *earningsFixture) run(t *testing.T, seed int64, msgs ...sdk.Msg) (code uint32, log string) {
	t.Helper()
	f.height++
	tx := signedTx(t, f.app, f.key, seed, f.gas, msgs...)
	resp := finalize(t, f.app, f.height, f.genesis.Add(time.Duration(f.height)*2*time.Second), tx)
	return resp.TxResults[0].Code, resp.TxResults[0].Log
}

// requireOnlyFeesSpent checks that the tx failed and cost the signer its fee and nothing else:
// no earnings were turned into a bank balance, and the ledgers still match the module accounts.
func (f *earningsFixture) requireOnlyFeesSpent(t *testing.T, code uint32, log string, txs int64) {
	t.Helper()
	require.NotZero(t, code, "the message must fail for this test to mean anything")
	ctx := f.app.NewContext(true)
	earnings, err := f.app.FeesKeeper.GetEarnings(ctx, f.addr)
	require.NoError(t, err)
	// A message that fails in its handler still pays its fee; one an ante decorator refuses pays
	// nothing. Either way earnings fall by no more than the fee.
	floor := f.credit.Sub(math.NewInt(int64(f.gas) * txs))
	require.True(t, earnings.GTE(floor) && earnings.LTE(f.credit),
		"earnings = %s, want between %s and %s (at most the fee of %d failed tx): %s", earnings, floor, f.credit, txs, log)
	require.True(t, f.app.BankKeeper.GetBalance(ctx, f.addr, params.BaseDenom).Amount.IsZero(),
		"a failed message left earnings in the signer's bank balance")
	inv, err := f.app.FeesKeeper.CheckInvariants(ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule && inv.DepositsMatchModule && inv.FeesBalance, inv.Detail)
}

func TestApp_failedDelegateDoesNotTurnEarningsIntoBank(t *testing.T) {
	bond := math.NewInt(1_000_000_000)
	f := newEarningsFixture(t, 2_000_000_000)
	missing := sdk.ValAddress(ed25519.GenPrivKey().PubKey().Address())

	code, log := f.run(t, 5, stakingtypes.NewMsgDelegate(f.addr.String(), missing.String(), sdk.NewCoin(params.BaseDenom, bond)))
	f.requireOnlyFeesSpent(t, code, log, 1)
}

func TestApp_failedCreateValidatorDoesNotTurnEarningsIntoBank(t *testing.T) {
	bond := math.NewInt(1_000_000_000)
	f := newEarningsFixture(t, 2_000_000_000)
	// The consensus key of the bootstrap member is taken, so the staking handler refuses this.
	msg, err := stakingtypes.NewMsgCreateValidator(
		sdk.ValAddress(f.addr).String(), f.cons, sdk.NewCoin(params.BaseDenom, bond),
		stakingtypes.NewDescription("dup", "", "", "", ""),
		stakingtypes.NewCommissionRates(math.LegacyNewDecWithPrec(1, 1), math.LegacyNewDecWithPrec(2, 1), math.LegacyNewDecWithPrec(1, 2)),
		math.OneInt(),
	)
	require.NoError(t, err)

	code, log := f.run(t, 6, msg)
	f.requireOnlyFeesSpent(t, code, log, 1)
}

func TestApp_aLaterFailingMessageRevertsAnEarlierTopUpInTheSameTx(t *testing.T) {
	bond := math.NewInt(1_000_000_000)
	f := newEarningsFixture(t, 3_000_000_000)
	missing := sdk.ValAddress(ed25519.GenPrivKey().PubKey().Address())
	// Both delegations need funding from earnings; the second is to a validator that does not
	// exist. The whole transaction fails, so the first delegation and its top-up go too.
	self := sdk.ValAddress(f.addr)
	first := stakingtypes.NewMsgDelegate(f.addr.String(), self.String(), sdk.NewCoin(params.BaseDenom, bond))
	second := stakingtypes.NewMsgDelegate(f.addr.String(), missing.String(), sdk.NewCoin(params.BaseDenom, bond))

	code, log := f.run(t, 7, first, second)
	f.requireOnlyFeesSpent(t, code, log, 1)
}

func TestApp_failedBondNodeDoesNotTurnEarningsIntoBank(t *testing.T) {
	f := newEarningsFixture(t, 4_000_000_000)
	bond := math.NewInt(1_000_000_000)

	torPub, torPriv, err := stded25519.GenerateKey(nil)
	require.NoError(t, err)
	hotPriv := secp256k1.GenPrivKey()
	hot := sdk.AccAddress(hotPriv.PubKey().Address())
	hotSig, err := hotPriv.Sign(nodestypes.BindingSignBytes(testChainID, f.addr.String(), nodestypes.HotKeyService, hotPriv.PubKey().Bytes()))
	require.NoError(t, err)
	code, log := f.run(t, 8,
		&nodestypes.MsgRegisterOperator{Operator: f.addr.String()},
		&nodestypes.MsgRegisterNode{
			Operator: f.addr.String(), NodeId: "node-1", Roles: []nodestypes.Role{nodestypes.RoleRelay}, HotKey: hot.String(),
			Bindings: []nodestypes.Binding{{
				Service: "tor", KeyType: nodestypes.KeyTypeEd25519, Pubkey: torPub,
				Signature: stded25519.Sign(torPriv, nodestypes.BindingSignBytes(testChainID, f.addr.String(), "tor", torPub)),
			}, {
				Service: nodestypes.HotKeyService, KeyType: nodestypes.KeyTypeSecp256k1, Pubkey: hotPriv.PubKey().Bytes(), Signature: hotSig,
			}},
			Endpoints: []string{"https://node.example:443"},
		})
	require.Zero(t, code, log)
	after := func() math.Int {
		e, err := f.app.FeesKeeper.GetEarnings(f.app.NewContext(true), f.addr)
		require.NoError(t, err)
		return e
	}
	beforeBond := after()

	// A role the node does not have, a node that does not exist, and a retired node: each is a
	// bond the handler rejects, and none may leave a bank balance behind.
	rejected := []sdk.Msg{
		&nodestypes.MsgBondNode{Operator: f.addr.String(), NodeId: "node-1", Role: nodestypes.RoleStorage, Amount: bond},
		&nodestypes.MsgBondNode{Operator: f.addr.String(), NodeId: "ghost", Role: nodestypes.RoleRelay, Amount: bond},
	}
	for i, msg := range rejected {
		code, log = f.run(t, int64(20+i), msg)
		require.NotZero(t, code, log)
	}
	code, log = f.run(t, 30, &nodestypes.MsgRetireNode{Operator: f.addr.String(), NodeId: "node-1"})
	require.Zero(t, code, log)
	code, log = f.run(t, 31, &nodestypes.MsgBondNode{Operator: f.addr.String(), NodeId: "node-1", Role: nodestypes.RoleRelay, Amount: bond})
	require.NotZero(t, code, log)

	ctx := f.app.NewContext(true)
	require.True(t, f.app.BankKeeper.GetBalance(ctx, f.addr, params.BaseDenom).Amount.IsZero(), "a rejected bond left earnings in the bank")
	spentOnFees := beforeBond.Sub(after())
	// four transactions after registration, each paying gas x base fee 1; the retire also refunds
	// the node's deposit (99%), which lands in earnings, so only an upper bound on the loss holds.
	require.True(t, spentOnFees.LTE(math.NewInt(4*int64(f.gas))), "earnings fell by %s, more than the fees of the four txs", spentOnFees)
	inv, err := f.app.FeesKeeper.CheckInvariants(ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule && inv.DepositsMatchModule && inv.FeesBalance, inv.Detail)
}

func (f *earningsFixture) dealMsg(t *testing.T, epochs uint64) *storagetypes.MsgCreateDeal {
	t.Helper()
	commitment, err := piece.Commit(make([]byte, 2048))
	require.NoError(t, err)
	return &storagetypes.MsgCreateDeal{
		Signer: f.addr.String(), Class: storagetypes.DealClass_DEAL_CLASS_PUBLIC_PIN,
		DealNonce: bytes.Repeat([]byte{1}, storagetypes.NonceLen), Replicas: storagetypes.MinReplicas,
		PricePerEpoch: math.NewInt(1000), DurationEpochs: epochs,
		Pieces: []storagetypes.PieceCommitment{{
			Root: commitment.Root, RealLeafCount: commitment.RealLeafCount,
			PaddedLeafCount: commitment.PaddedLeafCount, PieceBytes: 2048,
		}},
	}
}

// failing is a message the handler rejects, used to fail a transaction after an earlier message
// in it has already topped up from earnings.
func (f *earningsFixture) failing() sdk.Msg {
	return &storagetypes.MsgExtendDeal{Signer: f.addr.String(), DealId: 987654, ExtraEpochs: 1}
}

func TestApp_failedCreateDealRevertsItsEarningsTopUp(t *testing.T) {
	f := newEarningsFixture(t, 1_000_000_000)
	code, log := f.run(t, 40, f.dealMsg(t, 2), f.failing())
	f.requireOnlyFeesSpent(t, code, log, 1)
	_, err := f.app.StorageKeeper.Deals.Get(f.app.NewContext(true), 1)
	require.Error(t, err, "the deal of the failed transaction was reverted with its top-up")
}

func TestApp_failedExtendDealRevertsItsEarningsTopUp(t *testing.T) {
	f := newEarningsFixture(t, 1_000_000_000)
	code, log := f.run(t, 41, f.dealMsg(t, 2))
	require.Zero(t, code, log)
	after := func() math.Int {
		e, err := f.app.FeesKeeper.GetEarnings(f.app.NewContext(true), f.addr)
		require.NoError(t, err)
		return e
	}
	before := after()
	extend := &storagetypes.MsgExtendDeal{Signer: f.addr.String(), DealId: 1, ExtraEpochs: 3}
	code, log = f.run(t, 42, extend, f.failing())
	require.NotZero(t, code, log)
	require.True(t, before.Sub(after()).LTE(math.NewInt(int64(f.gas))), "only the fee left the earnings, not the extension's escrow")
	require.True(t, f.app.BankKeeper.GetBalance(f.app.NewContext(true), f.addr, params.BaseDenom).Amount.IsZero())
}

func TestApp_failedCreateTokenRevertsItsEarningsTopUp(t *testing.T) {
	f := newEarningsFixture(t, 10_000_000_000_000)
	msg := &tokentypes.MsgCreateToken{Creator: f.addr.String(), Subdenom: "gold", Name: "Gold", Symbol: "GLD"}
	code, log := f.run(t, 43, msg, f.failing())
	f.requireOnlyFeesSpent(t, code, log, 1)
	has, err := f.app.TokenKeeper.Tokens.Has(f.app.NewContext(true), "factory/"+f.addr.String()+"/gold")
	require.NoError(t, err)
	require.False(t, has, "the token of the failed transaction was reverted")
}
