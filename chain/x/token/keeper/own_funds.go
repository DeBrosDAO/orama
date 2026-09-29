package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// OwnFunds prices what MsgCreateToken takes from its creator's bank balance, for x/fees' earnings
// top-up (C2 item 4): the burned creation fee plus the metadata deposit. CreateToken checks the
// creator's spendable balance against exactly this total before it moves anything. A payer nil
// means "not a message this module prices".
func (k Keeper) OwnFunds(ctx sdk.Context, msg sdk.Msg) (sdk.AccAddress, math.Int, error) {
	m, ok := msg.(*types.MsgCreateToken)
	if !ok {
		return nil, math.Int{}, nil
	}
	creator, err := sdk.AccAddressFromBech32(m.Creator)
	if err != nil {
		return nil, math.Int{}, nil
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, math.Int{}, fmt.Errorf("failed to load token params: %w", err)
	}
	if p.CreationFee.IsNil() || p.DepositPerByte.IsNil() {
		return nil, math.Int{}, nil
	}
	deposit := types.DepositFor(p.DepositPerByte, m.Subdenom, m.Name, m.Symbol, m.Description)
	return creator, p.CreationFee.Add(deposit), nil
}
