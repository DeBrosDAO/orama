package keeper

import (
	"fmt"
	"strings"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// Invariants is the result of CheckInvariants.
type Invariants struct {
	SupplyMatches bool
	DepositsMatch bool
	Detail        string
}

// CheckInvariants checks, for every token:
//
//   - issued supply equals the bank supply of that denom
//   - the recorded metadata deposit equals the deposit FeesKeeper.GetDeposit
//     returns for that id, owned by the creator
//
// The walk is O(tokens), the same shape as x/fees' deposit walk. Transfers
// themselves are one bank send.
func (k Keeper) CheckInvariants(ctx sdk.Context) (Invariants, error) {
	supplyOK := true
	depositsOK := true
	var detail strings.Builder

	err := k.Tokens.Walk(ctx, nil, func(denom string, token types.Token) (bool, error) {
		bankSupply := k.bank.GetSupply(ctx, denom).Amount
		if bankSupply.IsNil() {
			bankSupply = math.ZeroInt()
		}
		issued := token.Issued
		if issued.IsNil() {
			issued = math.ZeroInt()
		}
		match := issued.Equal(bankSupply)
		if !match {
			supplyOK = false
		}
		fmt.Fprintf(&detail, "supply %s: issued=%s bank=%s match=%t\n", denom, issued, bankSupply, match)

		deposit, err := k.fees.GetDeposit(ctx, types.DepositID(denom))
		depositMatch := err == nil && !deposit.Amount.IsNil() && deposit.Amount.Equal(token.DepositAmount) && deposit.Owner == token.Creator
		if !depositMatch {
			depositsOK = false
		}
		if err != nil {
			fmt.Fprintf(&detail, "deposit %s: interface error: %v; recorded=%s\n", denom, err, token.DepositAmount)
			return false, nil
		}
		fmt.Fprintf(&detail, "deposit %s: recorded=%s interface=%s owner=%s match=%t\n",
			denom, token.DepositAmount, deposit.Amount, deposit.Owner, depositMatch)
		return false, nil
	})
	if err != nil {
		return Invariants{}, fmt.Errorf("failed to walk tokens: %w", err)
	}
	if detail.Len() == 0 {
		detail.WriteString("no tokens\n")
	}
	return Invariants{
		SupplyMatches: supplyOK,
		DepositsMatch: depositsOK,
		Detail:        detail.String(),
	}, nil
}
