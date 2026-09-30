package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"
)

func TestBaseFeeOnly_isGasTimesTheBaseFee(t *testing.T) {
	require.Equal(t, "600000", baseFeeOnly(math.NewInt(1), 600_000).String())
	require.Equal(t, "1800000", baseFeeOnly(math.NewInt(3), 600_000).String())
}

// A base fee times a large gas limit does not wrap, as shell arithmetic would.
func TestBaseFeeOnly_doesNotOverflow(t *testing.T) {
	require.Equal(t, "184467440737095516150000000000", baseFeeOnly(math.NewInt(10_000_000_000), 18_446_744_073_709_551_615).String())
}

func TestCmdTxFee_requiresAPositiveGasBeforeAnyNetworkCall(t *testing.T) {
	for _, args := range [][]string{{"tx-fee"}, {"tx-fee", "--gas", "0"}} {
		err := run(context.Background(), append(args, "--rpc", "tcp://127.0.0.1:1"), &bytes.Buffer{}, &bytes.Buffer{})
		require.ErrorContains(t, err, "--gas is required", args)
	}
	require.Error(t, run(context.Background(), []string{"tx-fee", "--gas", "-5"}, &bytes.Buffer{}, &bytes.Buffer{}))
}
