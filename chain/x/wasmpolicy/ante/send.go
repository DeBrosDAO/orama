package ante

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// IsContract reports whether addr is a wasm contract account.
type IsContract func(ctx context.Context, addr sdk.AccAddress) bool

// ContractSendDecorator stops a contract from bank-sending norama to a user account.
// A contract may send norama to an allowlisted module account, or to another contract.
// The chain allowlists every module account: module-to-account keeper sends are how the token,
// market and storage bindings take a contract's fee, bid or deal escrow. The same rule is bank's send restriction, so contract submessages
// hit it even when they never appear in the outer tx.
type ContractSendDecorator struct {
	isContract IsContract
	allow      []sdk.AccAddress
}

// NewContractSendDecorator builds the decorator. allowModules are module account names.
func NewContractSendDecorator(isContract IsContract, allowModules []string) ContractSendDecorator {
	if isContract == nil {
		isContract = func(context.Context, sdk.AccAddress) bool { return false }
	}
	allow := make([]sdk.AccAddress, 0, len(allowModules))
	for _, name := range allowModules {
		allow = append(allow, authtypes.NewModuleAddress(name))
	}
	return ContractSendDecorator{isContract: isContract, allow: allow}
}

// Restrict is the bank send restriction. On success the recipient is unchanged.
func (d ContractSendDecorator) Restrict(ctx context.Context, from, to sdk.AccAddress, amt sdk.Coins) (sdk.AccAddress, error) {
	if err := d.decide(ctx, from, to, amt); err != nil {
		return nil, err
	}
	return to, nil
}

// AnteHandle applies the norama rule to MsgSend and MsgMultiSend.
func (d ContractSendDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	for _, msg := range tx.GetMsgs() {
		if err := d.checkMsg(ctx, msg); err != nil {
			return ctx, err
		}
	}
	return next(ctx, tx, simulate)
}

func (d ContractSendDecorator) checkMsg(ctx context.Context, msg sdk.Msg) error {
	switch m := msg.(type) {
	case *banktypes.MsgSend:
		from, err := sdk.AccAddressFromBech32(m.FromAddress)
		if err != nil {
			return err
		}
		to, err := sdk.AccAddressFromBech32(m.ToAddress)
		if err != nil {
			return err
		}
		return d.decide(ctx, from, to, m.Amount)
	case *banktypes.MsgMultiSend:
		var contracts []sdk.AccAddress
		for _, in := range m.Inputs {
			from, err := sdk.AccAddressFromBech32(in.Address)
			if err != nil {
				return err
			}
			if d.isContract(ctx, from) {
				contracts = append(contracts, from)
			}
		}
		if len(contracts) == 0 {
			return nil
		}
		for _, out := range m.Outputs {
			to, err := sdk.AccAddressFromBech32(out.Address)
			if err != nil {
				return err
			}
			for _, from := range contracts {
				if err := d.decide(ctx, from, to, out.Coins); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (d ContractSendDecorator) decide(ctx context.Context, from, to sdk.AccAddress, amt sdk.Coins) error {
	if !d.isContract(ctx, from) || !hasNorama(amt) {
		return nil
	}
	if d.isContract(ctx, to) || d.allows(to) {
		return nil
	}
	return types.ErrContractNorama
}

func (d ContractSendDecorator) allows(addr sdk.AccAddress) bool {
	for _, allowed := range d.allow {
		if allowed.Equals(addr) {
			return true
		}
	}
	return false
}

func hasNorama(amt sdk.Coins) bool {
	return amt.AmountOf(params.BaseDenom).IsPositive()
}
