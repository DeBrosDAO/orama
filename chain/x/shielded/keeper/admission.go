package keeper

import (
	"container/list"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

const (
	// MaxFailedBundles is how many rejected bundles the mempool remembers.
	MaxFailedBundles = 4096
	// MaxCheckVerificationsPerBlock bounds the proofs one node verifies in CheckTx between two
	// blocks. A signer-less transfer pays nothing before its proof is checked, so without a bound a
	// stream of bundles that fail only the Halo 2 proof costs every node one verification each. It is
	// local admission policy: it never runs in a block and never affects state.
	MaxCheckVerificationsPerBlock = 256
)

// ErrMempoolBusy means this node has verified its share of proofs since the last block and refuses
// more until the next one. The transaction is not invalid; it can be sent again.
var ErrMempoolBusy = errors.New("shielded proof verification is busy in this block, try again next block")

// admission is the mempool's defence against proof-failing bundles: a bounded memory of bundles
// that failed verification, so each costs one verification per node, and a per-block budget of
// verifications. Both are CheckTx-only and local to a node.
type admission struct {
	mu     sync.Mutex
	failed map[[sha256.Size]byte]*list.Element
	order  *list.List // front is the newest
	budget atomic.Int64
}

type failedEntry struct {
	key [sha256.Size]byte
	err error
}

func newAdmission() *admission {
	return &admission{failed: map[[sha256.Size]byte]*list.Element{}, order: list.New()}
}

// key is the hash of the exact bytes: a different byte, however small, is a different bundle, so
// remembering one bundle's failure can never refuse another.
func admissionKey(raw, binding []byte) [sha256.Size]byte {
	h := sha256.New()
	h.Write(raw)
	h.Write(binding)
	var out [sha256.Size]byte
	h.Sum(out[:0])
	return out
}

// recalled returns the remembered rejection of these exact bytes.
func (a *admission) recalled(key [sha256.Size]byte) (error, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	el, ok := a.failed[key]
	if !ok {
		return nil, false
	}
	a.order.MoveToFront(el)
	return el.Value.(failedEntry).err, true
}

// remember records a rejection. Only a verdict on the bundle is kept: a verifier fault says
// nothing about the bundle, so it is never remembered.
func (a *admission) remember(key [sha256.Size]byte, err error) {
	if !errors.Is(err, verify.ErrTampered) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if el, ok := a.failed[key]; ok {
		a.order.MoveToFront(el)
		return
	}
	a.failed[key] = a.order.PushFront(failedEntry{key: key, err: err})
	if a.order.Len() > MaxFailedBundles {
		oldest := a.order.Back()
		a.order.Remove(oldest)
		delete(a.failed, oldest.Value.(failedEntry).key)
	}
}

// spend takes one verification from the block's budget.
func (a *admission) spend() bool { return a.budget.Add(1) <= MaxCheckVerificationsPerBlock }

// newBlock refills the budget.
func (a *admission) newBlock() { a.budget.Store(0) }
