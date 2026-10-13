package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

// RegisterLegacyAminoCodec registers x/storage messages. There is no cancel message.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	// Names stay under amino's 39-character ledger limit.
	legacy.RegisterAminoMsg(cdc, &MsgCreateDeal{}, "orama/storage/CreateDeal")
	legacy.RegisterAminoMsg(cdc, &MsgExtendDeal{}, "orama/storage/ExtendDeal")
	legacy.RegisterAminoMsg(cdc, &MsgGrantDealAuthorization{}, "orama/storage/GrantDealAuth")
	legacy.RegisterAminoMsg(cdc, &MsgRevokeDealAuthorization{}, "orama/storage/RevokeDealAuth")
	legacy.RegisterAminoMsg(cdc, &MsgAcceptDeal{}, "orama/storage/AcceptDeal")
	legacy.RegisterAminoMsg(cdc, &MsgDeclineDeal{}, "orama/storage/DeclineDeal")
	legacy.RegisterAminoMsg(cdc, &MsgSubmitProofs{}, "orama/storage/SubmitProofs")
	legacy.RegisterAminoMsg(cdc, &MsgReleaseReplica{}, "orama/storage/ReleaseReplica")
}

// RegisterInterfaces registers x/storage's sdk.Msg implementations.
// Msg service methods are exactly the eight above; protocol deals are not messages.
func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgCreateDeal{},
		&MsgExtendDeal{},
		&MsgGrantDealAuthorization{},
		&MsgRevokeDealAuthorization{},
		&MsgAcceptDeal{},
		&MsgDeclineDeal{},
		&MsgSubmitProofs{},
		&MsgReleaseReplica{},
	)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
