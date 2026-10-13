package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

func tierReadGas(t *testing.T, c *wiringChain) uint64 {
	t.Helper()
	ctx := c.app.NewContext(true).WithGasMeter(storetypes.NewGasMeter(1 << 60))
	_, err := c.app.HousesKeeper.Tiers(ctx)
	require.NoError(t, err)
	return ctx.GasMeter().GasConsumed()
}

// MsgRegisterOperator costs only the transaction fee and an operator can never be removed,
// while every x/houses message that reads the operator house (SubmitProposal, VoteOperator) walks
// every registered operator, with a service-day prefix scan each. Registering operators therefore
// raises the gas of governance without bound.
func TestGovernanceGasDoesNotGrowWithRegisteredOperators(t *testing.T) {
	c := newWiringChain(t)
	base := tierReadGas(t, c)
	const spam = 1000
	c.write(func(ctx sdk.Context) {
		srv := nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper)
		for i := 0; i < spam; i++ {
			addr := sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address())
			_, err := srv.RegisterOperator(ctx, &nodestypes.MsgRegisterOperator{Operator: addr.String()})
			require.NoError(t, err)
		}
	})
	grown := tierReadGas(t, c)
	t.Logf("gas to read the tiers: %d with no operators, %d with %d registered operators (%d gas per operator)", base, grown, spam, (grown-base)/spam)
	require.LessOrEqual(t, grown, base*2, "the cost of a governance message must not be set by how many accounts registered as operators")
}
