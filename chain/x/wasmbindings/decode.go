package wasmbindings

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
)

func decodeEarnings(m *EarningsMsg) (Action, error) {
	if err := one("earnings", m.Pay != nil); err != nil {
		return Action{}, err
	}
	return Action{Pay: m.Pay}, nil
}

func decodeToken(who string, m *TokenMsg) (Action, error) {
	if err := one("token", m.Create != nil, m.Mint != nil, m.Burn != nil); err != nil {
		return Action{}, err
	}
	switch {
	case m.Create != nil:
		c := m.Create
		if err := wasmpolicy.RefuseNoramaWrapper(tokentypes.Denom(who, c.Subdenom), nil); err != nil {
			return Action{}, err
		}
		return Action{Msgs: []sdk.Msg{&tokentypes.MsgCreateToken{
			Creator: who, Subdenom: c.Subdenom, Name: c.Name, Symbol: c.Symbol, Description: c.Description,
			Mint: c.Mint, Freeze: c.Freeze, PermanentDelegate: c.PermanentDelegate, TransferFeeBps: c.TransferFeeBps,
			NonTransferable: c.NonTransferable, Pause: c.Pause, TransferHook: c.TransferHook,
		}}}, nil
	case m.Mint != nil:
		if err := refuseNoramaDenom(m.Mint.Denom); err != nil {
			return Action{}, err
		}
		return Action{Msgs: []sdk.Msg{&tokentypes.MsgMint{
			Sender: who, Denom: m.Mint.Denom, Recipient: m.Mint.Recipient, Amount: m.Mint.Amount,
		}}}, nil
	}
	if err := refuseNoramaDenom(m.Burn.Denom); err != nil {
		return Action{}, err
	}
	return Action{Msgs: []sdk.Msg{&tokentypes.MsgBurn{Sender: who, Denom: m.Burn.Denom, Amount: m.Burn.Amount}}}, nil
}

// refuseNoramaDenom refuses a token operation on norama itself or on a factory token named norama.
func refuseNoramaDenom(denom string) error {
	return wasmpolicy.RefuseNoramaWrapper(denom, []string{denom})
}

func decodeCNFT(who string, m *CNFTMsg) (Action, error) {
	if err := one("cnft", m.CreateCollection != nil, m.CreateTree != nil, m.Mint != nil); err != nil {
		return Action{}, err
	}
	switch {
	case m.CreateCollection != nil:
		return Action{Msgs: []sdk.Msg{&cnfttypes.MsgCreateCollection{
			Creator: who, Name: m.CreateCollection.Name, RoyaltyBps: m.CreateCollection.RoyaltyBps,
		}}}, nil
	case m.CreateTree != nil:
		t := m.CreateTree
		return Action{Msgs: []sdk.Msg{&cnfttypes.MsgCreateTree{
			Creator: who, CollectionId: t.CollectionID, Depth: t.Depth, Buffer: t.Buffer, Canopy: t.Canopy,
		}}}, nil
	}
	return Action{Msgs: []sdk.Msg{&cnfttypes.MsgMint{
		Creator: who, TreeId: m.Mint.TreeID, Root: m.Mint.Root, Leaves: m.Mint.Leaves,
	}}}, nil
}

func decodeMarket(who string, m *MarketMsg) (Action, error) {
	if err := one("market", m.List != nil, m.CancelListing != nil, m.Bid != nil, m.CancelBid != nil, m.Settle != nil); err != nil {
		return Action{}, err
	}
	switch {
	case m.List != nil:
		return Action{Msgs: []sdk.Msg{&markettypes.MsgList{
			Seller: who, TreeId: m.List.TreeID, Leaf: m.List.Leaf, Proof: m.List.Proof, Price: m.List.Price,
		}}}, nil
	case m.CancelListing != nil:
		return Action{Msgs: []sdk.Msg{&markettypes.MsgCancelListing{Seller: who, ListingId: m.CancelListing.ListingID}}}, nil
	case m.Bid != nil:
		return Action{Msgs: []sdk.Msg{&markettypes.MsgBid{Bidder: who, ListingId: m.Bid.ListingID, Amount: m.Bid.Amount}}}, nil
	case m.CancelBid != nil:
		return Action{Msgs: []sdk.Msg{&markettypes.MsgCancelBid{Bidder: who, ListingId: m.CancelBid.ListingID, BidId: m.CancelBid.BidID}}}, nil
	}
	return Action{Msgs: []sdk.Msg{&markettypes.MsgSettle{
		Signer: who, ListingId: m.Settle.ListingID, BidId: m.Settle.BidID, Leaf: m.Settle.Leaf, Proof: m.Settle.Proof,
	}}}, nil
}

func decodeStorage(who string, m *StorageMsg) (Action, error) {
	if err := one("storage", m.CreateDeal != nil); err != nil {
		return Action{}, err
	}
	d := m.CreateDeal
	return Action{Msgs: []sdk.Msg{&storagetypes.MsgCreateDeal{
		Signer: who, Class: d.Class, DealNonce: d.DealNonce, RepairDelegate: d.RepairDelegate, Replicas: d.Replicas,
		PricePerEpoch: d.PricePerEpoch, DurationEpochs: d.DurationEpochs, Pieces: d.Pieces,
	}}}, nil
}
