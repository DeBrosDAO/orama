// Package keeper implements x/token's state machine: factory denoms whose
// balances live in x/bank (plans/open-network/track-c-chain.md C10).
package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"

	feeskeeper "github.com/DeBrosOfficial/network/chain/x/fees/keeper"
	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// The real keepers satisfy the interfaces x/token calls. The app does not
// construct this keeper yet; the assertions keep the shapes aligned.
var (
	_ types.BankKeeper = bankkeeper.BaseKeeper{}
	_ types.FeesKeeper = feeskeeper.Keeper{}
)

// Keeper is x/token's keeper. It has no module admin key.
type Keeper struct {
	cdc          codec.BinaryCodec
	storeService storetypes.KVStoreService
	bank         types.BankKeeper
	fees         types.FeesKeeper
	transferHook types.TransferHook

	Schema collections.Schema
	Params collections.Item[types.Params]
	Tokens collections.Map[string, types.Token]
	Frozen collections.KeySet[collections.Pair[string, string]]
}

// NewKeeper builds a new x/token Keeper. hook may be nil; a token cannot then be created with
// the transfer-hook capability.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	bank types.BankKeeper,
	fees types.FeesKeeper,
	hook types.TransferHook,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		cdc:          cdc,
		storeService: storeService,
		bank:         bank,
		fees:         fees,
		transferHook: hook,
		Params:       collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Tokens:       collections.NewMap(sb, types.TokensPrefix, "tokens", collections.StringKey, codec.CollValue[types.Token](cdc)),
		Frozen:       collections.NewKeySet(sb, types.FrozenPrefix, "frozen", collections.PairKeyCodec(collections.StringKey, collections.StringKey)),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.Logger().With("module", "x/"+types.ModuleName)
}

func (k Keeper) getToken(ctx context.Context, denom string) (types.Token, error) {
	token, err := k.Tokens.Get(ctx, denom)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.Token{}, fmt.Errorf("token %s does not exist", denom)
		}
		return types.Token{}, fmt.Errorf("failed to load token %s: %w", denom, err)
	}
	return token, nil
}

func (k Keeper) storeToken(ctx context.Context, token types.Token) error {
	if err := k.Tokens.Set(ctx, token.Denom, token); err != nil {
		return fmt.Errorf("failed to store token %s: %w", token.Denom, err)
	}
	return nil
}

func (k Keeper) isFrozen(ctx context.Context, denom, account string) (bool, error) {
	frozen, err := k.Frozen.Has(ctx, collections.Join(denom, account))
	if err != nil {
		return false, fmt.Errorf("failed to check freeze of %s on %s: %w", account, denom, err)
	}
	return frozen, nil
}

func parseAcc(addr, what string) (sdk.AccAddress, error) {
	if addr == "" {
		return nil, fmt.Errorf("%s is required", what)
	}
	parsed, err := sdk.AccAddressFromBech32(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %q: %w", what, addr, err)
	}
	return parsed, nil
}

func requirePositive(amount math.Int, what string) error {
	if amount.IsNil() {
		return fmt.Errorf("%s must be positive", what)
	}
	if !amount.IsPositive() {
		return fmt.Errorf("%s must be positive, got %s", what, amount)
	}
	return nil
}

func coins(denom string, amount math.Int) sdk.Coins {
	return sdk.NewCoins(sdk.NewCoin(denom, amount))
}
