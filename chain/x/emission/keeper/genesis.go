package keeper

import (
	"fmt"
	"strings"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// nonProductionChainIDMarkers are the chain-id substrings the bootstrap-stake premine gate
// accepts (Keeper.checkPremineGate). A mainnet chain-id ("orama-1") contains none of these.
var nonProductionChainIDMarkers = []string{"-stagenet-", "-devnet-", "-localnet-"}

// InitGenesis sets x/emission's state from a GenesisState. It must run after x/bank's InitGenesis
// so that, on a true fresh genesis, it can observe whatever norama supply genesis accounts
// created.
//
// A "true fresh genesis" is detected as CurrentEpoch <= 1, CumulativeMinted == 0 and
// CumulativeFaucetMinted == 0 (a faucet drip can precede the first epoch close): on that path
// GenesisSupply is (re)computed from the live bank supply and checked against the bootstrap-stake
// premine gate (checkPremineGate). On any other path (a genesis file produced by ExportGenesis,
// used to continue a chain after an upgrade or a coordinated hard fork) the given GenesisSupply is
// trusted as-is, since recomputing it from the then-current bank supply would double count
// everything minted since the real genesis.
//
// InitGenesis validates genState itself (in addition to the module's ValidateGenesis, which only
// runs when a genesis file is checked by hand, e.g. `oramad genesis validate`) and, once state is
// set, re-checks the supply invariant before returning - either way, a chain can never actually
// start from a broken emission genesis.
func (k Keeper) InitGenesis(ctx sdk.Context, genState types.GenesisState) error {
	if err := genState.Validate(); err != nil {
		return fmt.Errorf("invalid emission genesis state: %w", err)
	}

	if err := k.Params.Set(ctx, genState.Params); err != nil {
		return fmt.Errorf("failed to set emission params: %w", err)
	}

	state := genState.EpochState
	if err := checkBootstrapChainID(ctx, genState.Params); err != nil {
		return err
	}
	if err := checkFaucetChainID(ctx, genState.Params); err != nil {
		return err
	}
	if state.CumulativeDevelopmentMinted.IsNil() {
		state.CumulativeDevelopmentMinted = math.ZeroInt()
	}
	state.CumulativeFaucetMinted = nonNilInt(state.CumulativeFaucetMinted)
	state.FaucetEpochMinted = nonNilInt(state.FaucetEpochMinted)
	isFreshGenesis := state.CurrentEpoch <= 1 && state.CumulativeMinted.IsZero() && state.CumulativeFaucetMinted.IsZero()
	if isFreshGenesis {
		state.CurrentEpoch = 1
		state.GenesisSupply = k.bankKeeper.GetSupply(ctx, params.BaseDenom).Amount
		if state.EpochStartUnixNano == 0 {
			state.EpochStartUnixNano = ctx.BlockTime().UnixNano()
		}
		if err := k.checkPremineGate(ctx, genState.Params, state.GenesisSupply); err != nil {
			return err
		}
	}
	if err := k.EpochState.Set(ctx, state); err != nil {
		return fmt.Errorf("failed to set emission epoch state: %w", err)
	}

	for _, record := range genState.Ceilings {
		if record.DevelopmentMinted.IsNil() {
			record.DevelopmentMinted = math.ZeroInt()
		}
		if err := k.Ceilings.Set(ctx, record.Epoch, record); err != nil {
			return fmt.Errorf("failed to set emission ceiling record for epoch %d: %w", record.Epoch, err)
		}
	}

	if detail, broken := k.CheckSupplyInvariant(ctx); broken {
		return fmt.Errorf("emission genesis produced a broken supply invariant:\n%s", detail)
	}

	return nil
}

// checkPremineGate enforces the devnet-only bootstrap-stake exception documented on InitGenesis:
// a fresh genesis with nonzero norama supply is rejected unless Params.AllowBootstrapStake is set,
// the chain-id looks like a non-production one, and every norama of that supply sits in the
// staking bonded pool (i.e. it was spent entirely on genesis validators' self-bonds, with nothing
// left idle in a plain account).
func (k Keeper) checkPremineGate(ctx sdk.Context, p types.Params, genesisSupply math.Int) error {
	if genesisSupply.IsZero() {
		return nil
	}
	if !p.AllowBootstrapStake {
		return fmt.Errorf(
			"genesis supply is %s norama but allow_bootstrap_stake is false: a genesis must start at exactly zero supply unless allow_bootstrap_stake is explicitly set for a non-production chain",
			genesisSupply,
		)
	}

	bondedBalance := k.bankKeeper.GetBalance(ctx, k.bondedPoolAddr, params.BaseDenom).Amount
	if !genesisSupply.Equal(bondedBalance) {
		return fmt.Errorf(
			"allow_bootstrap_stake genesis supply (%s norama) must equal the staking bonded pool balance (%s norama) exactly, with none left idle outside it",
			genesisSupply, bondedBalance,
		)
	}

	return nil
}

// checkBootstrapChainID rejects allow_bootstrap_stake on a production chain-id, on both fresh and
// re-imported genesis. The flag also relaxes the production epoch floors (see Params.Validate), so
// this runs even at zero supply: otherwise a production chain-id could start with devnet-short
// epochs and emit the whole schedule in hours.
func checkBootstrapChainID(ctx sdk.Context, p types.Params) error {
	if p.AllowBootstrapStake && !isNonProductionChainID(ctx.ChainID()) {
		return fmt.Errorf(
			"allow_bootstrap_stake requires a chain-id containing one of %v, got %q",
			nonProductionChainIDMarkers, ctx.ChainID(),
		)
	}
	return nil
}

// checkFaucetChainID rejects faucet_enabled on a production chain-id, on both fresh and
// re-imported genesis: the faucet mints norama for anyone who asks, so a production chain must
// never be able to switch it on.
func checkFaucetChainID(ctx sdk.Context, p types.Params) error {
	if p.FaucetEnabled && !isNonProductionChainID(ctx.ChainID()) {
		return fmt.Errorf(
			"faucet_enabled requires a chain-id containing one of %v, got %q",
			nonProductionChainIDMarkers, ctx.ChainID(),
		)
	}
	return nil
}

// isNonProductionChainID reports whether chainID contains one of nonProductionChainIDMarkers.
func isNonProductionChainID(chainID string) bool {
	for _, marker := range nonProductionChainIDMarkers {
		if strings.Contains(chainID, marker) {
			return true
		}
	}
	return false
}

// ExportGenesis reads x/emission's current state back into a GenesisState.
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get emission params: %w", err)
	}
	state, err := k.EpochState.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get emission epoch state: %w", err)
	}

	var ceilings []types.CeilingRecord
	if err := k.Ceilings.Walk(ctx, nil, func(_ uint64, record types.CeilingRecord) (bool, error) {
		ceilings = append(ceilings, record)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk emission ceiling records: %w", err)
	}

	return &types.GenesisState{
		Params:     p,
		EpochState: state,
		Ceilings:   ceilings,
	}, nil
}
