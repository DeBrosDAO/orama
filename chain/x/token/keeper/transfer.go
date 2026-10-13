package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// Transfer moves amount of denom from `from` to `to` by a bank send, after
// pause, non-transferable, authority, and freeze checks. A transfer fee, when
// the capability is still held, is burned in the token's own denom. The
// permanent delegate may set `from` to another account; otherwise `from` must
// be the signer. Non-transferable blocks the delegate too.
//
// The transfer hook is a contract call that can mint or burn the token it
// created. So the move, the fee and the issued supply are never computed from
// state read before the hook: after the hook has run, every check is made again
// on the state it left, and the hook's writes, the move and the fee burn commit
// together or not at all.
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
	// The keeper sends with bank.SendCoins, which does not look at the blocked-address list that
	// x/bank's own MsgSend enforces; without this check a transfer could pay a module account,
	// whose coins no ledger of the module accounts for.
	if k.bank.BlockedAddr(to) {
		return fmt.Errorf("transfer to %s refused: it is a module account, which cannot receive a direct send", msg.To)
	}
	if from.Equals(to) {
		return fmt.Errorf("transfer from and to must differ")
	}

	token, fee, err := k.checkTransfer(ctx, msg, sender, from, to)
	if err != nil {
		return err
	}

	work, commit := ctx.CacheContext()
	writeHook, err := k.runTransferHook(work, token, from, to, msg.Amount)
	if err != nil {
		return err
	}
	if writeHook != nil {
		writeHook()
		if token, fee, err = k.checkTransfer(work, msg, sender, from, to); err != nil {
			return fmt.Errorf("transfer of %s is no longer valid after its hook ran: %w", msg.Denom, err)
		}
	}
	if err := k.settleTransfer(work, token, fee, from, to, msg.Amount); err != nil {
		return err
	}
	commit()
	return nil
}

// checkTransfer applies the pause, non-transferable, authority, freeze and
// balance checks to the state in ctx and returns the token and the fee of the move.
func (k Keeper) checkTransfer(ctx sdk.Context, msg *types.MsgTransfer, sender, from, to sdk.AccAddress) (types.Token, math.Int, error) {
	token, err := k.getToken(ctx, msg.Denom)
	if err != nil {
		return types.Token{}, math.Int{}, err
	}
	if token.Paused {
		return token, math.Int{}, fmt.Errorf("token %s is paused", token.Denom)
	}
	if token.Extensions.NonTransferable {
		return token, math.Int{}, fmt.Errorf("token %s is non-transferable", token.Denom)
	}
	if !from.Equals(sender) {
		if token.Extensions.PermanentDelegate == "" || token.Extensions.PermanentDelegate != sender.String() {
			return token, math.Int{}, fmt.Errorf("%s cannot transfer %s from %s", sender, token.Denom, from)
		}
	}
	fromFrozen, err := k.isFrozen(ctx, token.Denom, from.String())
	if err != nil {
		return token, math.Int{}, err
	}
	toFrozen, err := k.isFrozen(ctx, token.Denom, to.String())
	if err != nil {
		return token, math.Int{}, err
	}
	if fromFrozen || toFrozen {
		return token, math.Int{}, fmt.Errorf("token %s transfer blocked: from frozen=%t to frozen=%t", token.Denom, fromFrozen, toFrozen)
	}

	fee, err := types.TransferFeeAmount(msg.Amount, token.Extensions.TransferFeeBps)
	if err != nil {
		return token, math.Int{}, err
	}
	balance := k.bank.GetBalance(ctx, from, token.Denom).Amount
	if balance.IsNil() || balance.LT(msg.Amount) {
		return token, math.Int{}, fmt.Errorf("insufficient %s balance: have %s, need %s", token.Denom, spendableText(balance), msg.Amount)
	}
	if fee.IsPositive() && (token.Issued.IsNil() || token.Issued.LT(fee)) {
		return token, math.Int{}, fmt.Errorf("transfer fee %s%s exceeds issued supply %s", fee, token.Denom, spendableText(token.Issued))
	}
	return token, fee, nil
}

// settleTransfer sends the net amount, then burns the fee and lowers the
// token's issued supply by it. token must have been read from ctx after every
// outside call.
func (k Keeper) settleTransfer(ctx sdk.Context, token types.Token, fee math.Int, from, to sdk.AccAddress, amount math.Int) error {
	net := amount.Sub(fee)
	if net.IsPositive() {
		if err := k.bank.SendCoins(scopedTransfer(ctx), from, to, coins(token.Denom, net)); err != nil {
			return fmt.Errorf("failed to send %s: %w", token.Denom, err)
		}
	}
	if !fee.IsPositive() {
		return nil
	}
	feeCoins := coins(token.Denom, fee)
	if err := k.bank.SendCoinsFromAccountToModule(ctx, from, types.ModuleName, feeCoins); err != nil {
		return fmt.Errorf("failed to take transfer fee for %s: %w", token.Denom, err)
	}
	if err := k.bank.BurnCoins(ctx, types.ModuleName, feeCoins); err != nil {
		return fmt.Errorf("failed to burn transfer fee for %s: %w", token.Denom, err)
	}
	token.Issued = token.Issued.Sub(fee)
	return k.storeToken(ctx, token)
}
