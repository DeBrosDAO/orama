package pool

import (
	"testing"

	"cosmossdk.io/math"
)

func TestServeCapsTheWhale(t *testing.T) {
	got := Serve(math.NewInt(100), math.NewInt(40), []Request{
		{Address: "small", Amount: math.NewInt(10)},
		{Address: "whale", Amount: math.NewInt(90)},
	})
	if !got[0].Amount.Equal(math.NewInt(10)) {
		t.Fatalf("small %s", got[0].Amount)
	}
	if !got[1].Amount.Equal(math.NewInt(40)) {
		t.Fatalf("whale %s", got[1].Amount)
	}
}

func TestServeProRata(t *testing.T) {
	got := Serve(math.NewInt(100), math.NewInt(100), []Request{
		{Address: "a", Amount: math.NewInt(25)},
		{Address: "b", Amount: math.NewInt(75)},
	})
	if !got[0].Amount.Equal(math.NewInt(25)) || !got[1].Amount.Equal(math.NewInt(75)) {
		t.Fatalf("%s %s", got[0].Amount, got[1].Amount)
	}
}

func TestServeEmptyCapacity(t *testing.T) {
	got := Serve(math.ZeroInt(), math.NewInt(10), []Request{{Address: "a", Amount: math.NewInt(5)}})
	if !got[0].Amount.IsZero() {
		t.Fatal(got[0].Amount)
	}
}
