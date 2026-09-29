package ante

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
)

// DepositPayerDecorator records the transaction's first signer as the account that pays contract
// state deposits. A contract that calls another contract cannot pay: its call chain is rooted in
// a signer, and that signer is who locked the deposit and who gets it back.
type DepositPayerDecorator struct{}

// NewDepositPayerDecorator returns the decorator.
func NewDepositPayerDecorator() DepositPayerDecorator { return DepositPayerDecorator{} }

// AnteHandle sets the deposit payer on the context passed down the chain.
func (DepositPayerDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return ctx, fmt.Errorf("tx %T does not expose its signers", tx)
	}
	signers, err := sigTx.GetSigners()
	if err != nil {
		return ctx, fmt.Errorf("failed to read the tx signers: %w", err)
	}
	if len(signers) == 0 {
		return next(ctx, tx, simulate)
	}
	return next(wasmpolicy.WithDepositPayer(ctx, sdk.AccAddress(signers[0])), tx, simulate)
}
