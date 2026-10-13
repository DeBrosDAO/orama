package types

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

// BankKeeper moves escrow and buy-now payments into and out of the market module
// account. Sale proceeds are not sent to a user account through this interface.
type BankKeeper interface {
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}

// EarningsKeeper credits sale proceeds and royalties. CreditEarnings must not
// increase the recipient's public bank balance.
type EarningsKeeper interface {
	CreditEarnings(ctx context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error
}

// CnftKeeper is the subset of x/cnft x/market uses. Royalty is read from the
// collection at listing time. TransferForSale does not itself pay a royalty.
type CnftKeeper interface {
	CollectionRoyalty(ctx context.Context, collectionID uint64) (creator sdk.AccAddress, royaltyBps uint32, err error)
	ProveOwned(ctx context.Context, treeID uint64, leaf cnfttypes.Leaf, proof cnfttypes.MerkleProof, owner sdk.AccAddress) (collectionID uint64, err error)
	TransferForSale(ctx context.Context, treeID uint64, leaf cnfttypes.Leaf, proof cnfttypes.MerkleProof, seller, buyer sdk.AccAddress) error
}
