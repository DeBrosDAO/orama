package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

// RegisterLegacyAminoCodec registers x/token's messages on the legacy Amino codec.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	legacy.RegisterAminoMsg(cdc, &MsgCreateToken{}, "orama/token/MsgCreateToken")
	legacy.RegisterAminoMsg(cdc, &MsgMint{}, "orama/token/MsgMint")
	legacy.RegisterAminoMsg(cdc, &MsgBurn{}, "orama/token/MsgBurn")
	legacy.RegisterAminoMsg(cdc, &MsgTransfer{}, "orama/token/MsgTransfer")
	legacy.RegisterAminoMsg(cdc, &MsgSetFrozen{}, "orama/token/MsgSetFrozen")
	legacy.RegisterAminoMsg(cdc, &MsgSetPaused{}, "orama/token/MsgSetPaused")
	legacy.RegisterAminoMsg(cdc, &MsgRenounce{}, "orama/token/MsgRenounce")
	legacy.RegisterAminoMsg(cdc, &MsgSetShieldable{}, "orama/token/MsgSetShieldable")
	legacy.RegisterAminoMsg(cdc, &MsgDeleteToken{}, "orama/token/MsgDeleteToken")
}

// RegisterInterfaces registers x/token's messages and Msg service descriptor.
func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgCreateToken{},
		&MsgMint{},
		&MsgBurn{},
		&MsgTransfer{},
		&MsgSetFrozen{},
		&MsgSetPaused{},
		&MsgRenounce{},
		&MsgSetShieldable{},
		&MsgDeleteToken{},
	)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
