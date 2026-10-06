package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/token/types"
)

// CreateToken burns the creation fee, locks the metadata deposit, and stores
// the token. Capabilities in the message cannot be added later.
func (k Keeper) CreateToken(ctx sdk.Context, msg *types.MsgCreateToken) (types.Token, error) {
	if msg == nil {
		return types.Token{}, fmt.Errorf("nil create token message")
	}
	creator, err := parseAcc(msg.Creator, "creator")
	if err != nil {
		return types.Token{}, err
	}
	if err := types.ValidateSubdenom(msg.Subdenom); err != nil {
		return types.Token{}, err
	}
	if err := types.ValidateMetadata(msg.Name, msg.Symbol, msg.Description); err != nil {
		return types.Token{}, err
	}
	ext := types.Extensions{
		Mint:              msg.Mint,
		Freeze:            msg.Freeze,
		PermanentDelegate: msg.PermanentDelegate,
		TransferFeeBps:    msg.TransferFeeBps,
		NonTransferable:   msg.NonTransferable,
		Pause:             msg.Pause,
		TransferHook:      msg.TransferHook,
	}
	if ext.PermanentDelegate != "" {
		delegate, err := parseAcc(ext.PermanentDelegate, "permanent delegate")
		if err != nil {
			return types.Token{}, err
		}
		ext.PermanentDelegate = delegate.String()
	}
	if err := types.ValidateExtensions(ext); err != nil {
		return types.Token{}, err
	}

	denom := types.Denom(creator.String(), msg.Subdenom)
	if err := sdk.ValidateDenom(denom); err != nil {
		return types.Token{}, fmt.Errorf("invalid denom %s: %w", denom, err)
	}
	exists, err := k.Tokens.Has(ctx, denom)
	if err != nil {
		return types.Token{}, fmt.Errorf("failed to check token %s: %w", denom, err)
	}
	if exists {
		return types.Token{}, fmt.Errorf("token %s already exists", denom)
	}

	p, err := k.Params.Get(ctx)
	if err != nil {
		return types.Token{}, fmt.Errorf("failed to load token params: %w", err)
	}
	if p.CreationFee.IsNil() || p.DepositPerByte.IsNil() {
		return types.Token{}, fmt.Errorf("token params are not initialized")
	}
	deposit := types.DepositFor(p.DepositPerByte, msg.Subdenom, msg.Name, msg.Symbol, msg.Description)
	if !deposit.IsPositive() {
		return types.Token{}, fmt.Errorf("metadata deposit for %s must be positive", denom)
	}
	need := p.CreationFee.Add(deposit)
	if err := k.fees.FundSpendFromEarnings(ctx, creator, params.BaseDenom, need); err != nil {
		return types.Token{}, err
	}
	spendable := k.bank.SpendableCoin(ctx, creator, params.BaseDenom).Amount
	if spendable.IsNil() || spendable.LT(need) {
		return types.Token{}, fmt.Errorf(
			"insufficient %s to create %s: need %s, have %s spendable",
			params.BaseDenom, denom, need, spendableText(spendable),
		)
	}

	fee := coins(params.BaseDenom, p.CreationFee)
	if err := k.bank.SendCoinsFromAccountToModule(ctx, creator, types.ModuleName, fee); err != nil {
		return types.Token{}, fmt.Errorf("failed to take creation fee for %s: %w", denom, err)
	}
	if err := k.bank.BurnCoins(ctx, types.ModuleName, fee); err != nil {
		return types.Token{}, fmt.Errorf("failed to burn creation fee for %s: %w", denom, err)
	}
	if err := k.fees.LockDeposit(ctx, creator, types.DepositID(denom), deposit); err != nil {
		return types.Token{}, fmt.Errorf("failed to lock metadata deposit for %s: %w", denom, err)
	}

	token := types.Token{
		Denom:         denom,
		Creator:       creator.String(),
		Name:          msg.Name,
		Symbol:        msg.Symbol,
		Description:   msg.Description,
		Extensions:    ext,
		Issued:        math.ZeroInt(),
		DepositAmount: deposit,
	}
	if err := k.storeToken(ctx, token); err != nil {
		return types.Token{}, err
	}
	k.Logger(ctx).Info("token created", "denom", denom, "creator", creator.String())
	return token, nil
}

// DeleteToken removes a zero-supply token and releases its metadata deposit.
// The fees keeper refunds 99% to the creator's earnings and burns 1%.
func (k Keeper) DeleteToken(ctx sdk.Context, msg *types.MsgDeleteToken) (refund, burn math.Int, err error) {
	if msg == nil {
		return math.Int{}, math.Int{}, fmt.Errorf("nil delete token message")
	}
	sender, err := parseAcc(msg.Sender, "sender")
	if err != nil {
		return math.Int{}, math.Int{}, err
	}
	token, err := k.getToken(ctx, msg.Denom)
	if err != nil {
		return math.Int{}, math.Int{}, err
	}
	if token.Creator != sender.String() {
		return math.Int{}, math.Int{}, fmt.Errorf("%s cannot delete %s", sender, token.Denom)
	}
	if token.Issued.IsNil() || !token.Issued.IsZero() {
		return math.Int{}, math.Int{}, fmt.Errorf("token %s still has issued supply %s", token.Denom, token.Issued)
	}
	supply := k.bank.GetSupply(ctx, token.Denom).Amount
	if supply.IsNil() || !supply.IsZero() {
		return math.Int{}, math.Int{}, fmt.Errorf("token %s bank supply is %s, not zero", token.Denom, spendableText(supply))
	}
	if err := k.Frozen.Clear(ctx, collections.NewPrefixedPairRange[string, string](token.Denom)); err != nil {
		return math.Int{}, math.Int{}, fmt.Errorf("failed to clear freezes for %s: %w", token.Denom, err)
	}
	refund, burn, err = k.fees.ReleaseDeposit(ctx, types.DepositID(token.Denom))
	if err != nil {
		return math.Int{}, math.Int{}, fmt.Errorf("failed to release metadata deposit for %s: %w", token.Denom, err)
	}
	if err := k.Tokens.Remove(ctx, token.Denom); err != nil {
		return math.Int{}, math.Int{}, fmt.Errorf("failed to delete token %s: %w", token.Denom, err)
	}
	k.Logger(ctx).Info("token deleted", "denom", token.Denom, "refund", refund.String(), "burned", burn.String())
	return refund, burn, nil
}

func spendableText(v math.Int) string {
	if v.IsNil() {
		return "0"
	}
	return v.String()
}
