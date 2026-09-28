package pool

import (
	"testing"
	"time"

	"cosmossdk.io/math"
)

func TestTurnstileUnderflowAndMigration(t *testing.T) {
	p := NewPools()
	from := Key{Vintage: 1, Asset: NativeAsset}
	to := Key{Vintage: 2, Asset: NativeAsset}
	if err := p.Add(from, math.NewInt(100)); err != nil {
		t.Fatal(err)
	}
	if err := p.Sub(from, math.NewInt(101)); err != ErrUnderflow {
		t.Fatalf("underflow: %v", err)
	}
	if err := p.Move(from, to, math.NewInt(40)); err != nil {
		t.Fatal(err)
	}
	if !p.Balance(from).Equal(math.NewInt(60)) || !p.Balance(to).Equal(math.NewInt(40)) {
		t.Fatalf("balances from=%s to=%s", p.Balance(from), p.Balance(to))
	}
	older := Key{Vintage: 1, Asset: NativeAsset}
	if err := p.Move(to, older, math.NewInt(1)); err != ErrVintage {
		t.Fatalf("downgrade: %v", err)
	}
	other := to
	other.Asset[0] = 1
	if err := p.Move(from, other, math.NewInt(1)); err != ErrVintage {
		t.Fatalf("asset change: %v", err)
	}
}

func TestMultiAssetStaysInert(t *testing.T) {
	var other [32]byte
	other[0] = 9
	if err := AllowAsset(false, other); err != ErrMultiAssetInactive {
		t.Fatalf("got %v", err)
	}
	if err := AllowAsset(false, NativeAsset); err != nil {
		t.Fatal(err)
	}
	if err := AllowAsset(true, other); err != nil {
		t.Fatal(err)
	}
}

func TestTokenPowers(t *testing.T) {
	if err := AllowToken(false, false, false); err != nil {
		t.Fatal(err)
	}
	for _, powers := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if err := AllowToken(powers[0], powers[1], powers[2]); err != ErrTokenPowers {
			t.Fatalf("powers %v: %v", powers, err)
		}
	}
}

func TestCapFloorAndExemptFee(t *testing.T) {
	floor := math.NewInt(UnshieldFloor)
	// 2% of 10 ORAMA is 0.2 ORAMA, so the floor wins.
	small := math.NewInt(10_000_000_000)
	if !CapAmount(small, floor).Equal(floor) {
		t.Fatalf("floor: %s", CapAmount(small, floor))
	}
	// 2% of 1000 ORAMA is 20 ORAMA.
	large := math.NewInt(1000_000_000_000)
	want := math.NewInt(20_000_000_000)
	if !CapAmount(large, floor).Equal(want) {
		t.Fatalf("pct: %s", CapAmount(large, floor))
	}

	lim := &Limiter{}
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	if out, err := lim.Apply(now, large, floor, math.NewInt(1), KindFeeBurn); err != nil || out != OutcomeAllow {
		t.Fatalf("fee: %v %v", out, err)
	}
	if !lim.Counted.IsZero() {
		t.Fatal("fee burn counted against the cap")
	}
	if _, err := lim.Apply(now, large, floor, want, KindAdapter); err != nil {
		t.Fatal(err)
	}
	if _, err := lim.Apply(now, large, floor, math.NewInt(1), KindAdapter); err != ErrCapExhausted {
		t.Fatalf("adapter over cap: %v", err)
	}
	out, err := lim.Apply(now, large, floor, math.NewInt(1), KindBond)
	if err != nil || out != OutcomeQueue {
		t.Fatalf("bond should queue, got %v %v", out, err)
	}
	// A new window clears the counter.
	later := now.Add(Window)
	if _, err := lim.Apply(later, large, floor, math.NewInt(1), KindAdapter); err != nil {
		t.Fatal(err)
	}
}

func TestConservation(t *testing.T) {
	p := NewPools()
	k := Key{Vintage: 1, Asset: NativeAsset}
	if err := p.Add(k, math.NewInt(1000)); err != nil {
		t.Fatal(err)
	}
	if err := p.Sub(k, math.NewInt(100)); err != nil {
		t.Fatal(err)
	}
	if err := p.Sub(k, math.NewInt(5)); err != nil {
		t.Fatal(err)
	}
	if !p.Balance(k).Equal(math.NewInt(895)) {
		t.Fatalf("balance %s", p.Balance(k))
	}
}
