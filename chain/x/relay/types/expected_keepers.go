package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// NodeView is the subset of x/nodes x/relay reads. It is not imported: x/nodes
// does not exist in this binary yet. RelayBinding returns the RELAY role's
// ed25519 identity, the operator account, and the node's effective network from
// x/nodes: a canonical "A.B.0.0/16" derived from its literal-IP endpoints, or ""
// when it has none or its identity is still inside network_identity_lock_seconds.
// It is used only to derive the /16 cap bucket (see RelayPrefix16).
type NodeView interface {
	RelayBinding(ctx context.Context, nodeID string) (ed25519Pub []byte, operator sdk.AccAddress, network string, err error)
}

// EmissionKeeper is the subset of x/emission x/relay mints against. RelayCeiling
// is that epoch's relay ceiling in norama. MintRelayReward must refuse an amount
// that would exceed the ceiling and must never mint more than the ceiling.
// CurrentEpoch is the epoch in progress: every epoch below it is closed and has
// a ceiling. x/relay does not import x/emission.
type EmissionKeeper interface {
	CurrentEpoch(ctx context.Context) (uint64, error)
	RelayCeiling(ctx context.Context, epoch uint64) (math.Int, error)
	MintRelayReward(ctx context.Context, epoch uint64, amt math.Int) error
}

// EarningsKeeper is the subset of x/fees x/relay pays through. CreditEarnings
// moves amt from senderModule into the earnings module account and credits it
// to addr. Every relay payout goes here; there is no public bank balance.
type EarningsKeeper interface {
	CreditEarnings(ctx context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error
}

// ServiceSplit is the C2 service-payment split x/relay pays every operator through, the same
// split and the same destinations x/storage uses. SplitServicePayment returns the operator's
// part, the burned part and the archive-fund part, which sum to amount; the operator receives
// the rounding remainder. BurnService burns amt from senderModule. FundArchive moves amt from
// senderModule into the archive fund and records it there. x/relay does not import x/storage.
type ServiceSplit interface {
	SplitServicePayment(amount math.Int) (toOperator, burn, archive math.Int)
	BurnService(ctx context.Context, senderModule string, amt math.Int) error
	FundArchive(ctx context.Context, senderModule string, amt math.Int) error
}
