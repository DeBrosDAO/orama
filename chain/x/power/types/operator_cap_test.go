package types_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

var (
	capFive = math.LegacyNewDecWithPrec(5, 2)
	multTwo = math.LegacyNewDec(2)
)

func opStake(valoper, operator string, tokens int64) types.OperatorStake {
	return types.OperatorStake{OperatorAddress: valoper, Operator: operator, BondedTokens: math.NewInt(tokens)}
}

// field builds `singles` one-validator operators holding `each` tokens, plus one operator
// "whale" running `whaleValidators` validators of `each` tokens.
func field(singles, whaleValidators int, each int64) []types.OperatorStake {
	var stakes []types.OperatorStake
	for i := 0; i < singles; i++ {
		stakes = append(stakes, opStake(fmt.Sprintf("single-%02d", i), fmt.Sprintf("op-%02d", i), each))
	}
	for i := 0; i < whaleValidators; i++ {
		stakes = append(stakes, opStake(fmt.Sprintf("whale-%02d", i), "whale", each))
	}
	return stakes
}

func sumWhale(stakes []types.OperatorStake, shares []math.LegacyDec) math.LegacyDec {
	sum := math.LegacyZeroDec()
	for i, s := range stakes {
		if s.Operator == "whale" {
			sum = sum.Add(shares[i])
		}
	}
	return sum
}

func TestComputeOperatorCappedShares_oneOperatorWithManyValidatorsHoldsOneCap(t *testing.T) {
	stakes := field(30, 20, 100)

	perOperator := types.ComputeOperatorCappedShares(stakes, capFive, multTwo)

	whale := sumWhale(stakes, perOperator)
	require.True(t, whale.LTE(capFive), "the operator's 20 validators hold %s, more than the cap %s", whale, capFive)

	// The same stakes capped per validator give the whale 20 slots.
	validators := make([]types.ValidatorStake, len(stakes))
	for i, s := range stakes {
		validators[i] = types.ValidatorStake{OperatorAddress: s.OperatorAddress, BondedTokens: s.BondedTokens}
	}
	perValidator := types.ComputeCappedShares(validators, capFive, multTwo)
	require.True(t, sumWhale(stakes, perValidator).GT(math.LegacyNewDecWithPrec(30, 2)), "without the operator cap the whale holds the majority of its raw 40%%")
}

func TestComputeOperatorCappedShares_splittingStakeOverMoreValidatorsGainsNothing(t *testing.T) {
	const totalWhaleTokens = 2_000
	var whaleShares []math.LegacyDec
	for _, validators := range []int{1, 2, 5, 20} {
		stakes := field(30, 0, 100)
		for i := 0; i < validators; i++ {
			stakes = append(stakes, opStake(fmt.Sprintf("whale-%02d", i), "whale", totalWhaleTokens/int64(validators)))
		}
		shares := types.ComputeOperatorCappedShares(stakes, capFive, multTwo)
		whaleShares = append(whaleShares, sumWhale(stakes, shares))
	}
	for i := 1; i < len(whaleShares); i++ {
		require.True(t, whaleShares[i].Sub(whaleShares[0]).Abs().LT(math.LegacyNewDecWithPrec(1, 12)),
			"the same stake held by more validators changed the operator's share: %s vs %s", whaleShares[i], whaleShares[0])
	}
}

func TestComputeOperatorCappedShares_aSplitOperatorLeavesMoreForTheOthers(t *testing.T) {
	stakes := field(30, 20, 100)
	shares := types.ComputeOperatorCappedShares(stakes, capFive, multTwo)

	// A one-validator operator holds 100 of 5,000 raw tokens, 2%. The whale's excess over its cap
	// is redistributed, so each of the 30 holds an equal part of the 95% the whale leaves.
	want := math.LegacyNewDecWithPrec(95, 2).QuoInt64(30)
	require.True(t, shares[0].Sub(want).Abs().LT(math.LegacyNewDecWithPrec(1, 12)), "got %s, want %s", shares[0], want)
}

func TestComputeOperatorCappedShares_splitsAnOperatorsShareProRataToStake(t *testing.T) {
	stakes := field(30, 0, 100)
	stakes = append(stakes, opStake("whale-big", "whale", 3_000), opStake("whale-small", "whale", 1_000))

	shares := types.ComputeOperatorCappedShares(stakes, capFive, multTwo)

	big, small := shares[len(shares)-2], shares[len(shares)-1]
	ratio := big.Quo(small)
	require.True(t, ratio.Sub(math.LegacyNewDec(3)).Abs().LT(math.LegacyNewDecWithPrec(1, 12)), "3:1 stake must split 3:1, got %s", ratio)
}

func TestComputeOperatorCappedShares_fewerThan20OperatorsShareEquallyPerOperator(t *testing.T) {
	// 4 operators cannot respect a 5% cap, so each holds 1/4 whatever its stake, and an operator's
	// validators split that quarter.
	stakes := []types.OperatorStake{
		opStake("a1", "A", 10), opStake("a2", "A", 10), opStake("a3", "A", 10), opStake("a4", "A", 10), opStake("a5", "A", 10),
		opStake("b1", "B", 1_000), opStake("c1", "C", 5), opStake("d1", "D", 5),
	}

	shares := types.ComputeOperatorCappedShares(stakes, capFive, multTwo)

	quarter := math.LegacyNewDecWithPrec(25, 2)
	require.True(t, shares[0].Add(shares[1]).Add(shares[2]).Add(shares[3]).Add(shares[4]).Sub(quarter).Abs().LT(math.LegacyNewDecWithPrec(1, 12)))
	require.True(t, shares[5].Sub(quarter).Abs().LT(math.LegacyNewDecWithPrec(1, 12)))
	require.True(t, shares[6].Sub(quarter).Abs().LT(math.LegacyNewDecWithPrec(1, 12)))
}

func TestComputeOperatorCappedShares_aZeroStakeOperatorSplitsEquallyAmongItsValidators(t *testing.T) {
	stakes := []types.OperatorStake{opStake("z1", "Z", 0), opStake("z2", "Z", 0), opStake("y1", "Y", 0)}

	shares := types.ComputeOperatorCappedShares(stakes, capFive, multTwo)

	half := math.LegacyNewDecWithPrec(25, 2)
	require.True(t, shares[0].Equal(half) && shares[1].Equal(half), "got %s and %s", shares[0], shares[1])
	require.True(t, shares[2].Equal(math.LegacyNewDecWithPrec(5, 1)))
}

func TestComputeOperatorCappedShares_onePerValidatorOperatorsMatchThePerValidatorCap(t *testing.T) {
	var tagged []types.OperatorStake
	var plain []types.ValidatorStake
	for i := 0; i < 40; i++ {
		tokens := int64(50 + 37*i)
		valoper := fmt.Sprintf("val-%02d", i)
		tagged = append(tagged, opStake(valoper, valoper, tokens))
		plain = append(plain, types.ValidatorStake{OperatorAddress: valoper, BondedTokens: math.NewInt(tokens)})
	}

	got := types.ComputeOperatorCappedShares(tagged, capFive, multTwo)
	want := types.ComputeCappedShares(plain, capFive, multTwo)

	for i := range got {
		require.True(t, got[i].Sub(want[i]).Abs().LT(math.LegacyNewDecWithPrec(1, 15)), "validator %d: %s vs %s", i, got[i], want[i])
	}
}

func TestComputeOperatorCappedShares_isIndependentOfInputOrder(t *testing.T) {
	stakes := field(25, 7, 100)
	forward := types.ComputeOperatorCappedShares(stakes, capFive, multTwo)

	reversed := make([]types.OperatorStake, len(stakes))
	for i, s := range stakes {
		reversed[len(stakes)-1-i] = s
	}
	backward := types.ComputeOperatorCappedShares(reversed, capFive, multTwo)

	for i := range stakes {
		require.True(t, forward[i].Equal(backward[len(stakes)-1-i]), "validator %s moved with the input order", stakes[i].OperatorAddress)
	}
}

func TestComputeOperatorCappedShares_emptyInput(t *testing.T) {
	require.Nil(t, types.ComputeOperatorCappedShares(nil, capFive, multTwo))
}

func TestComputeOperatorCappedShares_aSingleOperatorHoldsEverythingAndSplitsItProRata(t *testing.T) {
	stakes := []types.OperatorStake{opStake("a", "only", 300), opStake("b", "only", 100)}

	shares := types.ComputeOperatorCappedShares(stakes, capFive, multTwo)

	require.True(t, shares[0].Add(shares[1]).Sub(math.LegacyOneDec()).Abs().LT(math.LegacyNewDecWithPrec(1, 15)))
	require.True(t, shares[0].Quo(shares[1]).Sub(math.LegacyNewDec(3)).Abs().LT(math.LegacyNewDecWithPrec(1, 12)))
}

func TestCountOperators(t *testing.T) {
	require.Equal(t, uint64(0), types.CountOperators(nil))
	require.Equal(t, uint64(2), types.CountOperators([]string{"a", "b", "a", "b", "a"}))
}
