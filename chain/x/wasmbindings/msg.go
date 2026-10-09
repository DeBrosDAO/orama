package wasmbindings

import (
	"bytes"
	"encoding/json"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// Msg is the JSON a contract puts in CosmosMsg::Custom. Exactly one field is set. Unknown fields
// are refused. There is no sender field anywhere: the contract is always the signer.
//
//	{"token":{"mint":{"denom":"factory/orama1.../gold","recipient":"orama1...","amount":"5"}}}
type Msg struct {
	Token    *TokenMsg       `json:"token,omitempty"`
	CNFT     *CNFTMsg        `json:"cnft,omitempty"`
	Market   *MarketMsg      `json:"market,omitempty"`
	Storage  *StorageMsg     `json:"storage,omitempty"`
	Earnings *EarningsMsg    `json:"earnings,omitempty"`
	Shielded json.RawMessage `json:"shielded,omitempty"`
}

// TokenMsg is the x/token binding: create, mint and burn for tokens the contract administers.
type TokenMsg struct {
	Create *TokenCreate `json:"create,omitempty"`
	Mint   *TokenMint   `json:"mint,omitempty"`
	Burn   *TokenBurn   `json:"burn,omitempty"`
}

// TokenCreate mirrors x/token MsgCreateToken without the creator.
type TokenCreate struct {
	Subdenom          string `json:"subdenom"`
	Name              string `json:"name"`
	Symbol            string `json:"symbol"`
	Description       string `json:"description"`
	Mint              bool   `json:"mint"`
	Freeze            bool   `json:"freeze"`
	PermanentDelegate string `json:"permanent_delegate"`
	TransferFeeBps    uint32 `json:"transfer_fee_bps"`
	NonTransferable   bool   `json:"non_transferable"`
	Pause             bool   `json:"pause"`
	TransferHook      string `json:"transfer_hook"`
}

// TokenMint mints a token the contract created and still holds mint authority over.
type TokenMint struct {
	Denom     string   `json:"denom"`
	Recipient string   `json:"recipient"`
	Amount    math.Int `json:"amount"`
}

// TokenBurn burns from the contract's own balance.
type TokenBurn struct {
	Denom  string   `json:"denom"`
	Amount math.Int `json:"amount"`
}

// CNFTMsg is the x/cnft binding: collections, trees and mints the contract owns.
type CNFTMsg struct {
	CreateCollection *CNFTCreateCollection `json:"create_collection,omitempty"`
	CreateTree       *CNFTCreateTree       `json:"create_tree,omitempty"`
	Mint             *CNFTMint             `json:"mint,omitempty"`
}

// CNFTCreateCollection mirrors MsgCreateCollection without the creator.
type CNFTCreateCollection struct {
	Name       string `json:"name"`
	RoyaltyBps uint32 `json:"royalty_bps"`
}

// CNFTCreateTree mirrors MsgCreateTree without the creator.
type CNFTCreateTree struct {
	CollectionID uint64 `json:"collection_id"`
	Depth        uint32 `json:"depth"`
	Buffer       uint32 `json:"buffer"`
	Canopy       uint32 `json:"canopy"`
}

// CNFTMint mirrors MsgMint without the creator.
type CNFTMint struct {
	TreeID uint64               `json:"tree_id"`
	Root   []byte               `json:"root"`
	Leaves []cnfttypes.MintLeaf `json:"leaves"`
}

// MarketMsg is the x/market binding. The contract is the seller, bidder or settler.
type MarketMsg struct {
	List          *MarketList          `json:"list,omitempty"`
	CancelListing *MarketCancelListing `json:"cancel_listing,omitempty"`
	Bid           *MarketBid           `json:"bid,omitempty"`
	CancelBid     *MarketCancelBid     `json:"cancel_bid,omitempty"`
	Settle        *MarketSettle        `json:"settle,omitempty"`
}

// MarketList mirrors MsgList without the seller.
type MarketList struct {
	TreeID uint64                `json:"tree_id"`
	Leaf   cnfttypes.Leaf        `json:"leaf"`
	Proof  cnfttypes.MerkleProof `json:"proof"`
	Price  math.Int              `json:"price"`
}

// MarketCancelListing mirrors MsgCancelListing without the seller.
type MarketCancelListing struct {
	ListingID uint64 `json:"listing_id"`
}

// MarketBid mirrors MsgBid without the bidder.
type MarketBid struct {
	ListingID uint64   `json:"listing_id"`
	Amount    math.Int `json:"amount"`
}

// MarketCancelBid mirrors MsgCancelBid without the bidder.
type MarketCancelBid struct {
	ListingID uint64 `json:"listing_id"`
	BidID     uint64 `json:"bid_id"`
}

// MarketSettle mirrors MsgSettle without the signer.
type MarketSettle struct {
	ListingID uint64                `json:"listing_id"`
	BidID     uint64                `json:"bid_id"`
	Leaf      cnfttypes.Leaf        `json:"leaf"`
	Proof     cnfttypes.MerkleProof `json:"proof"`
}

// StorageMsg is the x/storage binding: a deal paid from the contract's own funds.
type StorageMsg struct {
	CreateDeal *StorageCreateDeal `json:"create_deal,omitempty"`
}

// StorageCreateDeal mirrors MsgCreateDeal without the signer and the granter: a contract never
// spends another account's deal allowance.
type StorageCreateDeal struct {
	Class          storagetypes.DealClass         `json:"class"`
	DealNonce      []byte                         `json:"deal_nonce"`
	RepairDelegate string                         `json:"repair_delegate"`
	Replicas       uint32                         `json:"replicas"`
	PricePerEpoch  math.Int                       `json:"price_per_epoch"`
	DurationEpochs uint64                         `json:"duration_epochs"`
	Pieces         []storagetypes.PieceCommitment `json:"pieces"`
}

// EarningsMsg pays norama to a user's earnings account. It is how a contract pays a user in ORAMA:
// bank sends from a contract to a user are refused, so the payment lands in the user's earnings
// (C2) and never as a public balance.
type EarningsMsg struct {
	Pay *EarningsPay `json:"pay,omitempty"`
}

// EarningsPay pays Amount norama from the contract's balance into Recipient's earnings.
type EarningsPay struct {
	Recipient string   `json:"recipient"`
	Amount    math.Int `json:"amount"`
}

// Action is a decoded custom message: either SDK messages to route, or an earnings payment.
type Action struct {
	Msgs []sdk.Msg
	Pay  *EarningsPay
}

// Decode parses a custom message and builds the action for contract. It refuses shielded
// messages with NOT_LINKED and does not execute anything.
func Decode(contract sdk.AccAddress, raw json.RawMessage) (Action, error) {
	var m Msg
	if err := decodeStrict(raw, &m); err != nil {
		return Action{}, err
	}
	if err := one("message", m.Token != nil, m.CNFT != nil, m.Market != nil, m.Storage != nil, m.Earnings != nil, m.Shielded != nil); err != nil {
		return Action{}, err
	}
	who := contract.String()
	switch {
	case m.Shielded != nil:
		return Action{}, ErrNotLinked
	case m.Earnings != nil:
		return decodeEarnings(m.Earnings)
	case m.Token != nil:
		return decodeToken(who, m.Token)
	case m.CNFT != nil:
		return decodeCNFT(who, m.CNFT)
	case m.Market != nil:
		return decodeMarket(who, m.Market)
	}
	return decodeStorage(who, m.Storage)
}

func decodeStrict(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return ErrBadMessage.Wrapf("%v", err)
	}
	if dec.More() {
		return ErrBadMessage.Wrap("trailing data")
	}
	return nil
}

func one(name string, set ...bool) error {
	n := 0
	for _, s := range set {
		if s {
			n++
		}
	}
	if n != 1 {
		return ErrBadMessage.Wrapf("%s needs exactly one variant, got %d", name, n)
	}
	return nil
}
