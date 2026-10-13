package ante_test

import (
	"testing"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/shielded/ante"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
)

// A simulation runs on the mempool's check state and then runs the message, which checks and marks
// the nullifiers itself. Marking them pending in the ante handler made the message refuse its own
// nullifiers, so every shielded simulation failed ("nullifier already spent or pending ... is
// pending", stagenet smoke on orama-stagenet-4). A simulation leaves them unmarked, and a later
// real check of the same bundle is admitted.
func TestProof_aSimulationLeavesTheNullifiersForTheMessage(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	d := ante.NewProofDecorator(e.Keeper)
	tx := buildTx(t, nil, shieldMsg(1))

	reached, err := run(d, e.CheckCtx(), tx, true)
	require.NoError(t, err)
	require.True(t, reached)
	require.Zero(t, e.V1.Calls+e.V2.Calls, "a simulation's message verifies, as in a block")

	_, err = run(d, e.CheckCtx(), tx, false)
	require.NoError(t, err, "the simulation marked the nullifier pending")
}

func TestSignerless_aSimulationLeavesTheNullifiersForTheMessage(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	tx := buildTx(t, gas(oneActionGas), transferMsg(1, 20))

	reached, err := run(signerless(e), e.CheckCtx().WithGasMeter(storetypes.NewGasMeter(oneActionGas)), tx, true)
	require.NoError(t, err)
	require.True(t, reached)

	_, err = run(signerless(e), e.CheckCtx().WithGasMeter(storetypes.NewGasMeter(oneActionGas)), tx, false)
	require.NoError(t, err, "the simulation marked the nullifier pending")
}
