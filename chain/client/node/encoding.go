package node

import (
	"fmt"

	"github.com/cosmos/gogoproto/proto"

	"github.com/cosmos/cosmos-sdk/x/tx/signing"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	archivetypes "github.com/DeBrosOfficial/network/chain/x/archive/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// register adds one module's interfaces to the client's registry.
type register func(codectypes.InterfaceRegistry)

type encoding struct {
	registry codectypes.InterfaceRegistry
	cdc      codec.Codec
	txConfig client.TxConfig
}

// newEncoding registers the SDK's standard interfaces, auth accounts, and the
// messages the global services sign. It is the same TxConfig construction as
// the app's, over a smaller registry, so the client does not link the node.
func newEncoding(extra ...register) (encoding, error) {
	registry, err := codectypes.NewInterfaceRegistryWithOptions(codectypes.InterfaceRegistryOptions{
		ProtoFiles: proto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec:          address.Bech32Codec{Bech32Prefix: params.Bech32Prefix},
			ValidatorAddressCodec: address.Bech32Codec{Bech32Prefix: params.Bech32PrefixValAddr},
		},
	})
	if err != nil {
		return encoding{}, fmt.Errorf("build the interface registry: %w", err)
	}
	std.RegisterInterfaces(registry)
	authtypes.RegisterInterfaces(registry)
	storagetypes.RegisterInterfaces(registry)
	archivetypes.RegisterInterfaces(registry)
	for _, reg := range extra {
		reg(registry)
	}
	if err := registry.SigningContext().Validate(); err != nil {
		return encoding{}, fmt.Errorf("validate the signing context: %w", err)
	}
	cdc := codec.NewProtoCodec(registry)
	return encoding{registry: registry, cdc: cdc, txConfig: authtx.NewTxConfig(cdc, authtx.DefaultSignModes)}, nil
}
