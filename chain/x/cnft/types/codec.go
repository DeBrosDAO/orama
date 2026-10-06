package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

// RegisterLegacyAminoCodec registers x/cnft's messages on the legacy Amino codec.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	legacy.RegisterAminoMsg(cdc, &MsgCreateCollection{}, "orama/cnft/CreateCollection")
	legacy.RegisterAminoMsg(cdc, &MsgCreateTree{}, "orama/cnft/CreateTree")
	legacy.RegisterAminoMsg(cdc, &MsgMint{}, "orama/cnft/Mint")
	legacy.RegisterAminoMsg(cdc, &MsgTransfer{}, "orama/cnft/Transfer")
	legacy.RegisterAminoMsg(cdc, &MsgBurn{}, "orama/cnft/Burn")
	legacy.RegisterAminoMsg(cdc, &MsgUpdateMetadata{}, "orama/cnft/UpdateMetadata")
	legacy.RegisterAminoMsg(cdc, &MsgDecompress{}, "orama/cnft/Decompress")
	legacy.RegisterAminoMsg(cdc, &MsgCompress{}, "orama/cnft/Compress")
	legacy.RegisterAminoMsg(cdc, &MsgRecordSnapshot{}, "orama/cnft/RecordSnapshot")
}

// RegisterInterfaces registers x/cnft's sdk.Msg implementations.
func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgCreateCollection{},
		&MsgCreateTree{},
		&MsgMint{},
		&MsgTransfer{},
		&MsgBurn{},
		&MsgUpdateMetadata{},
		&MsgDecompress{},
		&MsgCompress{},
		&MsgRecordSnapshot{},
	)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
