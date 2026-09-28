package wasmpolicy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
)

func TestMeterChargesBytesAndRefundsOnDelete(t *testing.T) {
	perByte := math.NewInt(3)
	meter, err := wasmpolicy.NewMeter(perByte)
	require.NoError(t, err)

	got, err := meter.Charge("contract-store", 10)
	require.NoError(t, err)
	require.True(t, got.Equal(math.NewInt(30)))

	_, err = meter.Charge("contract-store", 1)
	require.Error(t, err)

	refund, err := meter.Refund("contract-store")
	require.NoError(t, err)
	require.True(t, refund.Equal(math.NewInt(30)))

	_, err = meter.Refund("contract-store")
	require.Error(t, err)
}

func TestStateDepositWrapperCallsTheMeter(t *testing.T) {
	wrapper, err := wasmpolicy.NewStateDeposit(math.NewInt(4))
	require.NoError(t, err)

	charged, err := wrapper.Charge("key", 5)
	require.NoError(t, err)
	require.True(t, charged.Equal(math.NewInt(20)))

	refunded, err := wrapper.Refund("key")
	require.NoError(t, err)
	require.True(t, refunded.Equal(charged))
}

func TestMeterRejectsNegativePrice(t *testing.T) {
	_, err := wasmpolicy.NewMeter(math.NewInt(-1))
	require.Error(t, err)
}
