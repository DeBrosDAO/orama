package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// Mint issues amount of the token to recipient. The signer must be the creator
// and the mint capability must still be held. A frozen recipient is refused.
// Pause and non-transferable do not block minting.
func (k Keeper) Mint(ctx sdk.Context, msg *types.MsgMint) error {
	if msg == nil {
		return fmt.Errorf("nil mint message")
	}
	sender, err := parseAcc(msg.Sender, "sender")
	if err != nil {
		return err
	}
	recipient, err := parseAcc(msg.Recipient, "recipient")
	if err != nil {
		return err
	}
	if err := requirePositive(msg.Amount, "mint amount"); err != nil {
		return err
	}
	token, err := k.getToken(ctx, msg.Denom)
	if err != nil {
		return err
	}
	if !token.Extensions.Mint || token.Creator != sender.String() {
		return fmt.Errorf("mint authority for %s is not held by %s", token.Denom, sender)
	}
	frozen, err := k.isFrozen(ctx, token.Denom, recipient.String())
	if err != nil {
		return err
	}
	if frozen {
		return fmt.Errorf("recipient %s is frozen for %s", recipient, token.Denom)
	}

	minted := coins(token.Denom, msg.Amount)
	if err := k.bank.MintCoins(ctx, types.ModuleName, minted); err != nil {
		return fmt.Errorf("failed to mint %s: %w", token.Denom, err)
	}
	if err := k.bank.SendCoinsFromModuleToAccount(ctx, types.ModuleName, recipient, minted); err != nil {
		return fmt.Errorf("failed to send minted %s to %s: %w", token.Denom, recipient, err)
	}
	token.Issued = token.Issued.Add(msg.Amount)
	return k.storeToken(ctx, token)
}

// Burn destroys amount of the token from the signer's own balance. Any holder
// may burn; mint authority is not required. A frozen holder may not burn.
func (k Keeper) Burn(ctx sdk.Context, msg *types.MsgBurn) error {
	if msg == nil {
		return fmt.Errorf("nil burn message")
	}
	sender, err := parseAcc(msg.Sender, "sender")
	if err != nil {
		return err
	}
	if err := requirePositive(msg.Amount, "burn amount"); err != nil {
		return err
	}
	token, err := k.getToken(ctx, msg.Denom)
	if err != nil {
		return err
	}
	frozen, err := k.isFrozen(ctx, token.Denom, sender.String())
	if err != nil {
		return err
	}
	if frozen {
		return fmt.Errorf("account %s is frozen for %s", sender, token.Denom)
	}
	if token.Issued.IsNil() || token.Issued.LT(msg.Amount) {
		return fmt.Errorf("burn of %s%s exceeds issued supply %s", msg.Amount, token.Denom, token.Issued)
	}
	balance := k.bank.GetBalance(ctx, sender, token.Denom).Amount
	if balance.IsNil() || balance.LT(msg.Amount) {
		return fmt.Errorf("insufficient %s balance: have %s, need %s", token.Denom, spendableText(balance), msg.Amount)
	}

	burned := coins(token.Denom, msg.Amount)
	if err := k.bank.SendCoinsFromAccountToModule(ctx, sender, types.ModuleName, burned); err != nil {
		return fmt.Errorf("failed to pull %s for burn: %w", token.Denom, err)
	}
	if err := k.bank.BurnCoins(ctx, types.ModuleName, burned); err != nil {
		return fmt.Errorf("failed to burn %s: %w", token.Denom, err)
	}
	token.Issued = token.Issued.Sub(msg.Amount)
	return k.storeToken(ctx, token)
}
