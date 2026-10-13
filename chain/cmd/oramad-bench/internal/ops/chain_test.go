package ops

import (
	"testing"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/client/node"
)

func TestPriceFor(t *testing.T) {
	cases := []struct {
		base     int64
		multiple int64
		want     int64
	}{
		{1, 32, 32}, {7, 4, 28}, {0, 10, 1}, {5, 1, 5},
	}
	for _, c := range cases {
		if got := PriceFor(math.NewInt(c.base), c.multiple); !got.Equal(math.NewInt(c.want)) {
			t.Errorf("PriceFor(%d, %d) = %s, want %d", c.base, c.multiple, got, c.want)
		}
	}
}

func TestFeeFor(t *testing.T) {
	fee := FeeFor(150_000, math.NewInt(32))
	if got := fee.AmountOf(params.BaseDenom); !got.Equal(math.NewInt(4_800_000)) {
		t.Fatalf("fee %s", fee)
	}
	if got := FeeFor(0, math.NewInt(32)).AmountOf(params.BaseDenom); !got.Equal(math.OneInt()) {
		t.Fatalf("a zero-gas fee must still be a valid one-norama coin, got %s", got)
	}
}

func TestGasLimit_scalesBySimulatedGas(t *testing.T) {
	c := &Chain{GasAdjustPercent: 150}
	if got := c.GasLimit(100_000); got != 150_000 {
		t.Fatalf("got %d", got)
	}
	if got := c.GasLimit(0); got != 0 {
		t.Fatalf("got %d", got)
	}
}

func TestNew_validatesItsArguments(t *testing.T) {
	client, err := node.Dial("tcp://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(nil, "c", 1, 150); err == nil {
		t.Error("nil client accepted")
	}
	if _, err := New(client, "", 1, 150); err == nil {
		t.Error("empty chain id accepted")
	}
	if _, err := New(client, "c", 0, 150); err == nil {
		t.Error("fee multiple 0 accepted")
	}
	if _, err := New(client, "c", 1, 99); err == nil {
		t.Error("a gas adjustment below 100% accepted")
	}
	if _, err := New(client, "c", DefaultFeeMultiple, DefaultGasAdjustPercent); err != nil {
		t.Errorf("defaults refused: %v", err)
	}
}
