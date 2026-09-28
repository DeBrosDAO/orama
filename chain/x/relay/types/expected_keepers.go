package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// NodeView is the subset of x/nodes x/relay reads. It is not imported: x/nodes
// does not exist in this binary yet. RelayBinding returns the RELAY role's
// ed25519 identity, the operator account, and the node's IPv4 address or CIDR
// (used only to derive the /16 cap bucket).
type NodeView interface {
	RelayBinding(ctx context.Context, nodeID string) (ed25519Pub []byte, operator sdk.AccAddress, ipv4 string, err error)
}

// EmissionKeeper is the subset of x/emission x/relay mints against. RelayCeiling
// is that epoch's relay ceiling in norama. MintRelayReward must refuse an amount
// that would exceed the ceiling and must never mint more than the ceiling.
// x/relay does not import x/emission.
type EmissionKeeper interface {
	RelayCeiling(ctx context.Context, epoch uint64) (math.Int, error)
	MintRelayReward(ctx context.Context, epoch uint64, amt math.Int) error
}

// EarningsKeeper is the subset of x/fees x/relay pays through. CreditEarnings
// moves amt from senderModule into the earnings module account and credits it
// to addr. Every relay payout goes here; there is no public bank balance.
type EarningsKeeper interface {
	CreditEarnings(ctx context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error
}
