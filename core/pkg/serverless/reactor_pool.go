package serverless

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// reactorWarmTimeout bounds a single background warm (instantiate + run the
// TinyGo _initialize cold-start). A runaway init can't pin a warmer goroutine.
const reactorWarmTimeout = 10 * time.Second

// reactorWarmer instantiates a fresh, fully-initialized reactor instance for
// wasmCID — it runs the TinyGo _initialize cold-start so the returned instance
// can serve a single `handle` call immediately. name must be unique within the
// wazero runtime (wazero rejects duplicate instance names).
type reactorWarmer func(ctx context.Context, wasmCID, name string) (api.Module, error)

// reactorPool pre-warms ONE-SHOT wasm instances per compiled module (keyed by
// WASMCID) so a reactor-mode invocation skips the ~550ms TinyGo _initialize
// cold-start on its request path (bugboard #898 — spun out of feat-27/#708).
//
// SAFETY MODEL (the property that makes this safe to enable on a privacy-first
// platform): every warmed instance serves EXACTLY ONE invocation and is then
// closed — instances are NEVER reused across calls. The pool's isolation is
// therefore identical to the current fresh-instance-per-call model; no
// cross-tenant or cross-invocation state can leak through a pooled instance.
// The pool only changes WHEN the cold-start runs (ahead of the request, in the
// background), not WHETHER instances are shared. An empty pool degrades
// gracefully: Acquire returns ok=false and the caller instantiates
// synchronously, so a burst never blocks on warming.
type reactorPool struct {
	warm       reactorWarmer
	logger     *zap.Logger
	target     int // ready instances to keep per module
	maxModules int // cap on distinct module pools (LRU-evicted)

	mu      sync.Mutex
	pools   map[string]*modulePool
	useSeq  uint64 // monotonic, for LRU eviction
}

type modulePool struct {
	ready    chan api.Module
	inflight int    // warms in progress (guarded by reactorPool.mu)
	nameSeq  uint64 // unique instance-name counter (guarded by reactorPool.mu)
	lastUse  uint64 // for LRU (guarded by reactorPool.mu)
}

func newReactorPool(warm reactorWarmer, target, maxModules int, logger *zap.Logger) *reactorPool {
	if target < 1 {
		target = 1
	}
	if maxModules < 1 {
		maxModules = 1
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &reactorPool{
		warm:       warm,
		logger:     logger,
		target:     target,
		maxModules: maxModules,
		pools:      make(map[string]*modulePool),
	}
}

// Acquire returns a pre-warmed instance for wasmCID if one is ready, then kicks
// off a background refill toward the target. ok=false means the pool was empty —
// the caller MUST instantiate synchronously. The returned instance belongs to
// the caller, which must Close it after its single handle call.
func (p *reactorPool) Acquire(wasmCID string) (api.Module, bool) {
	p.mu.Lock()
	mp := p.getOrCreatePoolLocked(wasmCID)
	p.mu.Unlock()

	var inst api.Module
	select {
	case inst = <-mp.ready:
	default:
	}
	p.refill(wasmCID, mp)
	return inst, inst != nil
}

// getOrCreatePoolLocked returns the modulePool for wasmCID (creating it if
// absent), marks it most-recently-used, and LRU-evicts down to the module cap.
// lastUse is set BEFORE eviction so the just-touched pool is never the victim.
// Caller holds p.mu.
func (p *reactorPool) getOrCreatePoolLocked(wasmCID string) *modulePool {
	mp := p.pools[wasmCID]
	if mp == nil {
		mp = &modulePool{ready: make(chan api.Module, p.target)}
		p.pools[wasmCID] = mp
	}
	p.useSeq++
	mp.lastUse = p.useSeq
	if len(p.pools) > p.maxModules {
		p.evictLRULocked()
	}
	return mp
}

// InstantiateSync synchronously warms one instance — the empty-pool fallback —
// using the pool's unique naming. The caller owns and must Close it after the
// single handle call. It does NOT enter the ready channel (it is consumed
// immediately by the awaiting request).
func (p *reactorPool) InstantiateSync(ctx context.Context, wasmCID string) (api.Module, error) {
	p.mu.Lock()
	mp := p.getOrCreatePoolLocked(wasmCID)
	mp.nameSeq++
	name := fmt.Sprintf("reactor-%s-cold-%d", shortCID(wasmCID), mp.nameSeq)
	p.mu.Unlock()
	return p.warm(ctx, wasmCID, name)
}

// refill spawns warmer goroutines until ready+inflight reaches the target.
func (p *reactorPool) refill(wasmCID string, mp *modulePool) {
	p.mu.Lock()
	need := p.target - (len(mp.ready) + mp.inflight)
	if need < 1 {
		p.mu.Unlock()
		return
	}
	names := make([]string, 0, need)
	for i := 0; i < need; i++ {
		mp.nameSeq++
		names = append(names, fmt.Sprintf("reactor-%s-%d", shortCID(wasmCID), mp.nameSeq))
	}
	mp.inflight += need
	p.mu.Unlock()

	for _, name := range names {
		go p.warmOne(wasmCID, mp, name)
	}
}

func (p *reactorPool) warmOne(wasmCID string, mp *modulePool, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), reactorWarmTimeout)
	defer cancel()
	inst, err := p.warm(ctx, wasmCID, name)

	// Decide the instance's fate under the lock. The send must only happen if
	// this pool is STILL the live pool for wasmCID — otherwise it was torn down
	// (Invalidate / LRU-evict / Close / replaced) while we were warming, and
	// sending into its now-detached channel would leak the instance (nothing
	// would ever drain + Close it). In that case we close it here instead.
	stored := false
	p.mu.Lock()
	mp.inflight--
	if err == nil && p.pools[wasmCID] == mp {
		select {
		case mp.ready <- inst:
			stored = true
		default:
			// Pool already at target — fall through and close the surplus below.
		}
	}
	p.mu.Unlock()

	if err != nil {
		p.logger.Debug("reactor warm failed; falling back to cold instantiate on next acquire",
			zap.String("wasm_cid", shortCID(wasmCID)),
			zap.Error(err))
		return
	}
	if !stored {
		// Surplus, or the pool was torn down mid-warm — discard so the instance
		// is never reused and never leaks.
		_ = inst.Close(context.Background())
	}
}

// evictLRULocked drops the least-recently-used module pool while over the cap.
// Caller holds p.mu.
func (p *reactorPool) evictLRULocked() {
	for len(p.pools) > p.maxModules {
		var oldestKey string
		var oldest uint64
		first := true
		for k, mp := range p.pools {
			if first || mp.lastUse < oldest {
				oldestKey, oldest, first = k, mp.lastUse, false
			}
		}
		victim := p.pools[oldestKey]
		delete(p.pools, oldestKey)
		go p.drain(victim) // close warmed instances off the lock
	}
}

// Invalidate drops + closes all warmed instances for wasmCID. Call when a
// function is redeployed so a stale instance never serves new code. No-op if
// absent. (Redeploys also produce a new content-addressed WASMCID, so a stale
// pool is never *served*; this just reclaims it promptly instead of via LRU.)
func (p *reactorPool) Invalidate(wasmCID string) {
	p.mu.Lock()
	mp := p.pools[wasmCID]
	delete(p.pools, wasmCID)
	p.mu.Unlock()
	if mp != nil {
		p.drain(mp)
	}
}

func (p *reactorPool) drain(mp *modulePool) {
	for {
		select {
		case inst := <-mp.ready:
			_ = inst.Close(context.Background())
		default:
			return
		}
	}
}

// Close drains every pool (gateway shutdown).
func (p *reactorPool) Close() {
	p.mu.Lock()
	pools := p.pools
	p.pools = make(map[string]*modulePool)
	p.mu.Unlock()
	for _, mp := range pools {
		p.drain(mp)
	}
}

// shortCID trims a WASM CID for log/instance-name use.
func shortCID(cid string) string {
	if len(cid) > 12 {
		return cid[:12]
	}
	return cid
}
