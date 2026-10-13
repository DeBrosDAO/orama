package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
	"github.com/cosmos/gogoproto/proto"
)

// GovernanceMsgs is every sdk.Msg this module accepts. The ossified-field
// test walks this list, and RegisterInterfaces registers this list, so a new
// message cannot skip either one.
func GovernanceMsgs() []proto.Message {
	return []proto.Message{
		&MsgSubmitProposal{},
		&MsgVoteToken{},
		&MsgVoteOperator{},
		&MsgLockHouseBond{},
		&MsgUnlockHouseBond{},
		&MsgExecuteProposal{},
	}
}

// RegisterLegacyAminoCodec registers x/houses messages.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	legacy.RegisterAminoMsg(cdc, &MsgSubmitProposal{}, "orama/houses/MsgSubmitProposal")
	legacy.RegisterAminoMsg(cdc, &MsgVoteToken{}, "orama/houses/MsgVoteToken")
	legacy.RegisterAminoMsg(cdc, &MsgVoteOperator{}, "orama/houses/MsgVoteOperator")
	legacy.RegisterAminoMsg(cdc, &MsgLockHouseBond{}, "orama/houses/MsgLockHouseBond")
	legacy.RegisterAminoMsg(cdc, &MsgUnlockHouseBond{}, "orama/houses/MsgUnlockHouseBond")
	legacy.RegisterAminoMsg(cdc, &MsgExecuteProposal{}, "orama/houses/MsgExecuteProposal")
}

// RegisterInterfaces registers x/houses messages as sdk.Msg.
func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil), GovernanceMsgs()...)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}

// MsgServiceMethods returns the Msg service method names.
func MsgServiceMethods() []string {
	names := make([]string, len(_Msg_serviceDesc.Methods))
	for i, m := range _Msg_serviceDesc.Methods {
		names[i] = m.MethodName
	}
	return names
}
