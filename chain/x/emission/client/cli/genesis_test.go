package cli

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

func faucetFlagsCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := SetEmissionParamsCmd("")
	require.NoError(t, cmd.ParseFlags(args))
	return cmd
}

func TestApplyFaucetFlags_onlyPassedFlagsChangeParams(t *testing.T) {
	p := types.DefaultParams()
	p.FaucetMaxDrip = math.NewInt(7)

	cmd := faucetFlagsCmd(t, "--faucet-enabled", "--faucet-epoch-cap=900", "--faucet-cooldown=0")
	require.NoError(t, applyFaucetFlags(cmd, &p))

	require.True(t, p.FaucetEnabled)
	require.True(t, p.FaucetMaxDrip.Equal(math.NewInt(7)), "an unpassed flag keeps the genesis value")
	require.True(t, p.FaucetEpochCap.Equal(math.NewInt(900)))
	require.Zero(t, p.FaucetRecipientCooldownSeconds)
}

func TestApplyFaucetFlags_noFlagsLeavesParamsUnchanged(t *testing.T) {
	p := types.DefaultParams()
	want := p
	require.NoError(t, applyFaucetFlags(faucetFlagsCmd(t), &p))
	require.Equal(t, want, p)
}

func TestApplyFaucetFlags_rejectsNonIntegerAmount(t *testing.T) {
	p := types.DefaultParams()
	err := applyFaucetFlags(faucetFlagsCmd(t, "--faucet-max-drip=lots"), &p)
	require.ErrorContains(t, err, "faucet-max-drip")
}
