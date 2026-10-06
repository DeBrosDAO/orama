package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

// RegisterLegacyAminoCodec registers x/shielded messages on the amino codec.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	legacy.RegisterAminoMsg(cdc, &MsgShieldedTransfer{}, "orama/shielded/MsgShieldedTransfer")
	legacy.RegisterAminoMsg(cdc, &MsgShield{}, "orama/shielded/MsgShield")
	legacy.RegisterAminoMsg(cdc, &MsgShieldEarnings{}, "orama/shielded/MsgShieldEarnings")
	legacy.RegisterAminoMsg(cdc, &MsgUnshield{}, "orama/shielded/MsgUnshield")
}

// RegisterInterfaces registers x/shielded messages as sdk.Msg implementations.
func RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgShieldedTransfer{},
		&MsgShield{},
		&MsgShieldEarnings{},
		&MsgUnshield{},
	)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
