package app

import (
	"fmt"
	"sort"
	"strings"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// checkBankConservation checks, for every denom, that the supply is the sum of all balances (module
// accounts, the shielded pool's account and the earnings ledger's account included, since those hold
// their coins as ordinary balances), and that the chain knows every denom that has a supply: norama,
// or a factory denom x/token has a record for. A coin that exists in a denom nobody created is the
// footprint of a mint that bypassed x/token.
func (app *OramaApp) checkBankConservation(ctx sdk.Context) error {
	held := map[string]math.Int{}
	app.BankKeeper.IterateAllBalances(ctx, func(_ sdk.AccAddress, coin sdk.Coin) bool {
		held[coin.Denom] = addOrZero(held[coin.Denom], coin.Amount)
		return false
	})

	supply := map[string]math.Int{}
	app.BankKeeper.IterateTotalSupply(ctx, func(coin sdk.Coin) bool {
		supply[coin.Denom] = coin.Amount
		return false
	})

	var problems []string
	for _, denom := range sortedDenoms(held, supply) {
		if !supply[denom].IsNil() && !held[denom].IsNil() && supply[denom].Equal(held[denom]) {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s: supply %s but balances add up to %s", denom, intText(supply[denom]), intText(held[denom])))
	}
	for _, denom := range sortedDenoms(supply) {
		if err := app.knownDenom(ctx, denom); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// knownDenom refuses a denom with a supply that is neither norama nor a token x/token created.
func (app *OramaApp) knownDenom(ctx sdk.Context, denom string) error {
	if denom == params.BaseDenom {
		return nil
	}
	if !strings.HasPrefix(denom, tokentypes.DenomPrefix+"/") {
		return fmt.Errorf("%s has a supply but is not norama or a factory denom", denom)
	}
	has, err := app.TokenKeeper.Tokens.Has(ctx, denom)
	if err != nil {
		return fmt.Errorf("failed to look up the token record of %s: %w", denom, err)
	}
	if !has {
		return fmt.Errorf("%s has a supply but x/token has no record of it", denom)
	}
	return nil
}

// checkStakingPools checks that the bonded pool holds exactly the tokens of the bonded validators, and
// the not-bonded pool exactly the tokens of the unbonded and unbonding validators plus the balances of
// the unbonding delegations. These were x/staking's invariants before SDK 0.50 removed them.
func (app *OramaApp) checkStakingPools(ctx sdk.Context) error {
	bondDenom, err := app.StakingKeeper.BondDenom(ctx)
	if err != nil {
		return fmt.Errorf("failed to read the bond denom: %w", err)
	}
	bonded, notBonded := math.ZeroInt(), math.ZeroInt()
	if err := app.StakingKeeper.IterateValidators(ctx, func(_ int64, v stakingtypes.ValidatorI) bool {
		if v.GetStatus() == stakingtypes.Bonded {
			bonded = bonded.Add(v.GetTokens())
		} else {
			notBonded = notBonded.Add(v.GetTokens())
		}
		return false
	}); err != nil {
		return fmt.Errorf("failed to iterate validators: %w", err)
	}
	if err := app.StakingKeeper.IterateUnbondingDelegations(ctx, func(_ int64, ubd stakingtypes.UnbondingDelegation) bool {
		for _, entry := range ubd.Entries {
			notBonded = notBonded.Add(entry.Balance)
		}
		return false
	}); err != nil {
		return fmt.Errorf("failed to iterate unbonding delegations: %w", err)
	}

	var problems []string
	if pool := app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(stakingtypes.BondedPoolName), bondDenom).Amount; !pool.Equal(bonded) {
		problems = append(problems, fmt.Sprintf("bonded pool holds %s but bonded validators have %s", pool, bonded))
	}
	if pool := app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(stakingtypes.NotBondedPoolName), bondDenom).Amount; !pool.Equal(notBonded) {
		problems = append(problems, fmt.Sprintf("not-bonded pool holds %s but unbonded validators and unbonding delegations have %s", pool, notBonded))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

func addOrZero(a, b math.Int) math.Int {
	if a.IsNil() {
		return b
	}
	return a.Add(b)
}

func intText(v math.Int) string {
	if v.IsNil() {
		return "0"
	}
	return v.String()
}

// sortedDenoms returns the union of the keys of the maps, in sorted order.
func sortedDenoms(maps ...map[string]math.Int) []string {
	seen := map[string]bool{}
	for _, m := range maps {
		for denom := range m {
			seen[denom] = true
		}
	}
	out := make([]string, 0, len(seen))
	for denom := range seen {
		out = append(out, denom)
	}
	sort.Strings(out)
	return out
}
