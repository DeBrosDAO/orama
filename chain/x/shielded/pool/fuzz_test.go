package pool

import (
	"testing"
	"time"

	"cosmossdk.io/math"
)

// FuzzPoolServe: the queue never pays out more than the window's capacity, never pays an address
// more than it asked for or (across its requests) than the per-address cap, never a negative
// amount or a part payment accept refused, the grants line up one to one with the requests, and
// it is oldest first: a request is cut short only when the capacity or its address's cap is used
// up, or accept refused the part.
func FuzzPoolServe(f *testing.F) {
	f.Add(uint64(100), uint64(50), uint64(0), []byte{10, 20, 30, 0, 200})
	f.Add(uint64(0), uint64(5), uint64(1), []byte{1})
	f.Add(uint64(1<<63), uint64(1<<62), uint64(1<<20), []byte{255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, capacity, perAddress, minimumPart uint64, amounts []byte) {
		reqs := make([]Request, len(amounts))
		for i, a := range amounts {
			// A spread of magnitudes from the one byte, up to 2^64.
			reqs[i] = Request{Address: string(rune('a' + i%26)), Amount: math.NewIntFromUint64(uint64(a) << (uint(a) % 57))}
		}
		capInt, perInt := math.NewIntFromUint64(capacity), math.NewIntFromUint64(perAddress)
		minPart := math.NewIntFromUint64(minimumPart)
		accept := func(_ int, grant math.Int) bool { return grant.GTE(minPart) }
		grants, _, _ := Serve(capInt, perInt, len(reqs), reqs, accept, nil)
		if len(grants) != len(reqs) {
			t.Fatalf("%d grants for %d requests", len(grants), len(reqs))
		}
		total := math.ZeroInt()
		perAddr := map[string]math.Int{}
		for i, g := range grants {
			if g.Amount.IsNegative() || g.Amount.GT(reqs[i].Amount) {
				t.Fatalf("grant %d is %s for a request of %s", i, g.Amount, reqs[i].Amount)
			}
			if g.Amount.IsPositive() && g.Amount.LT(reqs[i].Amount) && !accept(i, g.Amount) {
				t.Fatalf("grant %d is a part payment of %s that accept refused", i, g.Amount)
			}
			sum, ok := perAddr[g.Address]
			if !ok {
				sum = math.ZeroInt()
			}
			perAddr[g.Address] = sum.Add(g.Amount)
			if perAddr[g.Address].GT(perInt) {
				t.Fatalf("address %q was paid %s against a per-address cap of %s", g.Address, perAddr[g.Address], perInt)
			}
			if g.Amount.LT(reqs[i].Amount) {
				leftCap := capInt.Sub(total.Add(g.Amount))
				leftAddr := perInt.Sub(perAddr[g.Address])
				room := math.MinInt(leftCap, leftAddr)
				if room.IsPositive() && accept(i, math.MinInt(room.Add(g.Amount), reqs[i].Amount)) {
					t.Fatalf("grant %d was cut to %s with %s of room left", i, g.Amount, room)
				}
			}
			total = total.Add(g.Amount)
		}
		if total.GT(capInt) {
			t.Fatalf("paid %s out of a capacity of %s", total, capInt)
		}
		again, _, _ := Serve(capInt, perInt, len(reqs), reqs, accept, nil)
		for i := range grants {
			if !grants[i].Amount.Equal(again[i].Amount) {
				t.Fatal("Serve is not deterministic")
			}
		}
	})
}

// FuzzPoolAccounting runs a byte-coded sequence of Add, Sub, Move and limiter calls. A pool never
// goes negative, a failed call changes nothing, a Move keeps the total, and the limiter never lets
// its counted outflow pass its capacity (half the cap, so no 24 hours lets more than one cap out).
func FuzzPoolAccounting(f *testing.F) {
	f.Add([]byte{0, 1, 50, 1, 1, 20, 2, 1, 2, 30, 3, 1, 9})
	f.Add([]byte{1, 0, 1, 2, 0, 1})
	f.Fuzz(func(t *testing.T, script []byte) {
		p := NewPools()
		var lim Limiter
		clock := time.Unix(1_700_000_000, 0)
		key := func(b byte) Key { return Key{Vintage: uint32(b % 3), Asset: NativeAsset} }
		total := func() math.Int {
			sum := math.ZeroInt()
			for v := byte(0); v < 3; v++ {
				sum = sum.Add(p.Balance(key(v)))
			}
			return sum
		}
		for len(script) >= 3 {
			op, a, b := script[0]%4, script[1], script[2]
			script = script[3:]
			amount := math.NewIntFromUint64(uint64(b) * 1_000_000)
			before := total()
			switch op {
			case 0:
				if err := p.Add(key(a), amount); err == nil && !total().Equal(before.Add(amount)) {
					t.Fatal("Add changed the total by another amount")
				}
			case 1:
				bal := p.Balance(key(a))
				err := p.Sub(key(a), amount)
				if err != nil && !p.Balance(key(a)).Equal(bal) {
					t.Fatal("a refused Sub changed the balance")
				}
				if err == nil && !p.Balance(key(a)).Equal(bal.Sub(amount)) {
					t.Fatal("Sub took another amount")
				}
			case 2:
				if err := p.Move(key(a), key(b), amount); err == nil && !total().Equal(before) {
					t.Fatal("Move changed the total")
				} else if err != nil && !total().Equal(before) {
					t.Fatal("a refused Move changed the total")
				}
			default:
				clock = clock.Add(time.Duration(a) * time.Hour)
				kind := Kind(b % 5)
				out, err := lim.Apply(clock, total(), math.NewInt(UnshieldFloor), amount, kind)
				if err == nil && out == OutcomeAllow && kind != KindFeeBurn &&
					lim.Counted.GT(Capacity(CapAmount(total(), math.NewInt(UnshieldFloor)))) {
					t.Fatalf("counted %s passes the capacity %s", lim.Counted, Capacity(CapAmount(total(), math.NewInt(UnshieldFloor))))
				}
			}
			for v := byte(0); v < 3; v++ {
				if p.Balance(key(v)).IsNegative() {
					t.Fatal("a pool went negative")
				}
			}
		}
	})
}
