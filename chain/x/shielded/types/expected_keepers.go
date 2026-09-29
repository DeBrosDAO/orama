package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
)

// BankKeeper is the subset of x/bank the module uses.
type BankKeeper interface {
	SendCoinsFromAccountToModule(ctx context.Context, from sdk.AccAddress, module string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, module string, to sdk.AccAddress, amt sdk.Coins) error
	SendCoinsFromModuleToModule(ctx context.Context, from, to string, amt sdk.Coins) error
	BurnCoins(ctx context.Context, module string, amt sdk.Coins) error
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}

// FeesKeeper is the subset of x/fees the module uses: earnings accounts and the base fee.
type FeesKeeper interface {
	// CreditEarnings moves amt from senderModule into x/fees and credits addr's earnings.
	CreditEarnings(ctx context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error
	// DebitEarningsUpTo debits up to want from addr's earnings ledger and returns what it
	// debited. The coins stay in x/fees' account for the caller to move.
	DebitEarningsUpTo(ctx context.Context, addr sdk.AccAddress, want math.Int) (math.Int, error)
	// BaseFeePerGas is the current base fee in norama per gas.
	BaseFeePerGas(ctx context.Context) (math.Int, error)
	// EarningsModule is x/fees' module account, where earnings coins sit.
	EarningsModule() string
	// ProposerAccount is the account that owns the current block's proposer, if it resolves.
	ProposerAccount(ctx sdk.Context) (sdk.AccAddress, bool)
}

// Bonder delegates to a validator from the delegator's own bank balance.
type Bonder interface {
	Delegate(ctx sdk.Context, delegator sdk.AccAddress, validator string, amount math.Int) error
}

// NodeBonder adds to the signer's own x/nodes role bond from the operator's bank balance.
type NodeBonder interface {
	BondNode(ctx sdk.Context, msg *nodestypes.MsgBondNode) error
}

// NullifierStore is the append-only nullifier database outside IAVL. A record is visible to a
// reader at height asOf when it was written at a height below asOf, so a replayed or rolled-back
// block never sees its own or a later block's records.
type NullifierStore interface {
	// Spent reports whether nf was recorded at a height below asOf.
	Spent(nf [bundle.NodeLen]byte, asOf int64) (bool, error)
	// Commit writes the block's nullifiers at height, after removing every record at or above it.
	Commit(height int64, nfs [][bundle.NodeLen]byte) error
	// Walk calls fn for every record in insertion order.
	Walk(fn func(nf [bundle.NodeLen]byte, height int64) error) error
	// Import appends records at height 0, in order, into a store that has none (genesis import).
	Import(nfs [][bundle.NodeLen]byte) error
	// Empty reports whether the store holds no record.
	Empty() (bool, error)
}

// Tree appends note commitments to the note-commitment tree frontier and reads the empty root.
type Tree interface {
	Append(frontier []byte, commitments [][bundle.NodeLen]byte) ([]byte, [bundle.NodeLen]byte, error)
	EmptyRoot() ([bundle.NodeLen]byte, error)
}
