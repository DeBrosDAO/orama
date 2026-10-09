package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// Transfer moves amount of denom from `from` to `to` by a bank send, after
// pause, non-transferable, authority, and freeze checks. A transfer fee, when
// the capability is still held, is burned in the token's own denom. The
// permanent delegate may set `from` to another account; otherwise `from` must
// be the signer. Non-transferable blocks the delegate too.
func (k Keeper) Transfer(ctx sdk.Context, msg *types.MsgTransfer) error {
	if msg == nil {
		return fmt.Errorf("nil transfer message")
	}
	sender, err := parseAcc(msg.Sender, "sender")
	if err != nil {
		return err
	}
	from, err := parseAcc(msg.From, "from")
	if err != nil {
		return err
	}
	to, err := parseAcc(msg.To, "to")
	if err != nil {
		return err
	}
	if err := requirePositive(msg.Amount, "transfer amount"); err != nil {
		return err
	}
	if from.Equals(to) {
		return fmt.Errorf("transfer from and to must differ")
	}

	token, err := k.getToken(ctx, msg.Denom)
	if err != nil {
		return err
	}
	if token.Paused {
		return fmt.Errorf("token %s is paused", token.Denom)
	}
	if token.Extensions.NonTransferable {
		return fmt.Errorf("token %s is non-transferable", token.Denom)
	}
	if !from.Equals(sender) {
		if token.Extensions.PermanentDelegate == "" || token.Extensions.PermanentDelegate != sender.String() {
			return fmt.Errorf("%s cannot transfer %s from %s", sender, token.Denom, from)
		}
	}
	fromFrozen, err := k.isFrozen(ctx, token.Denom, from.String())
	if err != nil {
		return err
	}
	toFrozen, err := k.isFrozen(ctx, token.Denom, to.String())
	if err != nil {
		return err
	}
	if fromFrozen || toFrozen {
		return fmt.Errorf("token %s transfer blocked: from frozen=%t to frozen=%t", token.Denom, fromFrozen, toFrozen)
	}

	fee, err := types.TransferFeeAmount(msg.Amount, token.Extensions.TransferFeeBps)
	if err != nil {
		return err
	}
	balance := k.bank.GetBalance(ctx, from, token.Denom).Amount
	if balance.IsNil() || balance.LT(msg.Amount) {
		return fmt.Errorf("insufficient %s balance: have %s, need %s", token.Denom, spendableText(balance), msg.Amount)
	}

	writeHook, err := k.runTransferHook(ctx, token, from, to, msg.Amount)
	if err != nil {
		return err
	}

	net := msg.Amount.Sub(fee)
	if fee.IsPositive() && (token.Issued.IsNil() || token.Issued.LT(fee)) {
		return fmt.Errorf("transfer fee %s%s exceeds issued supply %s", fee, token.Denom, spendableText(token.Issued))
	}
	if net.IsPositive() {
		if err := k.bank.SendCoins(scopedTransfer(ctx), from, to, coins(token.Denom, net)); err != nil {
			return fmt.Errorf("failed to send %s: %w", token.Denom, err)
		}
	}
	if fee.IsPositive() {
		feeCoins := coins(token.Denom, fee)
		if err := k.bank.SendCoinsFromAccountToModule(ctx, from, types.ModuleName, feeCoins); err != nil {
			return fmt.Errorf("failed to take transfer fee for %s: %w", token.Denom, err)
		}
		if err := k.bank.BurnCoins(ctx, types.ModuleName, feeCoins); err != nil {
			return fmt.Errorf("failed to burn transfer fee for %s: %w", token.Denom, err)
		}
		token.Issued = token.Issued.Sub(fee)
		if err := k.storeToken(ctx, token); err != nil {
			return err
		}
	}
	if writeHook != nil {
		writeHook()
	}
	return nil
}
