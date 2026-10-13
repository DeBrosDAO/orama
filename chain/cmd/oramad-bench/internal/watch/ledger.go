// Package watch observes the chain while a load runs: which of our transactions each block
// included, how full the mempools are, and what the base fee did. It only reads; nothing here
// sends a transaction.
package watch

import (
	"sync"
	"time"
)

// Outcome is what became of a transaction.
type Outcome int

const (
	// Pending: sent (or about to be) and not yet seen in a block or refused.
	Pending Outcome = iota
	// Included: a block included it and it executed.
	Included
	// FailedInBlock: a block included it and its execution failed. It paid its fee and did nothing.
	FailedInBlock
	// Rejected: CheckTx refused it; it never reached a block.
	Rejected
	// NotSent: the sender held it back because an earlier transaction of its account was refused,
	// which leaves a hole in the account's sequence that no later transaction can cross.
	NotSent
)

func (o Outcome) String() string {
	switch o {
	case Included:
		return "included"
	case FailedInBlock:
		return "failed-in-block"
	case Rejected:
		return "rejected"
	case NotSent:
		return "not-sent"
	default:
		return "pending"
	}
}

// Record is the life of one transaction.
type Record struct {
	SentAt time.Time
	DoneAt time.Time
	Outcome
	// Code and Log are the chain's answer for a failure (CheckTx or execution).
	Code   uint32
	Log    string
	Height int64
}

// Latency is the time from the start of the broadcast to the moment the block that included the
// transaction was seen. It is zero for a transaction that was never included.
func (r Record) Latency() time.Duration {
	if r.Outcome != Included && r.Outcome != FailedInBlock {
		return 0
	}
	return r.DoneAt.Sub(r.SentAt)
}

// Ledger records the fate of the transactions of one step. A transaction is registered before it is
// sent, so a block that includes it can never be seen before the ledger knows about it.
type Ledger struct {
	mu       sync.Mutex
	recs     map[string]*Record
	pending  int
	done     int
	resolved chan struct{}
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{recs: map[string]*Record{}, resolved: make(chan struct{}, 1)}
}

// Register adds a transaction by hash. Registering a hash twice is a no-op.
func (l *Ledger) Register(hash string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.recs[hash]; !ok {
		l.recs[hash] = &Record{}
		l.pending++
	}
}

// Sent stamps the start of a transaction's broadcast.
func (l *Ledger) Sent(hash string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, ok := l.recs[hash]; ok && r.SentAt.IsZero() {
		r.SentAt = at
	}
}

// Rejected records that CheckTx refused the transaction.
func (l *Ledger) Rejected(hash string, at time.Time, code uint32, log string) {
	l.resolve(hash, Record{DoneAt: at, Outcome: Rejected, Code: code, Log: log})
}

// NotSent records that the sender held the transaction back, and why.
func (l *Ledger) NotSent(hash string, at time.Time, reason string) {
	l.resolve(hash, Record{DoneAt: at, Outcome: NotSent, Log: reason})
}

// Included records that a block (seen at the given time) included the transaction, with the code
// and log of its execution. It reports whether the ledger knew the transaction.
func (l *Ledger) Included(hash string, at time.Time, height int64, code uint32, log string) bool {
	out := Included
	if code != 0 {
		out = FailedInBlock
	}
	return l.resolve(hash, Record{DoneAt: at, Outcome: out, Code: code, Log: log, Height: height})
}

func (l *Ledger) resolve(hash string, done Record) bool {
	l.mu.Lock()
	r, ok := l.recs[hash]
	if !ok || r.Outcome != Pending {
		l.mu.Unlock()
		return ok
	}
	done.SentAt = r.SentAt
	*r = done
	l.pending--
	l.done++
	l.mu.Unlock()
	select {
	case l.resolved <- struct{}{}:
	default:
	}
	return true
}

// Pending is how many registered transactions have no outcome yet.
func (l *Ledger) Pending() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pending
}

// Done is how many transactions have an outcome.
func (l *Ledger) Done() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.done
}

// Resolved receives a value whenever some transaction got an outcome (values coalesce), so a
// closed-loop sender can wait instead of polling.
func (l *Ledger) Resolved() <-chan struct{} { return l.resolved }

// Snapshot copies every record.
func (l *Ledger) Snapshot() map[string]Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]Record, len(l.recs))
	for h, r := range l.recs {
		out[h] = *r
	}
	return out
}
