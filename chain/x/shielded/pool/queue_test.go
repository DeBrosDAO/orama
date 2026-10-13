package pool

import (
	"testing"

	"cosmossdk.io/math"
)

func requireGrants(t *testing.T, got []Grant, want ...string) {
	t.Helper()
	have := make([]string, len(got))
	for i, g := range got {
		have[i] = g.Amount.String()
	}
	if len(have) != len(want) {
		t.Fatalf("grants %v, want %v", have, want)
	}
	for i := range want {
		if have[i] != want[i] {
			t.Fatalf("grants %v, want %v", have, want)
		}
	}
}

// grantsOf serves reqs with no accept and no pay and returns the grants alone.
func grantsOf(capacity, perAddress math.Int, reqs ...Request) []Grant {
	grants, _, _ := Serve(capacity, perAddress, 1000, reqs, nil, nil)
	return grants
}

func req(address string, amount int64) Request {
	return Request{Address: address, Amount: math.NewInt(amount)}
}

func TestServe_capsTheWhale(t *testing.T) {
	got, _, _ := Serve(math.NewInt(100), math.NewInt(40), 1000, []Request{req("small", 10), req("whale", 90)}, nil, nil)
	requireGrants(t, got, "10", "40")
}

func TestServe_paysInFullWhenThereIsRoom(t *testing.T) {
	got, _, _ := Serve(math.NewInt(100), math.NewInt(100), 1000, []Request{req("a", 25), req("b", 75)}, nil, nil)
	requireGrants(t, got, "25", "75")
}

func TestServe_servesOldestFirstUntilTheCapacityIsGone(t *testing.T) {
	got, _, _ := Serve(math.NewInt(50), math.NewInt(1000), 1000, []Request{req("a", 40), req("b", 40), req("c", 40)}, nil, nil)
	requireGrants(t, got, "40", "10", "0")
}

func TestServe_aSmallRequestIsNotStarvedByALargerOneBehindIt(t *testing.T) {
	got, _, _ := Serve(math.NewInt(50), math.NewInt(1000), 1000, []Request{req("small", 10), req("whale", 900)}, nil, nil)
	requireGrants(t, got, "10", "40")
}

func TestServe_perAddressCapBindsAcrossAnAddressesRequests(t *testing.T) {
	got, _, _ := Serve(math.NewInt(100), math.NewInt(30), 1000, []Request{req("w", 20), req("w", 20), req("o", 20)}, nil, nil)
	requireGrants(t, got, "20", "10", "20")
}

func TestServe_aPartPaymentTheTargetRefusesIsSkippedAndKeepsTheCapacity(t *testing.T) {
	refuse := func(_ int, grant math.Int) bool { return grant.GTE(math.NewInt(10)) }
	got, _, _ := Serve(math.NewInt(50), math.NewInt(1000), 1000, []Request{req("a", 45), req("b", 40), req("c", 5)}, refuse, nil)
	// a is paid in full; b could only be paid 5, a part below 10, and is skipped; c is paid in full.
	requireGrants(t, got, "45", "0", "5")
}

func TestServe_aFullPaymentIsIssuedEvenIfAcceptWouldRefuseIt(t *testing.T) {
	refuse := func(int, math.Int) bool { return false }
	got, _, _ := Serve(math.NewInt(50), math.NewInt(1000), 1000, []Request{req("a", 7)}, refuse, nil)
	requireGrants(t, got, "7")
}

func TestServe_emptyCapacityAndBadRequests(t *testing.T) {
	requireGrants(t, grantsOf(math.ZeroInt(), math.NewInt(10), req("a", 5)), "0")
	requireGrants(t, grantsOf(math.NewInt(10), math.ZeroInt(), req("a", 5)), "0")
	requireGrants(t, grantsOf(math.NewInt(10), math.NewInt(10), req("a", 0), Request{Address: "b"}, req("c", -3)), "0", "0", "0")
	if got, refused, _ := Serve(math.NewInt(10), math.NewInt(10), 1000, nil, nil, nil); len(got) != 0 || len(refused) != 0 {
		t.Fatalf("no requests: %v", got)
	}
}

func TestServe_aRefusedPaymentKeepsTheCapacityForTheRequestsBehindIt(t *testing.T) {
	refuseHead := func(i int, _ math.Int) bool { return i != 0 }
	got, refused, _ := Serve(math.NewInt(50), math.NewInt(1000), 1000, []Request{req("dead", 50), req("a", 30), req("b", 30)}, nil, refuseHead)
	requireGrants(t, got, "0", "30", "20")
	if len(refused) != 1 || refused[0] != 0 {
		t.Fatalf("refused %v, want [0]", refused)
	}
}

func TestServe_aRefusedPaymentDoesNotUseTheAddressCap(t *testing.T) {
	refuseFirst := func(i int, _ math.Int) bool { return i != 0 }
	got, _, _ := Serve(math.NewInt(100), math.NewInt(30), 1000, []Request{req("w", 30), req("w", 30)}, nil, refuseFirst)
	requireGrants(t, got, "0", "30")
}

func TestServe_everyPaymentRefused(t *testing.T) {
	got, refused, _ := Serve(math.NewInt(100), math.NewInt(100), 1000, []Request{req("a", 5), req("b", 5)}, nil, func(int, math.Int) bool { return false })
	requireGrants(t, got, "0", "0")
	if len(refused) != 2 {
		t.Fatalf("refused %v, want both", refused)
	}
}

func TestServe_aRefusedPaymentDoesNotCountAgainstTheNumberOfPayments(t *testing.T) {
	refuseHead := func(i int, _ math.Int) bool { return i >= 3 }
	reqs := []Request{req("d1", 5), req("d2", 5), req("d3", 5), req("a", 5), req("b", 5), req("c", 5)}
	got, refused, _ := Serve(math.NewInt(100), math.NewInt(100), 2, reqs, nil, refuseHead)
	requireGrants(t, got, "0", "0", "0", "5", "5", "0")
	if len(refused) != 3 {
		t.Fatalf("refused %v, want the three at the head", refused)
	}
}

func TestServe_stopsAfterTheNumberOfPaymentsThatWentOut(t *testing.T) {
	got, refused, _ := Serve(math.NewInt(100), math.NewInt(100), 2, []Request{req("a", 5), req("b", 5), req("c", 5)}, nil, nil)
	requireGrants(t, got, "5", "5", "0")
	if len(refused) != 0 {
		t.Fatalf("refused %v, want none", refused)
	}
}

// Serve reports how far the window got: rows after the point where the capacity or the payment
// budget ran out were not looked at, so the caller leaves their state alone.
func TestServe_examinedStopsWhereTheWindowStopped(t *testing.T) {
	reqs := []Request{req("a", 30), req("b", 30), req("c", 30), req("d", 30)}
	if _, _, examined := Serve(math.NewInt(50), math.NewInt(1000), 1000, reqs, nil, nil); examined != 2 {
		t.Fatalf("capacity 50 runs out at the second request: examined %d, want 2", examined)
	}
	if _, _, examined := Serve(math.NewInt(1000), math.NewInt(1000), 1, reqs, nil, nil); examined != 1 {
		t.Fatalf("a budget of one payment stops after the first: examined %d, want 1", examined)
	}
	if _, _, examined := Serve(math.NewInt(1000), math.NewInt(1000), 1000, reqs, nil, nil); examined != len(reqs) {
		t.Fatalf("room for all: examined %d, want %d", examined, len(reqs))
	}
	if _, _, examined := Serve(math.NewInt(10), math.NewInt(10), 1000, nil, nil, nil); examined != 0 {
		t.Fatalf("no requests: examined %d, want 0", examined)
	}
}
