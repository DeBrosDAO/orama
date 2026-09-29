package app

import (
	"crypto/sha256"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

const (
	// inclusionIncludeAfter is how long a transaction must have sat in this
	// node's mempool, passing CheckTx, before ExtendVote lists it (C13
	// include_after). A transaction a proposer would normally take within a
	// couple of blocks is never listed.
	inclusionIncludeAfter = 10 * time.Second

	// inclusionPoolMaxAge drops a transaction from the seen-set after this
	// long. CometBFT expires or evicts mempool transactions without telling
	// the app, so the set needs its own expiry.
	inclusionPoolMaxAge = time.Hour

	// inclusionPoolMaxBytes bounds the memory the seen-set holds. A full set
	// stops tracking new transactions; they still reach the mempool.
	inclusionPoolMaxBytes = 16 * 1024 * 1024
)

type poolEntry struct {
	raw       []byte
	firstSeen time.Time
}

// inclusionPool is this node's record of transactions that passed CheckTx and
// when it first saw them. The app cannot read CometBFT's mempool, so
// ExtendVote takes its candidates from here. It is local, non-consensus state.
type inclusionPool struct {
	mu       sync.Mutex
	entries  map[[sha256.Size]byte]poolEntry
	bytes    int
	maxBytes int
	maxAge   time.Duration
}

func newInclusionPool(maxBytes int, maxAge time.Duration) *inclusionPool {
	return &inclusionPool{
		entries:  make(map[[sha256.Size]byte]poolEntry),
		maxBytes: maxBytes,
		maxAge:   maxAge,
	}
}

// Add records raw as first seen at now. It reports whether the transaction is
// tracked: a transaction too large to ever be listed, or one that would push
// the set past its byte bound, is not.
func (p *inclusionPool) Add(raw []byte, now time.Time) bool {
	if len(raw) == 0 || len(raw) > inclusion.DefaultListMaxBytes {
		return false
	}
	key := sha256.Sum256(raw)
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.entries[key]; ok {
		return true
	}
	if p.bytes+len(raw) > p.maxBytes {
		return false
	}
	p.entries[key] = poolEntry{raw: append([]byte(nil), raw...), firstSeen: now}
	p.bytes += len(raw)
	return true
}

// Remove forgets the given transactions.
func (p *inclusionPool) Remove(raws ...[]byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, raw := range raws {
		key := sha256.Sum256(raw)
		if e, ok := p.entries[key]; ok {
			p.bytes -= len(e.raw)
			delete(p.entries, key)
		}
	}
}

// Eligible returns the transactions first seen at least after ago, dropping
// any older than the pool's max age on the way.
func (p *inclusionPool) Eligible(now time.Time, after time.Duration) [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]byte, 0, len(p.entries))
	for key, e := range p.entries {
		age := now.Sub(e.firstSeen)
		if age > p.maxAge {
			p.bytes -= len(e.raw)
			delete(p.entries, key)
			continue
		}
		if age >= after {
			out = append(out, append([]byte(nil), e.raw...))
		}
	}
	return out
}

// Len is the number of tracked transactions.
func (p *inclusionPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}
