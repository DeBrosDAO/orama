package policy

import (
	"testing"

	"cosmossdk.io/math"
)

func TestBundlePaysFee(t *testing.T) {
	if err := BundlePaysFee(math.NewInt(10), math.NewInt(7), math.NewInt(3)); err != nil {
		t.Fatal(err)
	}
	if err := BundlePaysFee(math.NewInt(10), math.NewInt(8), math.NewInt(3)); err != ErrFeeUnpaid {
		t.Fatalf("got %v", err)
	}
	if err := BundlePaysFee(math.NewInt(10), math.NewInt(-1), math.NewInt(11)); err != ErrAmount {
		t.Fatalf("got %v", err)
	}
}
