package keeper_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/keeper"
	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// fakeOperators is an operator registry: it maps a consensus pubkey to the operator whose node
// binds it. A key it does not know is unlinked.
type fakeOperators struct{ byKey map[string]string }

func newFakeOperators() *fakeOperators { return &fakeOperators{byKey: map[string]string{}} }

func (o *fakeOperators) bind(pubkey []byte, operator string) {
	o.byKey[hex.EncodeToString(pubkey)] = operator
}

func (o *fakeOperators) OperatorOfConsensusKey(_ context.Context, pubkey []byte) (string, bool, error) {
	op, ok := o.byKey[hex.EncodeToString(pubkey)]
	return op, ok, nil
}

// operatorField is a bonded set of 24 one-validator operators holding 1,000 tokens each, plus one
// operator "whale" running whaleValidators validators of 100 tokens each, with the committee seat
// of setupSingleCommitteeGenesis and lambda set to lambda. Every stake is admitted, so the ramp
// does not mix into what the cap is measured on.
type operatorField struct {
	f      *testFixture
	ops    *fakeOperators
	whale  []string
	single []string
	keys   map[string][]byte
}

// unbind removes the validators' consensus keys from the operator registry, as if no node bound them.
func (fld *operatorField) unbind(valopers []string) {
	for _, v := range valopers {
		delete(fld.ops.byKey, hex.EncodeToString(fld.keys[v]))
	}
}

func newOperatorField(t *testing.T, whaleValidators int, lambda string, link bool) *operatorField {
	t.Helper()
	f := newTestFixture(t)
	setupSingleCommitteeGenesis(t, f)
	fld := &operatorField{f: f, ops: newFakeOperators(), keys: map[string][]byte{}}

	seed := byte(10)
	add := func(name string, tokens int64, operator string) string {
		valoper := sdk.ValAddress(fmt.Sprintf("%-20s", name)).String()
		pk := testPubKey(seed)
		seed++
		fld.keys[valoper] = pk
		f.Staking.addValidator(t, valoper, pk, tokens, "0.0")
		require.NoError(t, f.Keeper.RampAdmitted.Set(f.Ctx, valoper, math.NewInt(tokens)))
		if link {
			fld.ops.bind(pk, operator)
		}
		return valoper
	}
	for i := 0; i < 24; i++ {
		fld.single = append(fld.single, add(fmt.Sprintf("single-%02d", i), 1_000, fmt.Sprintf("op-%02d", i)))
	}
	for i := 0; i < whaleValidators; i++ {
		fld.whale = append(fld.whale, add(fmt.Sprintf("whale-%02d", i), 1_000, "whale"))
	}
	if link {
		f.Keeper = f.Keeper.WithOperators(fld.ops)
	}

	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	p.RampEpochs = 10
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))
	require.NoError(t, f.Keeper.Lambda.Set(f.Ctx, math.LegacyMustNewDecFromStr(lambda)))
	require.NoError(t, f.Keeper.LambdaLastUpdatedEpoch.Set(f.Ctx, 1_000_000))
	f.Emission.epoch = 100
	return fld
}

func (fld *operatorField) shares(t *testing.T) map[string]math.LegacyDec {
	t.Helper()
	shares, err := fld.f.Keeper.PowerShares(fld.f.Ctx, fld.f.Emission)
	require.NoError(t, err)
	return shares
}

func sumOf(shares map[string]math.LegacyDec, addrs []string) math.LegacyDec {
	sum := math.LegacyZeroDec()
	for _, a := range addrs {
		sum = sum.Add(shares[a])
	}
	return sum
}

func requireNear(t *testing.T, want string, got math.LegacyDec, msg string) {
	t.Helper()
	require.True(t, got.Sub(math.LegacyMustNewDecFromStr(want)).Abs().LT(math.LegacyNewDecWithPrec(1, 9)), "%s: got %s, want %s", msg, got, want)
}

func TestPowerShares_anOperatorWithManyValidatorsHoldsOneCap(t *testing.T) {
	fld := newOperatorField(t, 10, "1", true)

	shares := fld.shares(t)

	requireNear(t, "0.05", sumOf(shares, fld.whale), "the whale's 10 validators together")
	requireNear(t, "0.005", shares[fld.whale[0]], "one of ten equal whale validators")
	// The other 24 operators split what the whale leaves.
	requireNear(t, "0.039583333", shares[fld.single[0]], "a one-validator operator")
}

func TestPowerShares_withoutAnOperatorRegistryEveryValidatorIsItsOwnOperator(t *testing.T) {
	fld := newOperatorField(t, 10, "1", false)

	shares := fld.shares(t)

	// 10,000 whale tokens of 34,000 are 29%, but none of its ten validators nears the per-validator
	// cap, so the whale holds its raw share. This is the behaviour the operator cap replaces.
	requireNear(t, "0.294117647", sumOf(shares, fld.whale), "the whale without an operator cap")
}

func TestPowerShares_validatorsNoNodeBindsShareOneCap(t *testing.T) {
	fld := newOperatorField(t, 10, "1", true)
	fld.unbind(fld.whale)

	shares := fld.shares(t)

	requireNear(t, "0.05", sumOf(shares, fld.whale), "ten unlinked validators together")
}

func TestPowerShares_leavingTheUnlinkedBucketNeverGainsPower(t *testing.T) {
	linked := newOperatorField(t, 10, "1", true)
	unlinked := newOperatorField(t, 10, "1", true)
	unlinked.unbind(unlinked.whale)

	// Splitting over validators and registering no node is worth exactly as much as registering them
	// under one operator, so there is no reason to stay unregistered.
	requireNear(t, sumOf(linked.shares(t), linked.whale).String(), sumOf(unlinked.shares(t), unlinked.whale), "linked against unlinked")
}

func TestPowerShares_theCommitteeSeatKeepsItsBootstrapShareWhileLambdaIsBelowOne(t *testing.T) {
	fld := newOperatorField(t, 10, "0.5", true)

	shares := fld.shares(t)

	committee := sdk.ValAddress(sdk.AccAddress("solo_committee_member")).String()
	requireNear(t, "0.5", shares[committee], "the committee seat at lambda 0.5")
	requireNear(t, "0.025", sumOf(shares, fld.whale), "the whale holds the cap of the stake half")
}

func TestPowerShares_theCommitteeSeatLapsesAtLambdaOneAndTheCapStillHolds(t *testing.T) {
	fld := newOperatorField(t, 10, "1", true)

	shares := fld.shares(t)

	committee := sdk.ValAddress(sdk.AccAddress("solo_committee_member")).String()
	require.True(t, shares[committee].IsZero(), "a seat with no stake has no power at lambda 1")
	requireNear(t, "0.05", sumOf(shares, fld.whale), "the whale at lambda 1")
}

func TestPowerShares_aNewValidatorOfACappedOperatorRampsInUnderTheOperatorCap(t *testing.T) {
	fld := newOperatorField(t, 1, "1", true)
	late := sdk.ValAddress(fmt.Sprintf("%-20s", "whale-late")).String()
	fld.f.Staking.addValidator(t, late, testPubKey(99), 10_000, "0.0")
	fld.ops.bind(testPubKey(99), "whale")
	whale := append([]string{late}, fld.whale...)

	// Its tokens start ramping at epoch 100 over 10 epochs; nothing is admitted yet.
	require.NoError(t, fld.f.Keeper.RampExcess.Set(fld.f.Ctx, late, math.NewInt(10_000)))
	require.NoError(t, fld.f.Keeper.RampExcessEpoch.Set(fld.f.Ctx, late, 100))
	require.NoError(t, fld.f.Keeper.RampAdmitted.Set(fld.f.Ctx, late, math.ZeroInt()))

	fld.f.Emission.epoch = 100
	atStart := sumOf(fld.shares(t), whale)
	fld.f.Emission.epoch = 105
	halfway := sumOf(fld.shares(t), whale)
	fld.f.Emission.epoch = 110
	done := sumOf(fld.shares(t), whale)

	requireNear(t, "0.04", atStart, "the whale before any of the late stake is admitted")
	requireNear(t, "0.05", halfway, "the whale while its late stake ramps")
	requireNear(t, "0.05", done, "the whale after the ramp")
}

func TestRunEndBlock_aCapStepDownCountsOperatorsNotValidators(t *testing.T) {
	// 70 validators are more than the 60 that step the cap down to 3%, but one operator runs them
	// all: one independent party does not step the cap down.
	oneOperator := newTestFixture(t)
	setupSingleCommitteeGenesis(t, oneOperator)
	ops := newFakeOperators()
	for i := 0; i < 70; i++ {
		pk := testPubKey(byte(20 + i))
		oneOperator.Staking.addValidator(t, sdk.ValAddress(fmt.Sprintf("%-20s", fmt.Sprintf("many-%02d", i))).String(), pk, 100, "0.0")
		ops.bind(pk, "the-operator")
	}
	oneOperator.Keeper = oneOperator.Keeper.WithOperators(ops)
	oneOperator.Emission.epoch = 2
	_, err := oneOperator.Keeper.RunEndBlock(oneOperator.Ctx, oneOperator.Emission)
	require.NoError(t, err)
	capBps, err := oneOperator.Keeper.CapCurrentBps.Get(oneOperator.Ctx)
	require.NoError(t, err)
	require.Equal(t, types.CapBpsNormal, capBps, "70 validators of one operator are one party")

	// The same 70 validators with no operator registry are 70 parties.
	perValidator := newTestFixture(t)
	setupSingleCommitteeGenesis(t, perValidator)
	for i := 0; i < 70; i++ {
		perValidator.Staking.addValidator(t, sdk.ValAddress(fmt.Sprintf("%-20s", fmt.Sprintf("many-%02d", i))).String(), testPubKey(byte(20+i)), 100, "0.0")
	}
	perValidator.Emission.epoch = 2
	_, err = perValidator.Keeper.RunEndBlock(perValidator.Ctx, perValidator.Emission)
	require.NoError(t, err)
	capBps, err = perValidator.Keeper.CapCurrentBps.Get(perValidator.Ctx)
	require.NoError(t, err)
	require.Equal(t, types.CapBpsReduced, capBps, "70 independent validators step the cap down")
}

func TestValidatorPowerQuery_namesTheOperatorAValidatorCountsToward(t *testing.T) {
	fld := newOperatorField(t, 2, "1", true)
	server := keeper.NewQueryServerImpl(fld.f.Keeper)
	fld.unbind(fld.whale[1:])

	linked, err := server.ValidatorPower(fld.f.Ctx, &types.QueryValidatorPowerRequest{OperatorAddress: fld.whale[0]})
	require.NoError(t, err)
	require.Equal(t, "whale", linked.Operator)

	unlinked, err := server.ValidatorPower(fld.f.Ctx, &types.QueryValidatorPowerRequest{OperatorAddress: fld.whale[1]})
	require.NoError(t, err)
	require.Equal(t, types.UnlinkedOperator, unlinked.Operator)

	missing, err := server.ValidatorPower(fld.f.Ctx, &types.QueryValidatorPowerRequest{OperatorAddress: sdk.ValAddress("nobody_______________").String()})
	require.NoError(t, err)
	require.Empty(t, missing.Operator)
}

func TestValidatorPowerQuery_withoutAnOperatorRegistryAValidatorIsItsOwnOperator(t *testing.T) {
	fld := newOperatorField(t, 1, "1", false)
	server := keeper.NewQueryServerImpl(fld.f.Keeper)

	res, err := server.ValidatorPower(fld.f.Ctx, &types.QueryValidatorPowerRequest{OperatorAddress: fld.whale[0]})

	require.NoError(t, err)
	require.Equal(t, fld.whale[0], res.Operator)
}
