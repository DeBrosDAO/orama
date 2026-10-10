package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

// RegisterLegacyAminoCodec registers x/market's messages on the legacy Amino codec.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	legacy.RegisterAminoMsg(cdc, &MsgList{}, "orama/market/List")
	legacy.RegisterAminoMsg(cdc, &MsgCancelListing{}, "orama/market/CancelListing")
	legacy.RegisterAminoMsg(cdc, &MsgBid{}, "orama/market/Bid")
	legacy.RegisterAminoMsg(cdc, &MsgCancelBid{}, "orama/market/CancelBid")
	legacy.RegisterAminoMsg(cdc, &MsgSettle{}, "orama/market/Settle")
}

// RegisterInterfaces registers x/market's sdk.Msg implementations.
func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgList{},
		&MsgCancelListing{},
		&MsgBid{},
		&MsgCancelBid{},
		&MsgSettle{},
	)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
