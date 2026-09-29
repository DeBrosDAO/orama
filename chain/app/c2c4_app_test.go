package app_test

import (
	stded25519 "crypto/ed25519"
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
)

type committeeKey struct {
	account cryptotypes.PrivKey
	cons    cmted25519.PrivKey
}

func committeeGenesis(t *testing.T, oramaApp *app.OramaApp, n int, deadline uint64) (app.GenesisState, []committeeKey) {
	t.Helper()
	genState := app.NewDefaultGenesisState(oramaApp)
	keys := make([]committeeKey, n)
	members := make([]powertypes.BootstrapMember, n)
	for i := 0; i < n; i++ {
		keys[i] = committeeKey{account: ed25519.GenPrivKey(), cons: cmted25519.GenPrivKey()}
		members[i] = powertypes.BootstrapMember{
			OperatorAddress: sdk.AccAddress(keys[i].account.PubKey().Address()).String(),
			Moniker:         "committee",
			ConsensusPubkey: keys[i].cons.PubKey().Bytes(),
		}
	}
	powerGen := powertypes.DefaultGenesisState()
	powerGen.Params.MinCommitteeSize = 1
	powerGen.Params.BootstrapDeadlineEpochs = deadline
	powerGen.BootstrapCommittee = members
	genState[powertypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(powerGen)

	emissionGen := emissiontypes.DefaultGenesisState()
	emissionGen.Params = emissiontypes.NewParams(2*time.Second, 1, true)
	genState[emissiontypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(emissionGen)

	return genState, keys
}

func initChain(t *testing.T, oramaApp *app.OramaApp, genState app.GenesisState, maxGas int64, genesisTime time.Time) {
	t.Helper()
	stateBytes, err := json.Marshal(genState)
	require.NoError(t, err)
	req := &abci.RequestInitChain{
		ChainId:       testChainID,
		InitialHeight: 1,
		Time:          genesisTime,
		AppStateBytes: stateBytes,
	}
	if maxGas > 0 {
		req.ConsensusParams = &cmtproto.ConsensusParams{
			Block:     &cmtproto.BlockParams{MaxGas: maxGas, MaxBytes: 22_020_096},
			Evidence:  &cmtproto.EvidenceParams{MaxAgeNumBlocks: 100_000, MaxAgeDuration: time.Hour, MaxBytes: 1_048_576},
			Validator: &cmtproto.ValidatorParams{PubKeyTypes: []string{cmted25519.KeyType}},
		}
	}
	_, err = oramaApp.InitChain(req)
	require.NoError(t, err)
}

func finalize(t *testing.T, oramaApp *app.OramaApp, height int64, blockTime time.Time, txs ...[]byte) *abci.ResponseFinalizeBlock {
	t.Helper()
	resp, err := oramaApp.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: height,
		Time:   blockTime,
		Txs:    txs,
	})
	require.NoError(t, err)
	_, err = oramaApp.Commit()
	require.NoError(t, err)
	return resp
}

func TestApp_doubleSignSlashesFivePercentOfRealTokens(t *testing.T) {
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, keys := committeeGenesis(t, oramaApp, 1, 365)
	initChain(t, oramaApp, genState, 0, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	ctx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second)})
	valAddr := sdk.ValAddress(keys[0].account.PubKey().Address())
	validator, err := oramaApp.StakingKeeper.GetValidator(ctx, valAddr)
	require.NoError(t, err)
	consAddr, err := validator.GetConsAddr()
	require.NoError(t, err)
	before := validator.Tokens
	require.True(t, before.IsPositive())
	require.True(t, before.Mod(math.NewInt(1_000_000)).IsZero(), "slash base is exact only when tokens divide PowerReduction")

	// The power argument is the CometBFT scale (1e9), orders of magnitude above the
	// token-derived power. The slash must ignore it and burn 5% of real tokens.
	err = oramaApp.SlashingKeeper.SlashWithInfractionReason(
		ctx, consAddr, math.LegacyNewDecWithPrec(5, 2), 1_000_000_000_000, 2,
		stakingtypes.Infraction_INFRACTION_DOUBLE_SIGN,
	)
	require.NoError(t, err)

	after, err := oramaApp.StakingKeeper.GetValidator(ctx, valAddr)
	require.NoError(t, err)
	wantBurned := before.MulRaw(5).QuoRaw(100)
	require.True(t, before.Sub(after.Tokens).Equal(wantBurned),
		"burned %s, want exactly 5%% of %s (%s)", before.Sub(after.Tokens), before, wantBurned)

	require.NoError(t, oramaApp.EmissionKeeper.ReconcileBurns(ctx))
	_, broken := oramaApp.EmissionKeeper.CheckSupplyInvariant(ctx)
	require.False(t, broken)
}

func TestApp_doubleSignStillSlashesStakeThatUnbondedAfterTheInfraction(t *testing.T) {
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, keys := committeeGenesis(t, oramaApp, 1, 365)
	initChain(t, oramaApp, genState, 0, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	ctx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second)})
	valAddr := sdk.ValAddress(keys[0].account.PubKey().Address())
	delAddr := sdk.AccAddress(valAddr)
	validator, err := oramaApp.StakingKeeper.GetValidator(ctx, valAddr)
	require.NoError(t, err)
	consAddr, err := validator.GetConsAddr()
	require.NoError(t, err)
	half := validator.DelegatorShares.QuoInt64(2)
	_, unbonded, err := oramaApp.StakingKeeper.Undelegate(ctx, delAddr, valAddr, half)
	require.NoError(t, err)
	require.True(t, unbonded.IsPositive())

	bondedAfter, err := oramaApp.StakingKeeper.GetValidator(ctx, valAddr)
	require.NoError(t, err)
	bonded := bondedAfter.Tokens
	writeCache(t, ctx)
	_, err = oramaApp.Commit()
	require.NoError(t, err)

	slashCtx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 3, Time: genesisTime.Add(6 * time.Second)})
	persisted, err := oramaApp.StakingKeeper.GetValidator(slashCtx, valAddr)
	require.NoError(t, err)
	require.True(t, persisted.Tokens.Equal(bonded), "undelegation did not persist, still bonded %s", persisted.Tokens)
	err = oramaApp.SlashingKeeper.SlashWithInfractionReason(
		slashCtx, consAddr, math.LegacyNewDecWithPrec(5, 2), 1_000_000_000_000, 2,
		stakingtypes.Infraction_INFRACTION_DOUBLE_SIGN,
	)
	require.NoError(t, err)

	after, err := oramaApp.StakingKeeper.GetValidator(slashCtx, valAddr)
	require.NoError(t, err)
	wantBondBurned := bonded.MulRaw(5).QuoRaw(100)
	require.True(t, bonded.Sub(after.Tokens).Equal(wantBondBurned),
		"bonded burned %s, want 5%% of the %s still bonded", bonded.Sub(after.Tokens), bonded)

	unbonding, err := oramaApp.StakingKeeper.GetUnbondingDelegation(slashCtx, delAddr, valAddr)
	require.NoError(t, err)
	var left math.Int
	for _, entry := range unbonding.Entries {
		if left.IsNil() {
			left = entry.Balance
			continue
		}
		left = left.Add(entry.Balance)
	}
	wantUnbondLeft := unbonded.MulRaw(95).QuoRaw(100)
	require.True(t, left.Equal(wantUnbondLeft),
		"unbonding balance %s, want 95%% of the %s that left", left, unbonded)
}

func TestApp_jailedAndTombstonedMembersLosePower(t *testing.T) {
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, keys := committeeGenesis(t, oramaApp, 2, 365)
	initChain(t, oramaApp, genState, 0, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	ctx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second)})
	jailedVal := sdk.ValAddress(keys[0].account.PubKey().Address())
	jailed, err := oramaApp.StakingKeeper.GetValidator(ctx, jailedVal)
	require.NoError(t, err)
	jailedCons, err := jailed.GetConsAddr()
	require.NoError(t, err)
	require.NoError(t, oramaApp.SlashingKeeper.Jail(ctx, jailedCons))

	updates, err := oramaApp.PowerKeeper.RunEndBlock(ctx, oramaApp.EmissionKeeper)
	require.NoError(t, err)
	_, err = oramaApp.PowerKeeper.LastPower.Get(ctx, jailedVal.String())
	require.Error(t, err, "a jailed committee member must drop out of the validator set")
	other := sdk.ValAddress(keys[1].account.PubKey().Address()).String()
	otherPower, err := oramaApp.PowerKeeper.LastPower.Get(ctx, other)
	require.NoError(t, err)
	require.Positive(t, otherPower)
	require.NotEmpty(t, updates)

	tombApp := buildTestApp(t)
	tombGen, tombKeys := committeeGenesis(t, tombApp, 2, 365)
	initChain(t, tombApp, tombGen, 0, genesisTime)
	finalize(t, tombApp, 1, genesisTime.Add(2*time.Second))
	tombCtx := tombApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second)})
	tombVal := sdk.ValAddress(tombKeys[0].account.PubKey().Address())
	tombValidator, err := tombApp.StakingKeeper.GetValidator(tombCtx, tombVal)
	require.NoError(t, err)
	tombCons, err := tombValidator.GetConsAddr()
	require.NoError(t, err)
	require.NoError(t, tombApp.SlashingKeeper.Tombstone(tombCtx, tombCons))
	_, err = tombApp.PowerKeeper.RunEndBlock(tombCtx, tombApp.EmissionKeeper)
	require.NoError(t, err)
	_, err = tombApp.PowerKeeper.LastPower.Get(tombCtx, tombVal.String())
	require.Error(t, err, "a tombstoned committee member must lose its bootstrap seat")
}

func TestApp_exportImportRoundTrip(t *testing.T) {
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, keys := committeeGenesis(t, oramaApp, 1, 365)
	initChain(t, oramaApp, genState, 0, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	ctx := oramaApp.NewContext(true)
	wantEpoch, err := oramaApp.EmissionKeeper.EpochState.Get(ctx)
	require.NoError(t, err)
	member := sdk.AccAddress(keys[0].account.PubKey().Address())
	wantEarnings, err := oramaApp.FeesKeeper.GetEarnings(ctx, member)
	require.NoError(t, err)
	wantLambda, err := oramaApp.PowerKeeper.Lambda.Get(ctx)
	require.NoError(t, err)
	valAddr := sdk.ValAddress(member)
	wantVal, err := oramaApp.StakingKeeper.GetValidator(ctx, valAddr)
	require.NoError(t, err)

	exported, err := oramaApp.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)

	restored := buildTestApp(t)
	_, err = restored.InitChain(&abci.RequestInitChain{
		ChainId:         testChainID,
		InitialHeight:   exported.Height,
		Time:            genesisTime.Add(2 * time.Second),
		AppStateBytes:   exported.AppState,
		ConsensusParams: &exported.ConsensusParams,
	})
	require.NoError(t, err)

	// InitChain leaves its writes in the uncommitted finalize cache. Read that,
	// the same state FinalizeBlock(InitialHeight) would start from.
	restoredCtx := restored.NewContext(false)
	gotEpoch, err := restored.EmissionKeeper.EpochState.Get(restoredCtx)
	require.NoError(t, err)
	require.Equal(t, wantEpoch.CurrentEpoch, gotEpoch.CurrentEpoch)
	require.True(t, gotEpoch.CumulativeMinted.Equal(wantEpoch.CumulativeMinted))
	gotEarnings, err := restored.FeesKeeper.GetEarnings(restoredCtx, member)
	require.NoError(t, err)
	require.True(t, gotEarnings.Equal(wantEarnings), "earnings %s, want %s", gotEarnings, wantEarnings)
	gotLambda, err := restored.PowerKeeper.Lambda.Get(restoredCtx)
	require.NoError(t, err)
	require.True(t, gotLambda.Equal(wantLambda))
	gotVal, err := restored.StakingKeeper.GetValidator(restoredCtx, valAddr)
	require.NoError(t, err)
	require.True(t, gotVal.Tokens.Equal(wantVal.Tokens))

	_, broken := restored.EmissionKeeper.CheckSupplyInvariant(restoredCtx)
	require.False(t, broken)
	feesInv, err := restored.FeesKeeper.CheckInvariants(restoredCtx)
	require.NoError(t, err)
	require.True(t, feesInv.EarningsMatchModule, feesInv.Detail)
	require.True(t, feesInv.DepositsMatchModule, feesInv.Detail)
	require.True(t, feesInv.FeesBalance, feesInv.Detail)
}

func TestApp_lambdaAdvancesAcrossEpochs(t *testing.T) {
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, _ := committeeGenesis(t, oramaApp, 1, 3)
	var powerGen powertypes.GenesisState
	require.NoError(t, oramaApp.AppCodec().UnmarshalJSON(genState[powertypes.ModuleName], &powerGen))
	powerGen.GateSatisfied = true
	genState[powertypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(&powerGen)
	initChain(t, oramaApp, genState, 0, genesisTime)

	var previous math.LegacyDec
	for height := int64(1); height <= 4; height++ {
		finalize(t, oramaApp, height, genesisTime.Add(time.Duration(height)*2*time.Second))
		ctx := oramaApp.NewContext(true)
		lambda, err := oramaApp.PowerKeeper.Lambda.Get(ctx)
		require.NoError(t, err)
		if height > 1 {
			require.True(t, lambda.GTE(previous), "lambda moved backward: %s -> %s", previous, lambda)
		}
		previous = lambda
		_, broken := oramaApp.EmissionKeeper.CheckSupplyInvariant(ctx)
		require.False(t, broken)
		feesInv, err := oramaApp.FeesKeeper.CheckInvariants(ctx)
		require.NoError(t, err)
		require.True(t, feesInv.EarningsMatchModule && feesInv.DepositsMatchModule && feesInv.FeesBalance, feesInv.Detail)
	}
	require.True(t, previous.Equal(math.LegacyOneDec()), "lambda = %s, want 1 by the 3-epoch deadline", previous)
}

func TestApp_baseFeeRisesWhenTheBlockIsFull(t *testing.T) {
	const maxGas int64 = 120_000
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, keys := committeeGenesis(t, oramaApp, 1, 365)
	initChain(t, oramaApp, genState, maxGas, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	ctx := oramaApp.NewContext(true)
	before, err := oramaApp.FeesKeeper.BaseFee.Get(ctx)
	require.NoError(t, err)
	member := sdk.AccAddress(keys[0].account.PubKey().Address())
	acc := oramaApp.AccountKeeper.GetAccount(ctx, member)
	require.NotNil(t, acc)

	// Editing the validator spends gas and moves no coins. The fee equals the base
	// fee, so the tip is zero and the whole fee comes from earnings.
	msg := stakingtypes.NewMsgEditValidator(
		sdk.ValAddress(member).String(),
		stakingtypes.Description{Moniker: "renamed"},
		nil, nil,
	)
	fee := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(maxGas)))
	tx, err := simtestutil.GenSignedMockTx(
		rand.New(rand.NewSource(1)), oramaApp.TxConfig(),
		[]sdk.Msg{msg}, fee, uint64(maxGas), testChainID,
		[]uint64{acc.GetAccountNumber()}, []uint64{acc.GetSequence()}, keys[0].account,
	)
	require.NoError(t, err)
	txBytes, err := oramaApp.TxConfig().TxEncoder()(tx)
	require.NoError(t, err)

	resp := finalize(t, oramaApp, 2, genesisTime.Add(4*time.Second), txBytes)
	require.Len(t, resp.TxResults, 1)
	require.Zero(t, resp.TxResults[0].Code, "tx failed: %s", resp.TxResults[0].Log)

	afterCtx := oramaApp.NewContext(true)
	after, err := oramaApp.FeesKeeper.BaseFee.Get(afterCtx)
	require.NoError(t, err)
	require.True(t, after.GT(before), "base fee %s did not rise from %s (gas used %d / max %d)", after, before, resp.TxResults[0].GasUsed, maxGas)
}

func TestApp_outsiderBondsFromEarnings(t *testing.T) {
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, _ := committeeGenesis(t, oramaApp, 1, 365)
	outsiderKey := ed25519.GenPrivKey()
	outsider := sdk.AccAddress(outsiderKey.PubKey().Address())
	addAuthAccount(t, oramaApp, genState, outsiderKey)
	initChain(t, oramaApp, genState, 400_000, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	bond := math.NewInt(1_000_000_000) // 1 ORAMA, the minimum delegation
	gas := uint64(300_000)
	feeAmt := math.NewInt(int64(gas)) // base fee is 1
	credit := bond.Add(feeAmt)

	fundCtx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second)})
	require.NoError(t, oramaApp.BankKeeper.MintCoins(fundCtx, emissiontypes.ModuleName, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, credit))))
	require.NoError(t, oramaApp.FeesKeeper.CreditEarnings(fundCtx, emissiontypes.ModuleName, outsider, sdk.NewCoin(params.BaseDenom, credit)))
	writeCache(t, fundCtx)
	_, err := oramaApp.Commit()
	require.NoError(t, err)

	consPub := ed25519.GenPrivKey().PubKey()
	msg, err := stakingtypes.NewMsgCreateValidator(
		sdk.ValAddress(outsider).String(),
		consPub,
		sdk.NewCoin(params.BaseDenom, bond),
		stakingtypes.NewDescription("outsider", "", "", "", ""),
		stakingtypes.NewCommissionRates(math.LegacyNewDecWithPrec(1, 1), math.LegacyNewDecWithPrec(2, 1), math.LegacyNewDecWithPrec(1, 2)),
		math.OneInt(),
	)
	require.NoError(t, err)

	readCtx := oramaApp.NewContext(true)
	acc := oramaApp.AccountKeeper.GetAccount(readCtx, outsider)
	require.NotNil(t, acc)
	tx, err := simtestutil.GenSignedMockTx(
		rand.New(rand.NewSource(2)), oramaApp.TxConfig(),
		[]sdk.Msg{msg}, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, feeAmt)), gas, testChainID,
		[]uint64{acc.GetAccountNumber()}, []uint64{acc.GetSequence()}, outsiderKey,
	)
	require.NoError(t, err)
	txBytes, err := oramaApp.TxConfig().TxEncoder()(tx)
	require.NoError(t, err)
	resp := finalize(t, oramaApp, 3, genesisTime.Add(6*time.Second), txBytes)
	require.Zero(t, resp.TxResults[0].Code, "create-validator failed: %s", resp.TxResults[0].Log)

	checkCtx := oramaApp.NewContext(true)
	validator, err := oramaApp.StakingKeeper.GetValidator(checkCtx, sdk.ValAddress(outsider))
	require.NoError(t, err)
	require.True(t, validator.Tokens.Equal(bond), "bonded %s, want %s", validator.Tokens, bond)
	left, err := oramaApp.FeesKeeper.GetEarnings(checkCtx, outsider)
	require.NoError(t, err)
	require.True(t, left.IsZero(), "earnings left = %s, want the bond and the fee to have spent them", left)

	feesInv, err := oramaApp.FeesKeeper.CheckInvariants(checkCtx)
	require.NoError(t, err)
	require.True(t, feesInv.EarningsMatchModule && feesInv.DepositsMatchModule && feesInv.FeesBalance, feesInv.Detail)
}

func writeCache(t *testing.T, ctx sdk.Context) {
	t.Helper()
	cache, ok := ctx.MultiStore().(storetypes.CacheMultiStore)
	require.True(t, ok, "expected a cache multistore")
	cache.Write()
}

func addAuthAccount(t *testing.T, oramaApp *app.OramaApp, genState app.GenesisState, priv cryptotypes.PrivKey) {
	t.Helper()
	var authGen authtypes.GenesisState
	require.NoError(t, oramaApp.AppCodec().UnmarshalJSON(genState[authtypes.ModuleName], &authGen))
	accs, err := authtypes.UnpackAccounts(authGen.Accounts)
	require.NoError(t, err)
	addr := sdk.AccAddress(priv.PubKey().Address())
	accs = append(accs, authtypes.NewBaseAccount(addr, priv.PubKey(), 0, 0))
	accs = authtypes.SanitizeGenesisAccounts(accs)
	packed, err := authtypes.PackAccounts(accs)
	require.NoError(t, err)
	authGen.Accounts = packed
	genState[authtypes.ModuleName] = oramaApp.AppCodec().MustMarshalJSON(&authGen)
}

func signedTx(t *testing.T, oramaApp *app.OramaApp, key cryptotypes.PrivKey, seed int64, gas uint64, msgs ...sdk.Msg) []byte {
	t.Helper()
	signer := sdk.AccAddress(key.PubKey().Address())
	acc := oramaApp.AccountKeeper.GetAccount(oramaApp.NewContext(true), signer)
	require.NotNil(t, acc)
	tx, err := simtestutil.GenSignedMockTx(
		rand.New(rand.NewSource(seed)), oramaApp.TxConfig(),
		msgs, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(int64(gas)))), gas, testChainID,
		[]uint64{acc.GetAccountNumber()}, []uint64{acc.GetSequence()}, key,
	)
	require.NoError(t, err)
	txBytes, err := oramaApp.TxConfig().TxEncoder()(tx)
	require.NoError(t, err)
	return txBytes
}

func TestApp_operatorBondsNodeFromEarnings(t *testing.T) {
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, _ := committeeGenesis(t, oramaApp, 1, 365)
	opKey := ed25519.GenPrivKey()
	op := sdk.AccAddress(opKey.PubKey().Address())
	addAuthAccount(t, oramaApp, genState, opKey)
	initChain(t, oramaApp, genState, 2_000_000, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	bond := math.NewInt(1_000_000_000)
	gas := uint64(300_000)
	// the two bonds, plus headroom for the fees and the node-registration charge taken from the same earnings
	credit := bond.MulRaw(2).Add(math.NewInt(200_000_000))
	fundCtx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second)})
	require.NoError(t, oramaApp.BankKeeper.MintCoins(fundCtx, emissiontypes.ModuleName, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, credit))))
	require.NoError(t, oramaApp.FeesKeeper.CreditEarnings(fundCtx, emissiontypes.ModuleName, op, sdk.NewCoin(params.BaseDenom, credit)))
	writeCache(t, fundCtx)
	_, err := oramaApp.Commit()
	require.NoError(t, err)

	torPub, torPriv, err := stded25519.GenerateKey(nil)
	require.NoError(t, err)
	hot := sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address())
	register := signedTx(t, oramaApp, opKey, 3, gas,
		&nodestypes.MsgRegisterOperator{Operator: op.String()},
		&nodestypes.MsgRegisterNode{
			Operator: op.String(), NodeId: "node-1", Roles: []nodestypes.Role{nodestypes.RoleRelay}, HotKey: hot.String(),
			Bindings: []nodestypes.Binding{{
				Service: "tor", KeyType: nodestypes.KeyTypeEd25519, Pubkey: torPub,
				Signature: stded25519.Sign(torPriv, nodestypes.BindingSignBytes(testChainID, op.String(), "tor", torPub)),
			}},
			Endpoints: []string{"https://node.example:443"}, RegionHint: "eu-1",
		})
	resp := finalize(t, oramaApp, 3, genesisTime.Add(6*time.Second), register)
	require.Zero(t, resp.TxResults[0].Code, "register failed: %s", resp.TxResults[0].Log)
	require.True(t, oramaApp.BankKeeper.GetBalance(oramaApp.NewContext(true), op, params.BaseDenom).Amount.IsZero(), "operator holds no bank balance")

	bondMsg := func() sdk.Msg {
		return &nodestypes.MsgBondNode{Operator: op.String(), NodeId: "node-1", Role: nodestypes.RoleRelay, Amount: bond}
	}
	bondTx := signedTx(t, oramaApp, opKey, 4, gas, bondMsg(), bondMsg())
	resp = finalize(t, oramaApp, 4, genesisTime.Add(8*time.Second), bondTx)
	require.Zero(t, resp.TxResults[0].Code, "bond failed: %s", resp.TxResults[0].Log)

	checkCtx := oramaApp.NewContext(true)
	node, err := oramaApp.NodesKeeper.GetNode(checkCtx, "node-1")
	require.NoError(t, err)
	require.Equal(t, bond.MulRaw(2).String(), node.Bonds[0].Amount.String())
	left, err := oramaApp.FeesKeeper.GetEarnings(checkCtx, op)
	require.NoError(t, err)
	require.True(t, left.LT(math.NewInt(200_000_000)), "earnings left = %s, want the two bonds taken out of them", left)

	nodesInv, err := oramaApp.NodesKeeper.CheckInvariants(checkCtx)
	require.NoError(t, err)
	require.True(t, nodesInv.BalanceMatches, nodesInv.Detail)
	feesInv, err := oramaApp.FeesKeeper.CheckInvariants(checkCtx)
	require.NoError(t, err)
	require.True(t, feesInv.EarningsMatchModule && feesInv.DepositsMatchModule && feesInv.FeesBalance, feesInv.Detail)
}
