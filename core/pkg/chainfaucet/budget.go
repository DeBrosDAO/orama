package chainfaucet

import (
	"math/big"
	"sync"
	"time"
)

const (
	// BudgetWindow is how long a client network's allowance lasts: it is spent down over a day and
	// comes back whole after one.
	BudgetWindow = 24 * time.Hour
	// DefaultBudgetNorama is a client network's allowance for the window: 20,000 ORAMA. One
	// `orama setup` needs a little over 1,000, and the default drip is 100, so a person is nowhere
	// near it; a script that asks for the chain's maximum drip for fresh recipients, to spend the
	// chain's cap for everyone else, is stopped after two.
	DefaultBudgetNorama = 20_000 * 1_000_000_000
	// DefaultCeilingNorama is what one gateway gives out in a window to all clients together: 200,000
	// ORAMA, some two hundred setups. A client network's allowance cannot bound a caller that names
	// its own network (a process on the node sends X-Forwarded-For as Caddy does), nor an attacker with
	// many networks, nor an allowance table that is full; the ceiling does, because no client key goes
	// into it.
	DefaultCeilingNorama = 200_000 * 1_000_000_000
	// maxBudgetClients bounds the clients a Budget tracks. Past it a client's entry is dropped to
	// make room, which gives that client a fresh allowance: memory stays bounded, and the rate
	// limiter in front of the route remains the limit on how often it can ask.
	maxBudgetClients = 4096
)

// Budget limits the norama one client network can ask for in a window. The chain bounds a drip
// (its maximum), a recipient (a cooldown) and the faucet as a whole (the epoch cap), but a
// recipient costs nothing to invent, so one client could ask the maximum for fresh recipients until
// the cap is spent and the faucet is empty for everyone. The allowance is the part of the cap one
// client network can hold at a time.
type Budget struct {
	mu     sync.Mutex
	limit  *big.Int
	window time.Duration
	now    func() time.Time
	spent  map[string]*ledger
}

type ledger struct {
	amount *big.Int
	since  time.Time
}

// NewBudget returns a Budget of limit norama per window.
func NewBudget(limit *big.Int, window time.Duration) *Budget {
	return &Budget{limit: new(big.Int).Set(limit), window: window, now: time.Now, spent: map[string]*ledger{}}
}

// Ticket is what a Take charged, to give back with Return. It names the window it was charged in.
type Ticket struct {
	client string
	amount *big.Int
	since  time.Time
}

// Take charges amount to client. It reports false, and how long until the client's allowance
// comes back, when that would pass the limit; nothing is charged then.
func (b *Budget) Take(client string, amount *big.Int) (ticket Ticket, retryAfter time.Duration, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	l := b.ledgerFor(client, now)
	next := new(big.Int).Add(l.amount, amount)
	if next.Cmp(b.limit) > 0 {
		return Ticket{}, l.since.Add(b.window).Sub(now), false
	}
	l.amount = next
	return Ticket{client: client, amount: new(big.Int).Set(amount), since: l.since}, 0, true
}

// Return gives back what a Take charged for a drip that was not made. A charge from a window that
// is over is not given back to the window that replaced it: that one never held it.
func (b *Budget) Return(t Ticket) {
	b.mu.Lock()
	defer b.mu.Unlock()
	l, ok := b.spent[t.client]
	if !ok || !l.since.Equal(t.since) {
		return
	}
	if l.amount.Sub(l.amount, t.amount).Sign() < 0 {
		l.amount.SetInt64(0)
	}
}

// ledgerFor is client's ledger, started afresh when its window is over or it was never there.
func (b *Budget) ledgerFor(client string, now time.Time) *ledger {
	if l, ok := b.spent[client]; ok && now.Sub(l.since) < b.window {
		return l
	}
	if _, known := b.spent[client]; !known && len(b.spent) >= maxBudgetClients {
		b.makeRoom(now)
	}
	l := &ledger{amount: new(big.Int), since: now}
	b.spent[client] = l
	return l
}

// makeRoom drops the ledgers whose window is over, and one more if none was.
func (b *Budget) makeRoom(now time.Time) {
	for client, l := range b.spent {
		if now.Sub(l.since) >= b.window {
			delete(b.spent, client)
		}
	}
	if len(b.spent) < maxBudgetClients {
		return
	}
	for client := range b.spent {
		delete(b.spent, client)
		return
	}
}
