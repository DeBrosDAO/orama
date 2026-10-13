package chainfaucet

import (
	"math/big"
	"testing"
	"time"
)

func testBudget(limit int64) (*Budget, *time.Time) {
	b := NewBudget(big.NewInt(limit), time.Hour)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return now }
	return b, &now
}

func TestBudget_takesUpToTheLimitAndNoMore(t *testing.T) {
	b, _ := testBudget(100)
	if _, _, ok := b.Take("a", big.NewInt(60)); !ok {
		t.Fatal("a drip inside the allowance was refused")
	}
	if _, _, ok := b.Take("a", big.NewInt(40)); !ok {
		t.Fatal("a drip that exactly spends the allowance was refused")
	}
	_, retry, ok := b.Take("a", big.NewInt(1))
	if ok || retry != time.Hour {
		t.Fatalf("Take over the allowance = %v, %v; want a refusal that says to come back in an hour", retry, ok)
	}
}

func TestBudget_aRefusalChargesNothingAndEachClientHasItsOwn(t *testing.T) {
	b, _ := testBudget(100)
	if _, _, ok := b.Take("a", big.NewInt(101)); ok {
		t.Fatal("a drip over the whole allowance was taken")
	}
	if _, _, ok := b.Take("a", big.NewInt(100)); !ok {
		t.Error("a refused drip used up the allowance")
	}
	if _, _, ok := b.Take("b", big.NewInt(100)); !ok {
		t.Error("one client's drips spent another's allowance")
	}
}

func TestBudget_theAllowanceComesBackAfterTheWindow(t *testing.T) {
	b, now := testBudget(100)
	b.Take("a", big.NewInt(100))
	*now = now.Add(30 * time.Minute)
	if _, retry, ok := b.Take("a", big.NewInt(1)); ok || retry != 30*time.Minute {
		t.Fatalf("half way through: %v, %v; want a refusal that says to come back in 30 minutes", retry, ok)
	}
	*now = now.Add(30 * time.Minute)
	if _, _, ok := b.Take("a", big.NewInt(100)); !ok {
		t.Fatal("the allowance did not come back when the window ended")
	}
}

func TestBudget_aDripThatWasNotMadeIsGivenBack(t *testing.T) {
	b, _ := testBudget(100)
	ticket, _, _ := b.Take("a", big.NewInt(100))
	b.Return(ticket)
	if _, _, ok := b.Take("a", big.NewInt(100)); !ok {
		t.Error("the allowance of a drip that was refused was not given back")
	}
	b.Return(Ticket{})
	b.Return(Ticket{client: "never-asked", amount: big.NewInt(5)})
	if _, _, ok := b.Take("a", big.NewInt(1)); ok {
		t.Error("giving back a ticket that is not one raised the allowance")
	}
}

// A charge from a window that is over was not charged to the window that replaced it, so giving it
// back must not lower that one.
func TestBudget_aTicketOfAnEarlierWindowGivesNothingBack(t *testing.T) {
	b, now := testBudget(100)
	old, _, _ := b.Take("a", big.NewInt(60))
	*now = now.Add(2 * time.Hour)
	b.Take("a", big.NewInt(100))
	b.Return(old)
	if _, _, ok := b.Take("a", big.NewInt(1)); ok {
		t.Error("a ticket of the day before gave back to today's window")
	}
}

func TestBudget_memoryIsBounded(t *testing.T) {
	b, now := testBudget(100)
	for i := 0; i < maxBudgetClients+500; i++ {
		b.Take(string(rune('a'+i%26))+time.Duration(i).String(), big.NewInt(1))
	}
	if len(b.spent) > maxBudgetClients {
		t.Fatalf("%d clients tracked, want at most %d", len(b.spent), maxBudgetClients)
	}
	*now = now.Add(2 * time.Hour)
	b.Take("fresh", big.NewInt(1))
	if len(b.spent) != 1 {
		t.Errorf("%d ledgers after every window ended, want only the new one", len(b.spent))
	}
}
