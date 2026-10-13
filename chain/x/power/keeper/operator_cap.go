package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// operatorOf returns the operator a validator counts toward. Without an operator registry every
// validator is its own operator. With one, a validator whose consensus key is bound to a live node
// counts toward that node's operator, and every other validator shares types.UnlinkedOperator.
func (k Keeper) operatorOf(ctx sdk.Context, valoper string, consensusKey []byte) (string, error) {
	if k.operators == nil {
		return valoper, nil
	}
	operator, ok, err := k.operators.OperatorOfConsensusKey(ctx, consensusKey)
	if err != nil {
		return "", fmt.Errorf("failed to resolve the operator of validator %q: %w", valoper, err)
	}
	if !ok {
		return types.UnlinkedOperator, nil
	}
	return operator, nil
}

// operatorStakes tags each stake with its operator.
func (k Keeper) operatorStakes(ctx sdk.Context, stakes []types.ValidatorStake, pubKeyByAddr map[string][]byte) ([]types.OperatorStake, error) {
	out := make([]types.OperatorStake, len(stakes))
	for i, s := range stakes {
		operator, err := k.operatorOf(ctx, s.OperatorAddress, pubKeyByAddr[s.OperatorAddress])
		if err != nil {
			return nil, err
		}
		out[i] = types.OperatorStake{OperatorAddress: s.OperatorAddress, Operator: operator, BondedTokens: s.BondedTokens}
	}
	return out, nil
}

// validatorOperator returns the operator a stored validator counts toward, or "" when there is no
// such validator.
func (k Keeper) validatorOperator(ctx sdk.Context, valAddr sdk.ValAddress) (string, error) {
	validator, err := k.stakingKeeper.GetValidator(ctx, valAddr)
	if err != nil {
		if isValidatorNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to load validator %q: %w", valAddr, err)
	}
	pubKey, err := consPubKeyBytes(validator)
	if err != nil {
		return "", fmt.Errorf("failed to read consensus pubkey for %q: %w", valAddr, err)
	}
	return k.operatorOf(ctx, validator.OperatorAddress, pubKey)
}
