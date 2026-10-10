package ante_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txsigning "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/ante"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

type signedTx struct {
	txOf
	signers [][]byte
}

var _ authsigning.SigVerifiableTx = signedTx{}

func (s signedTx) GetSigners() ([][]byte, error)                     { return s.signers, nil }
func (s signedTx) GetPubKeys() ([]cryptotypes.PubKey, error)         { return nil, nil }
func (s signedTx) GetSignaturesV2() ([]txsigning.SignatureV2, error) { return nil, nil }

func payerCtx(t *testing.T) sdk.Context {
	t.Helper()
	key := storetypes.NewKVStoreKey("payer")
	return testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("t_payer")).Ctx
}

func TestDepositPayerDecorator_setsTheFirstSigner(t *testing.T) {
	first := sdk.AccAddress("first_signer_________")
	var seen sdk.AccAddress
	var budget *types.DepositBudget
	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
		seen = wasmpolicy.DepositPayer(ctx)
		budget = types.DepositBudgetFrom(ctx)
		return ctx, nil
	}
	tx := signedTx{signers: [][]byte{first, []byte("second_signer________")}}
	_, err := ante.NewDepositPayerDecorator().AnteHandle(payerCtx(t), tx, false, next)
	require.NoError(t, err)
	require.Equal(t, first, seen)
	require.NotNil(t, budget, "every transaction gets its own deposit budget")
	require.True(t, budget.Locked.IsZero())
}

func TestDepositPayerDecorator_noSignersLeavesNoPayer(t *testing.T) {
	var seen sdk.AccAddress
	next := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
		seen = wasmpolicy.DepositPayer(ctx)
		return ctx, nil
	}
	_, err := ante.NewDepositPayerDecorator().AnteHandle(payerCtx(t), signedTx{}, false, next)
	require.NoError(t, err)
	require.Empty(t, seen)
}

func TestDepositPayerDecorator_refusesATxThatHidesItsSigners(t *testing.T) {
	_, err := ante.NewDepositPayerDecorator().AnteHandle(payerCtx(t), txOf{}, false, nil)
	require.Error(t, err)
}
