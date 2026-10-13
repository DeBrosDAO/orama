package wasmpolicy_test

import (
	"testing"

	clienttypes "github.com/cosmos/ibc-go/v11/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

func TestContractMessageCannotOpenChannel(t *testing.T) {
	noop := wasmpolicy.ICS4Noop{}

	_, err := noop.OnChanOpenInit(sdk.Context{}, channeltypes.UNORDERED, nil, "wasm.port", "channel-0", channeltypes.Counterparty{}, "")
	require.ErrorIs(t, err, types.ErrIBCDisabled)

	_, err = noop.SendPacket(sdk.Context{}, "wasm.port", "channel-0", clienttypes.Height{}, 0, []byte{1})
	require.ErrorIs(t, err, types.ErrIBCDisabled)

	err = noop.WriteAcknowledgement(sdk.Context{}, nil, nil)
	require.ErrorIs(t, err, types.ErrIBCDisabled)

	version, found := noop.GetAppVersion(sdk.Context{}, "wasm.port", "channel-0")
	require.False(t, found)
	require.Empty(t, version)

	require.NoError(t, wasmpolicy.RejectContractIBC(wasmpolicy.ContractIBC{}))
}
