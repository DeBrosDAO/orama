// Package keeper implements x/fees's state machine: the EIP-1559-style base fee, earnings
// accounts, and the state-deposit ledger (plans/open-network/track-c-chain.md C2).
package keeper

import (
	"context"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log/v2"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// Keeper is x/fees's keeper. It has no authority address: fees are paid through the ante handler,
// earnings are credited by other modules' keepers (via CreditEarnings), and deposits are
// locked/released by their owning module's keeper. The one user-signed message is
// MsgWithdrawEarnings, which moves the signer's own earnings to the signer's own bank balance
// (plans/open-network.md D18).
type Keeper struct {
	storeService storetypes.KVStoreService
	bankKeeper   types.BankKeeper

	Schema      collections.Schema
	Params      collections.Item[types.Params]
	BaseFee     collections.Item[math.Int]
	Earnings    collections.Map[string, math.Int]
	FeeBalances collections.Map[string, math.Int]
	Deposits    collections.Map[string, types.Deposit]
	Collected   collections.Item[math.Int]
	Burned      collections.Item[math.Int]
	Distributed collections.Item[math.Int]
}

// NewKeeper builds a new x/fees Keeper.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	bankKeeper types.BankKeeper,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		storeService: storeService,
		bankKeeper:   bankKeeper,
		Params:       collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		BaseFee:      collections.NewItem(sb, types.BaseFeeKey, "base_fee", sdk.IntValue),
		Earnings:     collections.NewMap(sb, types.EarningsPrefix, "earnings", collections.StringKey, sdk.IntValue),
		FeeBalances:  collections.NewMap(sb, types.FeeBalancesPrefix, "fee_balances", collections.StringKey, sdk.IntValue),
		Deposits:     collections.NewMap(sb, types.DepositsPrefix, "deposits", collections.StringKey, codec.CollValue[types.Deposit](cdc)),
		Collected:    collections.NewItem(sb, types.CollectedKey, "collected", sdk.IntValue),
		Burned:       collections.NewItem(sb, types.BurnedKey, "burned", sdk.IntValue),
		Distributed:  collections.NewItem(sb, types.DistributedKey, "distributed", sdk.IntValue),
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
