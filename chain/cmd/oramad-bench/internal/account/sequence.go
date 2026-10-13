package account

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrUnknownAccount is returned for an address the Sequencer was never given a sequence for.
var ErrUnknownAccount = errors.New("account has no sequence; load it from the chain first")

// Lookup reads an account's committed sequence from the chain.
type Lookup func(ctx context.Context, addr string) (sequence uint64, err error)

// Sequencer hands out the consecutive sequences a batch of pre-signed transactions needs. Signing
// happens before sending, so the sequences are decided offline: the n-th transaction of an account
// carries chain sequence + n. After any step that left a transaction unincluded the Sequencer is
// reloaded from the chain, never patched by hand.
type Sequencer struct {
	mu   sync.Mutex
	next map[string]uint64
}

// NewSequencer returns an empty Sequencer.
func NewSequencer() *Sequencer { return &Sequencer{next: map[string]uint64{}} }

// Set makes seq the next sequence of addr, replacing any earlier value.
func (s *Sequencer) Set(addr string, seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next[addr] = seq
}

// Take returns addr's next sequence and advances it.
func (s *Sequencer) Take(addr string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, ok := s.next[addr]
	if !ok {
		return 0, fmt.Errorf("%s: %w", addr, ErrUnknownAccount)
	}
	s.next[addr] = seq + 1
	return seq, nil
}

// Peek returns addr's next sequence without taking it.
func (s *Sequencer) Peek(addr string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, ok := s.next[addr]
	return seq, ok
}

// Reload sets every address's next sequence to what the chain has committed for it.
func (s *Sequencer) Reload(ctx context.Context, lookup Lookup, addrs []string) error {
	for _, addr := range addrs {
		seq, err := lookup(ctx, addr)
		if err != nil {
			return fmt.Errorf("read the sequence of %s: %w", addr, err)
		}
		s.Set(addr, seq)
	}
	return nil
}

// Gate stops the sender from sending transactions that can no longer be accepted. A transaction
// the chain refused leaves a hole in its account's sequence, so every later transaction of that
// account would fail with a sequence mismatch and bury the real cause in noise.
type Gate struct {
	mu     sync.Mutex
	failed map[string]uint64
}

// NewGate returns a Gate that blocks nothing.
func NewGate() *Gate { return &Gate{failed: map[string]uint64{}} }

// Fail records that the transaction of addr with sequence seq was refused. The lowest recorded
// sequence of an account is the one that counts.
func (g *Gate) Fail(addr string, seq uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if first, ok := g.failed[addr]; !ok || seq < first {
		g.failed[addr] = seq
	}
}

// Blocked reports whether the transaction of addr with sequence seq follows a refused one.
func (g *Gate) Blocked(addr string, seq uint64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	first, ok := g.failed[addr]
	return ok && seq > first
}

// Failures is how many accounts have a refused transaction.
func (g *Gate) Failures() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.failed)
}
