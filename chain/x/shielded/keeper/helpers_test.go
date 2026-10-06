package keeper_test

import (
	"cosmossdk.io/collections"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

func collectionsPool() collections.Pair[uint32, []byte] {
	return collections.Join(types.VintageOrchardV1, pool.NativeAsset[:])
}

func storetypesGas() storetypes.GasMeter { return storetypes.NewInfiniteGasMeter() }
