package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// powerStakes is the bonded set whose tokens count toward C_i. A jailed or
// tombstoned validator is omitted so it cannot keep a capped-stake seat after
// its bootstrap share is zeroed. Its consensus pubkey is still returned so a
// removal update can name the key CometBFT already has.
func (k Keeper) powerStakes(ctx sdk.Context, bonded []stakingtypes.Validator) ([]types.ValidatorStake, map[string][]byte, error) {
	stakes := make([]types.ValidatorStake, 0, len(bonded))
	pubKeyByAddr := make(map[string][]byte, len(bonded))
	for _, validator := range bonded {
		pubKey, err := consPubKeyBytes(validator)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read consensus pubkey for %q: %w", validator.OperatorAddress, err)
		}
		pubKeyByAddr[validator.OperatorAddress] = pubKey
		if validator.Jailed {
			continue
		}
		consAddr, err := validator.GetConsAddr()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to derive consensus address for %q: %w", validator.OperatorAddress, err)
		}
		if k.slashingKeeper.IsTombstoned(ctx, consAddr) {
			continue
		}
		stakes = append(stakes, types.ValidatorStake{
			OperatorAddress: validator.OperatorAddress,
			BondedTokens:    validator.Tokens,
		})
	}
	return stakes, pubKeyByAddr, nil
}
