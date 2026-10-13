package keeper

import (
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"time"

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
	// FaultMemory is how long the mempool refuses to put a bundle to the verifier again after its
	// check ended in a fault; MaxRememberedFaults bounds the table. Local admission policy only.
	FaultMemory         = time.Minute
	MaxRememberedFaults = 1024
	// MaxRememberedValid bounds the memory of accepted bundles.
	MaxRememberedValid = 4096
	// MaxConcurrentSimulations bounds the simulations verified at once.
	MaxConcurrentSimulations = 2
)

// ErrMempoolBusy means this node has verified its share of proofs since the last block and refuses
// more until the next one. The transaction is not invalid; it can be sent again.
var ErrMempoolBusy = errors.New(GenericRefusal)

// GenericRefusal is the one text a remote caller gets when this node cannot check a shielded bundle
// right now, whatever the reason (busy, starting, a recent fault, too many simulations): the reason
// is in the node's log.
const GenericRefusal = "the shielded verifier is unavailable on this node right now; try again later or send the transaction to another node"

// admission is the mempool's defence against proof-failing bundles: a bounded memory of bundles
// that failed verification, so each costs one verification per node, and a per-block budget of
// verifications. Both are CheckTx-only and local to a node.
type admission struct {
	mu     sync.Mutex
	failed map[[sha256.Size]byte]*list.Element
	order  *list.List // front is the newest
	budget atomic.Int64
	// faults remembers, for FaultMemory, the bundles whose check ended in a verifier fault. It is
	// not a verdict: it only keeps the mempool from putting the same hanging bundle to the verifier
	// again and again. A block never reads it.
	faults map[[sha256.Size]byte]time.Time
	now    func() time.Time
	// valid remembers bundles a verifier accepted, oldest evicted first, so a proposal does not
	// verify again what this node's mempool already checked. Valid verdicts only, and only for the
	// mempool and proposal checks: a block always verifies.
	valid      map[[sha256.Size]byte]struct{}
	validOrder *list.List
	// simulations bounds the gRPC simulations that run at once: they run outside CometBFT's ABCI
	// lock, on the mempool's verifier.
	simulations chan struct{}
}

type failedEntry struct {
	key [sha256.Size]byte
	err error
}

func newAdmission() *admission {
	return &admission{failed: map[[sha256.Size]byte]*list.Element{}, order: list.New(),
		faults: map[[sha256.Size]byte]time.Time{}, now: time.Now,
		valid: map[[sha256.Size]byte]struct{}{}, validOrder: list.New(),
		simulations: make(chan struct{}, MaxConcurrentSimulations)}
}

// validRecalled reports a bundle a verifier accepted before.
func (a *admission) validRecalled(key [sha256.Size]byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.valid[key]
	return ok
}

// rememberValid records an accepted bundle.
func (a *admission) rememberValid(key [sha256.Size]byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.valid[key]; ok {
		return
	}
	a.valid[key] = struct{}{}
	a.validOrder.PushBack(key)
	if a.validOrder.Len() > MaxRememberedValid {
		oldest := a.validOrder.Front()
		a.validOrder.Remove(oldest)
		delete(a.valid, oldest.Value.([sha256.Size]byte))
	}
}

// faultRecalled reports a check of these exact bytes that ended in a fault less than FaultMemory ago.
func (a *admission) faultRecalled(key [sha256.Size]byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	until, ok := a.faults[key]
	if !ok {
		return false
	}
	if !a.now().Before(until) {
		delete(a.faults, key)
		return false
	}
	return true
}

// rememberFault records a fault for FaultMemory. The table is bounded: when it is full the expired
// entries go first, then the oldest, so a stream of distinct hanging bundles cannot grow it.
func (a *admission) rememberFault(key [sha256.Size]byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if len(a.faults) >= MaxRememberedFaults {
		var oldest [sha256.Size]byte
		var oldestAt time.Time
		for k, until := range a.faults {
			if !now.Before(until) {
				delete(a.faults, k)
				continue
			}
			if oldestAt.IsZero() || until.Before(oldestAt) {
				oldest, oldestAt = k, until
			}
		}
		if len(a.faults) >= MaxRememberedFaults {
			delete(a.faults, oldest)
		}
	}
	a.faults[key] = now.Add(FaultMemory)
}

// key is the hash of the exact bytes: a different byte, however small, is a different bundle, so
// remembering one bundle's failure can never refuse another.
func admissionKey(raw, binding []byte) [sha256.Size]byte {
	h := sha256.New()
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(raw)))
	h.Write(n[:]) // the bundle's length, so a bundle and a binding cannot be re-split into another pair
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
