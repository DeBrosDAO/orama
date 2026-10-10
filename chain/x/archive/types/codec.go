package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

// RegisterLegacyAminoCodec registers x/archive's messages on the Amino codec.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	legacy.RegisterAminoMsg(cdc, &MsgAttest{}, "orama/archive/MsgAttest")
	legacy.RegisterAminoMsg(cdc, &MsgAttachReplicas{}, "orama/archive/MsgAttachReplicas")
	legacy.RegisterAminoMsg(cdc, &MsgCreateArchiveDeal{}, "orama/archive/MsgCreateArchiveDeal")
}

// RegisterInterfaces registers x/archive's messages as sdk.Msg implementations.
// Every message is signed by their archiver field. There is no authority message.
func RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgAttest{},
		&MsgAttachReplicas{},
		&MsgCreateArchiveDeal{},
	)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
