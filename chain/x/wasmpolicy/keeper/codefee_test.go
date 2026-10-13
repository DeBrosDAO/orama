package keeper_test

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

func gz(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestChargeCodeFee_sizeTimesDepositPerByteIsBurnedFromTheBalance(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ChargeCodeFee(f.ctx, alice, bytes.Repeat([]byte{7}, 1000)))
	require.True(t, f.fees.balanceBurned[alice.String()].Equal(math.NewInt(1000*perByte)))
	require.Empty(t, f.fees.deposits, "the fee is a burn, not a refundable deposit")
}

func TestChargeCodeFee_gzipIsChargedOnTheUncompressedSize(t *testing.T) {
	f := newFixture(t)
	raw := bytes.Repeat([]byte{0}, 50_000)
	zipped := gz(t, raw)
	require.Less(t, len(zipped), 1000)
	require.NoError(t, f.k.ChargeCodeFee(f.ctx, alice, zipped))
	require.True(t, f.fees.balanceBurned[alice.String()].Equal(math.NewInt(50_000*perByte)))
}

func TestChargeCodeFee_refusedWhenThePayerCannotPay(t *testing.T) {
	f := newFixture(t)
	f.fees.poor = true
	err := f.k.ChargeCodeFee(f.ctx, alice, []byte("wasm"))
	require.ErrorIs(t, err, types.ErrCodeFee)
	require.Empty(t, f.fees.balanceBurned)
}

func TestChargeCodeFee_edgeCases(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.k.ChargeCodeFee(f.ctx, alice, nil), "empty code costs nothing and is left for wasmd to reject")
	require.Empty(t, f.fees.balanceBurned)

	require.ErrorIs(t, f.k.ChargeCodeFee(f.ctx, nil, []byte("wasm")), types.ErrDepositPayer, "no payer")
	require.ErrorIs(t, f.k.ChargeCodeFee(f.ctx, alice, []byte{0x1f, 0x8b, 0x08, 0xff}), types.ErrCodeFee, "broken gzip")

	bomb := gz(t, make([]byte, 5<<20))
	require.ErrorIs(t, f.k.ChargeCodeFee(f.ctx, alice, bomb), types.ErrCodeFee, "a gzip bomb is refused, not fully read")
	require.Empty(t, f.fees.balanceBurned)
}
