package wasmpolicy

import (
	"fmt"

	clienttypes "github.com/cosmos/ibc-go/v11/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
	porttypes "github.com/cosmos/ibc-go/v11/modules/core/05-port/types"
	ibcexported "github.com/cosmos/ibc-go/v11/modules/core/exported"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// ContractIBC is the part of a contract message that would open or use an IBC channel.
type ContractIBC struct {
	OpenChannel  bool
	SendPacket   bool
	CloseChannel bool
	Transfer     bool
}

// RejectContractIBC rejects every contract message that opens or uses a channel.
// A message that does none of those is left alone.
func RejectContractIBC(msg ContractIBC) error {
	if msg.OpenChannel || msg.SendPacket || msg.CloseChannel || msg.Transfer {
		return types.ErrIBCDisabled
	}
	return nil
}

// ICS4Noop is the ICS4Wrapper passed to wasmd. wasmd v0.70.3 does not ship a production
// noop (its test mock panics when SendPacket is unset). This wrapper fails closed:
// a contract cannot open a channel or send a packet. ibc-go is not a wired module.
type ICS4Noop struct{}

var _ porttypes.ICS4Wrapper = ICS4Noop{}

// SendPacket refuses a contract packet. No channel is opened.
func (ICS4Noop) SendPacket(sdk.Context, string, string, clienttypes.Height, uint64, []byte) (uint64, error) {
	if err := RejectContractIBC(ContractIBC{SendPacket: true}); err != nil {
		return 0, fmt.Errorf("send packet: %w", err)
	}
	return 0, nil
}

// WriteAcknowledgement refuses a contract acknowledgement.
func (ICS4Noop) WriteAcknowledgement(sdk.Context, ibcexported.PacketI, ibcexported.Acknowledgement) error {
	return fmt.Errorf("write acknowledgement: %w", RejectContractIBC(ContractIBC{SendPacket: true}))
}

// GetAppVersion reports no channel. There is no IBC app version to negotiate.
func (ICS4Noop) GetAppVersion(sdk.Context, string, string) (string, bool) {
	return "", false
}

// OnChanOpenInit refuses the channel handshake. The chain never registers an IBC module,
// so this is the same answer core IBC would fail to route.
func (ICS4Noop) OnChanOpenInit(
	sdk.Context,
	channeltypes.Order,
	[]string,
	string,
	string,
	channeltypes.Counterparty,
	string,
) (string, error) {
	return "", fmt.Errorf("channel open: %w", RejectContractIBC(ContractIBC{OpenChannel: true}))
}
