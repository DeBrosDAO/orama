package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	"github.com/DeBrosOfficial/network/chain/x/market/types"
)

type msgServer struct {
	Keeper
}

// NewMsgServer returns x/market's Msg implementation.
func NewMsgServer(k Keeper) types.MsgServer {
	return msgServer{Keeper: k}
}

func (m msgServer) List(ctx context.Context, msg *types.MsgList) (*types.MsgListResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	seller, err := sdk.AccAddressFromBech32(msg.Seller)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	id, err := m.list(ctx, seller, msg.TreeId, msg.Leaf, msg.Proof, msg.Price)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	return &types.MsgListResponse{Id: id}, nil
}

func (m msgServer) CancelListing(ctx context.Context, msg *types.MsgCancelListing) (*types.MsgCancelListingResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("cancel listing: %w", err)
	}
	seller, err := sdk.AccAddressFromBech32(msg.Seller)
	if err != nil {
		return nil, fmt.Errorf("cancel listing: %w", err)
	}
	if err := m.cancelListing(ctx, seller, msg.ListingId); err != nil {
		return nil, fmt.Errorf("cancel listing: %w", err)
	}
	return &types.MsgCancelListingResponse{}, nil
}

func (m msgServer) Bid(ctx context.Context, msg *types.MsgBid) (*types.MsgBidResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("bid: %w", err)
	}
	bidder, err := sdk.AccAddressFromBech32(msg.Bidder)
	if err != nil {
		return nil, fmt.Errorf("bid: %w", err)
	}
	id, err := m.bid(ctx, bidder, msg.ListingId, msg.Amount)
	if err != nil {
		return nil, fmt.Errorf("bid: %w", err)
	}
	return &types.MsgBidResponse{Id: id}, nil
}

func (m msgServer) CancelBid(ctx context.Context, msg *types.MsgCancelBid) (*types.MsgCancelBidResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("cancel bid: %w", err)
	}
	bidder, err := sdk.AccAddressFromBech32(msg.Bidder)
	if err != nil {
		return nil, fmt.Errorf("cancel bid: %w", err)
	}
	if err := m.cancelBid(ctx, bidder, msg.ListingId, msg.BidId); err != nil {
		return nil, fmt.Errorf("cancel bid: %w", err)
	}
	return &types.MsgCancelBidResponse{}, nil
}

func (m msgServer) Settle(ctx context.Context, msg *types.MsgSettle) (*types.MsgSettleResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("settle: %w", err)
	}
	signer, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, fmt.Errorf("settle: %w", err)
	}
	price, royalty, sellerProceeds, err := m.settle(ctx, signer, msg.ListingId, msg.BidId, msg.Leaf, msg.Proof)
	if err != nil {
		return nil, fmt.Errorf("settle: %w", err)
	}
	return &types.MsgSettleResponse{Price: price, Royalty: royalty, SellerProceeds: sellerProceeds}, nil
}

func (k Keeper) list(ctx context.Context, seller sdk.AccAddress, treeID uint64, leaf cnfttypes.Leaf, proof cnfttypes.MerkleProof, price math.Int) (uint64, error) {
	collectionID, err := k.cnft.ProveOwned(ctx, treeID, leaf, proof, seller)
	if err != nil {
		return 0, err
	}
	open, err := k.AssetListing.Has(ctx, leaf.AssetId)
	if err != nil {
		return 0, fmt.Errorf("failed to check existing listing: %w", err)
	}
	if open {
		return 0, fmt.Errorf("asset is already listed")
	}
	creator, bps, err := k.cnft.CollectionRoyalty(ctx, collectionID)
	if err != nil {
		return 0, err
	}
	id, err := k.takeID(ctx, k.NextListingID, "listing")
	if err != nil {
		return 0, err
	}
	listing := types.Listing{
		Id:             id,
		Seller:         seller.String(),
		TreeId:         treeID,
		LeafIndex:      proof.Index,
		AssetId:        append([]byte(nil), leaf.AssetId...),
		Price:          price,
		RoyaltyCreator: creator.String(),
		RoyaltyBps:     bps,
		CollectionId:   collectionID,
	}
	if err := k.Listings.Set(ctx, id, listing); err != nil {
		return 0, fmt.Errorf("failed to store listing %d: %w", id, err)
	}
	if err := k.AssetListing.Set(ctx, listing.AssetId, id); err != nil {
		return 0, fmt.Errorf("failed to index listing %d: %w", id, err)
	}
	return id, nil
}

func (k Keeper) bid(ctx context.Context, bidder sdk.AccAddress, listingID uint64, amount math.Int) (uint64, error) {
	listing, err := k.loadListing(ctx, listingID)
	if err != nil {
		return 0, err
	}
	seller, err := sdk.AccAddressFromBech32(listing.Seller)
	if err != nil {
		return 0, err
	}
	if bidder.Equals(seller) {
		return 0, fmt.Errorf("seller cannot bid on their own listing")
	}
	if err := k.bank.SendCoinsFromAccountToModule(ctx, bidder, types.ModuleName, sdk.NewCoins(coin(amount))); err != nil {
		return 0, fmt.Errorf("failed to escrow bid: %w", err)
	}
	id, err := k.takeID(ctx, k.NextBidID, "bid")
	if err != nil {
		return 0, err
	}
	stored := types.Bid{Id: id, ListingId: listingID, Bidder: bidder.String(), Amount: amount}
	if err := k.Bids.Set(ctx, collections.Join(listingID, id), stored); err != nil {
		return 0, fmt.Errorf("failed to store bid %d: %w", id, err)
	}
	return id, nil
}

func (k Keeper) cancelBid(ctx context.Context, bidder sdk.AccAddress, listingID, bidID uint64) error {
	bid, err := k.Bids.Get(ctx, collections.Join(listingID, bidID))
	if err != nil {
		return fmt.Errorf("bid %d does not exist: %w", bidID, err)
	}
	owner, err := sdk.AccAddressFromBech32(bid.Bidder)
	if err != nil {
		return err
	}
	if !owner.Equals(bidder) {
		return fmt.Errorf("signer is not the bidder")
	}
	return k.refundBid(ctx, bid)
}

func (k Keeper) cancelListing(ctx context.Context, seller sdk.AccAddress, listingID uint64) error {
	listing, err := k.loadListing(ctx, listingID)
	if err != nil {
		return err
	}
	owner, err := sdk.AccAddressFromBech32(listing.Seller)
	if err != nil {
		return err
	}
	if !owner.Equals(seller) {
		return fmt.Errorf("signer is not the seller")
	}
	if err := k.refundListingBids(ctx, listingID, 0); err != nil {
		return err
	}
	return k.deleteListing(ctx, listing)
}

func (k Keeper) settle(ctx context.Context, signer sdk.AccAddress, listingID, bidID uint64, leaf cnfttypes.Leaf, proof cnfttypes.MerkleProof) (price, royalty, sellerProceeds math.Int, err error) {
	listing, err := k.loadListing(ctx, listingID)
	if err != nil {
		return math.Int{}, math.Int{}, math.Int{}, err
	}
	if proof.Index != listing.LeafIndex || !bytes.Equal(leaf.AssetId, listing.AssetId) {
		return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("leaf does not match the listing")
	}
	seller, err := sdk.AccAddressFromBech32(listing.Seller)
	if err != nil {
		return math.Int{}, math.Int{}, math.Int{}, err
	}
	creator, err := sdk.AccAddressFromBech32(listing.RoyaltyCreator)
	if err != nil {
		return math.Int{}, math.Int{}, math.Int{}, err
	}

	var buyer sdk.AccAddress
	if bidID == 0 {
		buyer = signer
		if buyer.Equals(seller) {
			return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("seller cannot buy their own listing")
		}
		price = listing.Price
		balance := k.bank.GetBalance(ctx, buyer, params.BaseDenom).Amount
		if balance.LT(price) {
			return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("buyer balance %s is below price %s", balance, price)
		}
	} else {
		if !signer.Equals(seller) {
			return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("only the seller can accept a bid")
		}
		bid, err := k.Bids.Get(ctx, collections.Join(listingID, bidID))
		if err != nil {
			return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("bid %d does not exist: %w", bidID, err)
		}
		buyer, err = sdk.AccAddressFromBech32(bid.Bidder)
		if err != nil {
			return math.Int{}, math.Int{}, math.Int{}, err
		}
		price = bid.Amount
	}

	if _, err := k.cnft.ProveOwned(ctx, listing.TreeId, leaf, proof, seller); err != nil {
		return math.Int{}, math.Int{}, math.Int{}, err
	}
	royalty, sellerProceeds, err = types.SplitSale(price, listing.RoyaltyBps)
	if err != nil {
		return math.Int{}, math.Int{}, math.Int{}, err
	}

	if bidID == 0 {
		if err := k.bank.SendCoinsFromAccountToModule(ctx, buyer, types.ModuleName, sdk.NewCoins(coin(price))); err != nil {
			return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("failed to take payment: %w", err)
		}
	} else if err := k.Bids.Remove(ctx, collections.Join(listingID, bidID)); err != nil {
		return math.Int{}, math.Int{}, math.Int{}, fmt.Errorf("failed to consume bid %d: %w", bidID, err)
	}
	if err := k.refundListingBids(ctx, listingID, bidID); err != nil {
		return math.Int{}, math.Int{}, math.Int{}, err
	}
	if err := k.cnft.TransferForSale(ctx, listing.TreeId, leaf, proof, seller, buyer); err != nil {
		return math.Int{}, math.Int{}, math.Int{}, err
	}
	if err := k.payProceeds(ctx, creator, seller, royalty, sellerProceeds); err != nil {
		return math.Int{}, math.Int{}, math.Int{}, err
	}
	if err := k.deleteListing(ctx, listing); err != nil {
		return math.Int{}, math.Int{}, math.Int{}, err
	}
	return price, royalty, sellerProceeds, nil
}

func (k Keeper) payProceeds(ctx context.Context, creator, seller sdk.AccAddress, royalty, sellerProceeds math.Int) error {
	if royalty.IsPositive() {
		if err := k.earnings.CreditEarnings(ctx, types.ModuleName, creator, coin(royalty)); err != nil {
			return fmt.Errorf("failed to credit royalty: %w", err)
		}
	}
	if sellerProceeds.IsPositive() {
		if err := k.earnings.CreditEarnings(ctx, types.ModuleName, seller, coin(sellerProceeds)); err != nil {
			return fmt.Errorf("failed to credit seller: %w", err)
		}
	}
	return nil
}

func (k Keeper) refundListingBids(ctx context.Context, listingID, keep uint64) error {
	var bids []types.Bid
	rng := collections.NewPrefixedPairRange[uint64, uint64](listingID)
	if err := k.Bids.Walk(ctx, rng, func(_ collections.Pair[uint64, uint64], bid types.Bid) (bool, error) {
		if bid.Id != keep {
			bids = append(bids, bid)
		}
		return false, nil
	}); err != nil {
		return fmt.Errorf("failed to walk bids: %w", err)
	}
	for _, bid := range bids {
		if err := k.refundBid(ctx, bid); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) refundBid(ctx context.Context, bid types.Bid) error {
	bidder, err := sdk.AccAddressFromBech32(bid.Bidder)
	if err != nil {
		return fmt.Errorf("bid %d bidder: %w", bid.Id, err)
	}
	if err := k.bank.SendCoinsFromModuleToAccount(ctx, types.ModuleName, bidder, sdk.NewCoins(coin(bid.Amount))); err != nil {
		return fmt.Errorf("failed to refund bid %d: %w", bid.Id, err)
	}
	if err := k.Bids.Remove(ctx, collections.Join(bid.ListingId, bid.Id)); err != nil {
		return fmt.Errorf("failed to remove bid %d: %w", bid.Id, err)
	}
	return nil
}

func (k Keeper) deleteListing(ctx context.Context, listing types.Listing) error {
	if err := k.AssetListing.Remove(ctx, listing.AssetId); err != nil {
		return fmt.Errorf("failed to clear listing index: %w", err)
	}
	if err := k.Listings.Remove(ctx, listing.Id); err != nil {
		return fmt.Errorf("failed to remove listing %d: %w", listing.Id, err)
	}
	return nil
}
