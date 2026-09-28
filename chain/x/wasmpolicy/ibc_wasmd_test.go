//go:build cgo && !nowasm

package wasmpolicy_test

import (
	"testing"

	wasmvmtypes "github.com/CosmWasm/wasmvm/v3/types"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/keeper"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

func TestWasmdContractPacketCannotOpenChannel(t *testing.T) {
	handler := wasmkeeper.NewIBCRawPacketHandler(wasmpolicy.ICS4Noop{}, nil)
	msg := wasmvmtypes.CosmosMsg{IBC: &wasmvmtypes.IBCMsg{SendPacket: &wasmvmtypes.SendPacketMsg{
		ChannelID: "channel-0",
		Data:      []byte{1},
	}}}
	_, _, _, err := handler.DispatchMsg(sdk.Context{}, sdk.AccAddress{}, "wasm.port", msg)
	require.ErrorIs(t, err, types.ErrIBCDisabled)
}
