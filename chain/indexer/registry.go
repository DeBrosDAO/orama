package indexer

import (
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	"github.com/cosmos/cosmos-sdk/x/feegrant"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	archivetypes "github.com/DeBrosOfficial/network/chain/x/archive/types"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	houstypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// newCodec returns a codec that knows every message type the chain's modules
// accept except the CosmWasm ones: the index turns a message body into JSON
// with it. A message of any other type is kept as its type URL alone.
func newCodec() *codec.ProtoCodec {
	registry := codectypes.NewInterfaceRegistry()
	std.RegisterInterfaces(registry)
	banktypes.RegisterInterfaces(registry)
	stakingtypes.RegisterInterfaces(registry)
	distrtypes.RegisterInterfaces(registry)
	slashingtypes.RegisterInterfaces(registry)
	feegrant.RegisterInterfaces(registry)
	archivetypes.RegisterInterfaces(registry)
	cnfttypes.RegisterInterfaces(registry)
	emissiontypes.RegisterInterfaces(registry)
	houstypes.RegisterInterfaces(registry)
	markettypes.RegisterInterfaces(registry)
	nodestypes.RegisterInterfaces(registry)
	relaytypes.RegisterInterfaces(registry)
	shieldedtypes.RegisterInterfaces(registry)
	storagetypes.RegisterInterfaces(registry)
	tokentypes.RegisterInterfaces(registry)
	return codec.NewProtoCodec(registry)
}
