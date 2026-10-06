package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

// RegisterLegacyAminoCodec is a no-op. x/relay messages are proto-signed; there
// is no legacy amino client.
func RegisterLegacyAminoCodec(*codec.LegacyAmino) {}

// RegisterInterfaces registers x/relay's sdk.Msg implementations.
func RegisterInterfaces(reg cdctypes.InterfaceRegistry) {
	reg.RegisterImplementations((*sdk.Msg)(nil),
		&MsgRegisterRelay{},
		&MsgReportEpoch{},
		&MsgUpdateReporters{},
	)
	msgservice.RegisterMsgServiceDesc(reg, &_Msg_serviceDesc)
}
