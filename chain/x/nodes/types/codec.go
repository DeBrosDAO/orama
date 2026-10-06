package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

// RegisterLegacyAminoCodec registers x/nodes messages on the amino codec.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	legacy.RegisterAminoMsg(cdc, &MsgRegisterOperator{}, "orama/nodes/MsgRegisterOperator")
	legacy.RegisterAminoMsg(cdc, &MsgRegisterNode{}, "orama/nodes/MsgRegisterNode")
	legacy.RegisterAminoMsg(cdc, &MsgUpdateNode{}, "orama/nodes/MsgUpdateNode")
	legacy.RegisterAminoMsg(cdc, &MsgRetireNode{}, "orama/nodes/MsgRetireNode")
	legacy.RegisterAminoMsg(cdc, &MsgBondNode{}, "orama/nodes/MsgBondNode")
	legacy.RegisterAminoMsg(cdc, &MsgUnbondNode{}, "orama/nodes/MsgUnbondNode")
	legacy.RegisterAminoMsg(cdc, &MsgDeclareCapacity{}, "orama/nodes/MsgDeclareCapacity")
	legacy.RegisterAminoMsg(cdc, &MsgFundHotKey{}, "orama/nodes/MsgFundHotKey")
	legacy.RegisterAminoMsg(cdc, &MsgRegisterCluster{}, "orama/nodes/MsgRegisterCluster")
	legacy.RegisterAminoMsg(cdc, &MsgUpdateCluster{}, "orama/nodes/MsgUpdateCluster")
	legacy.RegisterAminoMsg(cdc, &MsgRetireCluster{}, "orama/nodes/MsgRetireCluster")
}

// RegisterInterfaces registers x/nodes messages as sdk.Msg implementations.
func RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&MsgRegisterOperator{},
		&MsgRegisterNode{},
		&MsgUpdateNode{},
		&MsgRetireNode{},
		&MsgBondNode{},
		&MsgUnbondNode{},
		&MsgDeclareCapacity{},
		&MsgFundHotKey{},
		&MsgRegisterCluster{},
		&MsgUpdateCluster{},
		&MsgRetireCluster{},
	)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
