package keeper

import (
	"errors"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// OwnFunds prices what a deal message takes from its signer's bank balance, for x/fees' earnings
// top-up (C2 item 4). A create pulls the per-deal fee and the escrow of price x replicas x
// duration; an extend pulls the escrow of the extra epochs. A deal paid by a grantor spends the
// grantor's money, never the signer's earnings, so it is not priced. A payer nil means "not a
// message this module prices".
func (k Keeper) OwnFunds(ctx sdk.Context, msg sdk.Msg) (sdk.AccAddress, math.Int, error) {
	switch m := msg.(type) {
	case *types.MsgCreateDeal:
		if m.Granter != "" || m.PricePerEpoch.IsNil() {
			return nil, math.Int{}, nil
		}
		signer, err := sdk.AccAddressFromBech32(m.Signer)
		if err != nil {
			return nil, math.Int{}, nil
		}
		p, err := k.params(ctx)
		if err != nil {
			return nil, math.Int{}, err
		}
		escrow := m.PricePerEpoch.MulRaw(int64(m.Replicas)).MulRaw(int64(m.DurationEpochs))
		return signer, p.DealFee.Add(escrow), nil
	case *types.MsgExtendDeal:
		signer, err := sdk.AccAddressFromBech32(m.Signer)
		if err != nil {
			return nil, math.Int{}, nil
		}
		deal, err := k.Deals.Get(ctx, m.DealId)
		if errors.Is(err, collections.ErrNotFound) {
			return nil, math.Int{}, nil
		}
		if err != nil {
			return nil, math.Int{}, err
		}
		client, err := sdk.AccAddressFromBech32(deal.Client)
		if deal.Protocol || err != nil || !client.Equals(signer) {
			return nil, math.Int{}, nil
		}
		return signer, deal.PricePerEpoch.MulRaw(int64(deal.Replicas)).MulRaw(int64(m.ExtraEpochs)), nil
	}
	return nil, math.Int{}, nil
}
