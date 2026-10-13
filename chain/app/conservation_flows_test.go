package app_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	oramatx "github.com/DeBrosOfficial/network/chain/client/tx"
	"github.com/DeBrosOfficial/network/chain/piece"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	storagekeeper "github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// A false positive of the conservation check halts the chain, so each flow below that moves coins through
// a place the check reads (the staking pools, an unbonding queue, a slash burn, a storage escrow, a
// contract's balance, the market) is driven through real blocks and the full pass is run after every one.
//
// The chain checks itself every min_blocks_per_epoch blocks. Each flow runs with the pass at every block
// (cadence 1) and with the pass at every seventh (cadence 7, the production shape: the chain itself looks
// at some heights only, while the explicit pass in these tests looks at all of them). A production chain's
// cadence is 14,400; TestConservation_productionCadenceReachesItsFirstFullPass runs that one.
var conservationCadences = []uint64{1, 7}

func forEachCadence(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	for _, cadence := range conservationCadences {
		t.Run(fmt.Sprintf("cadence=%d", cadence), func(t *testing.T) {
			withCadence(t, cadence)
			fn(t)
		})
	}
}

const shortUnbonding = 10 * time.Second

// newUnbondingFlow is a wallet flow on a chain whose unbonding time is shortUnbonding, so a test sees
// an unbonding entry mature.
func newUnbondingFlow(t *testing.T, unbonding time.Duration) *flow {
	t.Helper()
	b, err := oramatx.New(buildTestApp(t).TxConfig())
	require.NoError(t, err)
	wc := newWiringChainGenesis(t, nil, func(a *app.OramaApp, genState app.GenesisState) {
		var staking stakingtypes.GenesisState
		a.AppCodec().MustUnmarshalJSON(genState[stakingtypes.ModuleName], &staking)
		staking.Params.UnbondingTime = unbonding
		genState[stakingtypes.ModuleName] = a.AppCodec().MustMarshalJSON(&staking)
	})
	return &flow{wiringChain: wc, builder: b}
}

// step finalizes n empty blocks and runs the full pass after each.
func (f *flow) step(n int) {
	f.t.Helper()
	for i := 0; i < n; i++ {
		f.blocks(1)
		requireConserved(f.t, f.app)
	}
}

// do delivers a transaction that must succeed and runs the full pass after its block.
func (f *flow) do(w wallet, msgs ...sdk.Msg) {
	f.t.Helper()
	requireOK(f.t, f.deliver(w, msgs...))
	requireConserved(f.t, f.app)
}

func TestConservation_unbondingDelegationsHoldThenMatureAndPayOut(t *testing.T) {
	forEachCadence(t, func(t *testing.T) {
		f := newUnbondingFlow(t, shortUnbonding)
		alice, bob := f.newWallet(), f.newWallet()
		f.fundBank(alice, 50)
		f.fundBank(bob, 50)
		val := f.committeeValidator()
		coin := func(orama int64) sdk.Coin { return sdk.NewCoin(params.BaseDenom, norama(orama)) }

		f.do(alice, stakingtypes.NewMsgDelegate(alice.addr.String(), val.String(), coin(10)))
		f.do(bob, stakingtypes.NewMsgDelegate(bob.addr.String(), val.String(), coin(8)))
		// Two entries for alice, made in different blocks, and one for bob: three claims on the not-bonded pool.
		f.do(alice, stakingtypes.NewMsgUndelegate(alice.addr.String(), val.String(), coin(3)))
		f.do(alice, stakingtypes.NewMsgUndelegate(alice.addr.String(), val.String(), coin(2)))
		f.do(bob, stakingtypes.NewMsgUndelegate(bob.addr.String(), val.String(), coin(8)))

		ctx := f.app.NewContext(true)
		ubd, err := f.app.StakingKeeper.GetUnbondingDelegation(ctx, alice.addr, val)
		require.NoError(t, err)
		require.Len(t, ubd.Entries, 2)
		notBonded := f.app.BankKeeper.GetBalance(ctx, f.app.StakingKeeper.GetNotBondedPool(ctx).GetAddress(), params.BaseDenom).Amount
		require.True(t, notBonded.GTE(norama(13)), "the not-bonded pool holds the 13 ORAMA being unbonded, has %s", notBonded)
		aliceBefore := f.bank(alice.addr, params.BaseDenom)

		f.step(int(shortUnbonding/(2*time.Second)) + 3) // the entries mature in the end-blocker of these blocks

		_, err = f.app.StakingKeeper.GetUnbondingDelegation(f.app.NewContext(true), alice.addr, val)
		require.Error(t, err, "the matured entries are gone")
		require.True(t, f.bank(alice.addr, params.BaseDenom).Sub(aliceBefore).Equal(norama(5)), "alice is paid the 5 ORAMA she unbonded")
		f.step(2)
	})
}

func TestConservation_slashBurnsThroughTheBondedAndNotBondedPools(t *testing.T) {
	forEachCadence(t, func(t *testing.T) {
		f := newFlow(t)
		alice := f.newWallet()
		f.fundBank(alice, 50)
		val := f.committeeValidator()
		coin := func(orama int64) sdk.Coin { return sdk.NewCoin(params.BaseDenom, norama(orama)) }

		f.do(alice, stakingtypes.NewMsgDelegate(alice.addr.String(), val.String(), coin(10)))
		infractionHeight := f.height
		// Stake that unbonds after the infraction is slashed too: that burn comes out of the not-bonded pool.
		f.do(alice, stakingtypes.NewMsgUndelegate(alice.addr.String(), val.String(), coin(4)))

		ctx := f.app.NewContext(true)
		validator, err := f.app.StakingKeeper.GetValidator(ctx, val)
		require.NoError(t, err)
		consAddr, err := validator.GetConsAddr()
		require.NoError(t, err)
		tokensBefore := validator.Tokens
		bondedPool := f.app.StakingKeeper.GetBondedPool(ctx).GetAddress()
		bondedBefore := f.app.BankKeeper.GetBalance(ctx, bondedPool, params.BaseDenom).Amount

		f.write(func(ctx sdk.Context) {
			require.NoError(t, f.app.SlashingKeeper.SlashWithInfractionReason(
				ctx, consAddr, math.LegacyNewDecWithPrec(5, 2), 1, infractionHeight, stakingtypes.Infraction_INFRACTION_DOUBLE_SIGN))
		})
		// The burn is booked with x/emission in the next end-blocker; the block after must not halt on it.
		f.step(3)

		ctx = f.app.NewContext(true)
		validator, err = f.app.StakingKeeper.GetValidator(ctx, val)
		require.NoError(t, err)
		require.True(t, validator.Tokens.LT(tokensBefore), "the validator's tokens were slashed")
		ubd, err := f.app.StakingKeeper.GetUnbondingDelegation(ctx, alice.addr, val)
		require.NoError(t, err)
		require.True(t, ubd.Entries[0].Balance.LT(norama(4)), "the unbonding entry was slashed: %s", ubd.Entries[0].Balance)
		require.True(t, f.app.BankKeeper.GetBalance(ctx, bondedPool, params.BaseDenom).Amount.LT(bondedBefore), "the slash burned coins out of the bonded pool")
	})
}

func TestConservation_storageDealEscrowHoldsFromCreationThroughPayout(t *testing.T) {
	t.Run("cadence=1", func(t *testing.T) {
		const price = 1000
		c := newWiringChain(t)
		nodes := map[string]wiringNode{}
		for _, n := range []wiringNode{
			c.addStorageNode("s1", "https://45.33.100.10:443", 15169),
			c.addStorageNode("s2", "https://93.184.113.10:443", 13335),
			c.addStorageNode("s3", "https://151.101.2.10:443", 16509),
		} {
			nodes[n.id] = n
		}
		c.blocks(1)
		requireConserved(t, c.app)

		data := protocolPayload(9)
		client := sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address())
		var dealID uint64
		c.write(func(ctx sdk.Context) {
			funds := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(params.NoramaPerOrama).MulRaw(10)))
			require.NoError(t, mintNoramaForTest(c.app, ctx, funds))
			require.NoError(t, c.app.BankKeeper.SendCoinsFromModuleToAccount(ctx, emissiontypes.ModuleName, client, funds))
			commitment, err := piece.Commit(data)
			require.NoError(t, err)
			nonce := make([]byte, storagetypes.NonceLen)
			nonce[0] = 5
			res, err := storagekeeper.NewMsgServer(c.app.StorageKeeper).CreateDeal(ctx, &storagetypes.MsgCreateDeal{
				Signer: client.String(), Class: storagetypes.DealClass_DEAL_CLASS_PUBLIC_PIN, DealNonce: nonce,
				Replicas: 3, PricePerEpoch: math.NewInt(price), DurationEpochs: 100,
				Pieces: []storagetypes.PieceCommitment{{
					Root: commitment.Root, RealLeafCount: commitment.RealLeafCount, PaddedLeafCount: commitment.PaddedLeafCount, PieceBytes: uint64(len(data)),
				}},
			})
			require.NoError(t, err)
			dealID = res.DealId
		})
		c.blocks(1)
		requireConserved(t, c.app) // the whole escrow is in the module account, the ledger says the same
		escrow := c.app.BankKeeper.GetBalance(c.app.NewContext(true), authtypes.NewModuleAddress(storagetypes.EscrowModuleName), params.BaseDenom).Amount
		require.True(t, escrow.IsPositive(), "the customer's escrow is held")

		c.acceptSlots(dealID, nodes)
		c.blocks(1)
		requireConserved(t, c.app)
		for round := 0; round < 3; round++ {
			c.blocks(1)
			requireConserved(t, c.app)
			c.proveEverySlot(dealID, data, nodes)
			c.blocks(1)
			requireConserved(t, c.app) // payouts to operators' earnings, the burn and the archive fund
		}
		c.blocks(2)
		c.requireModuleInvariants()
	})
}

// The market (a listing settled with the buyer's bank balance, the royalty and the seller's proceeds
// credited to earnings), the token factory, delegation from earnings and a node bond are the wallet-flow
// suite's; it ends each with the full pass (flow.requireInvariants). Here they run on a chain whose own
// pass runs at every seventh block, the production shape, and not only at every block.
func TestConservation_walletFlowsOnAChainThatChecksItselfEverySeventhBlock(t *testing.T) {
	flows := map[string]func(*testing.T){
		"market list and settle":    TestWalletFlow_mintTransferListAndBuyACompressedNFT,
		"token create mint burn":    TestWalletFlow_tokenCreateMintTransferBurn,
		"node bond from earnings":   TestWalletFlow_registerNodeBondFromEarningsAndFundHotKey,
		"token powers on bank send": TestWalletFlow_tokenPowersHoldOnABankSend,
	}
	for name, flow := range flows {
		t.Run(name, func(t *testing.T) {
			withCadence(t, 7)
			flow(t)
		})
	}
}

// A production chain runs its own full pass every 14,400 blocks. This drives a chain with that setting through
// the flows above, then on to the first height the chain itself runs the pass, to show the pass the chain runs
// there agrees with the one the tests ran. It takes minutes under the race detector, so it runs only with
// -sim.production-cadence (make chain-sim passes it).
func TestConservation_productionCadenceReachesItsFirstFullPass(t *testing.T) {
	if !*simProductionCadence {
		t.Skip("run with -sim.production-cadence (make chain-sim does)")
	}
	const productionBlocks = 14_400
	withCadence(t, productionBlocks)
	f := newUnbondingFlow(t, 21*24*time.Hour) // a production unbonding time: the entries are still queued
	alice := f.newWallet()
	f.fundBank(alice, 50)
	val := f.committeeValidator()
	coin := sdk.NewCoin(params.BaseDenom, norama(5))
	f.do(alice, stakingtypes.NewMsgDelegate(alice.addr.String(), val.String(), coin))
	f.do(alice, stakingtypes.NewMsgUndelegate(alice.addr.String(), val.String(), sdk.NewCoin(params.BaseDenom, norama(2))))

	for f.height < productionBlocks {
		f.blocks(1) // the chain's own end-block check runs the full pass at height 14,400 and halts the block on a violation
	}
	require.Equal(t, int64(productionBlocks), f.height)
	requireConserved(t, f.app)
}
