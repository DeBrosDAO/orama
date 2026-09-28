// Package pool is the public shielded-pool accounting: vintage balances,
// turnstiles, and the 24h unshield cap. Proofs are not checked here.
package pool

import (
	"errors"
	"time"

	"cosmossdk.io/math"
)

const (
	// UnshieldNumerator and UnshieldDenominator are the ossified 2% cap
	// (plans/open-network.md P5).
	UnshieldNumerator   int64 = 2
	UnshieldDenominator int64 = 100
	// UnshieldFloor is the minimum 24h outflow when 2% of the pool is smaller
	// than one ORAMA. Amounts are norama.
	UnshieldFloor int64 = 1_000_000_000
	// Window is the rolling cap period.
	Window = 24 * time.Hour
)

// NativeAsset is the norama asset id. Multi-asset notes stay inert until
// activation. The id is the SHA-256 label, fixed so every node agrees.
var NativeAsset = [32]byte{
	0x6e, 0x6f, 0x72, 0x61, 0x6d, 0x61, // "norama" then zeros
}

var (
	ErrUnderflow          = errors.New("turnstile balance would go negative")
	ErrAmount             = errors.New("amount must be positive")
	ErrVintage            = errors.New("vintage migration must move to a newer pool of the same asset")
	ErrMultiAssetInactive = errors.New("multi-asset shielding is not active")
	ErrTokenPowers        = errors.New("token with freeze, permanent delegate, or pause cannot be shielded")
	ErrCapExhausted       = errors.New("unshield cap exhausted")
)

// Key identifies one vintage pool for one asset.
type Key struct {
	Vintage uint32
	Asset   [32]byte
}

// Pools is the public per-vintage, per-asset balance. It never goes negative.
type Pools struct {
	bal map[Key]math.Int
}

// NewPools returns an empty set of pools.
func NewPools() *Pools {
	return &Pools{bal: make(map[Key]math.Int)}
}

// Balance returns the public balance, or zero.
func (p *Pools) Balance(k Key) math.Int {
	v, ok := p.bal[k]
	if !ok {
		return math.ZeroInt()
	}
	return v
}

// Add credits a pool. Shielding in and turnstile inflow use this.
func (p *Pools) Add(k Key, amount math.Int) error {
	if !amount.IsPositive() {
		return ErrAmount
	}
	p.bal[k] = p.Balance(k).Add(amount)
	return nil
}

// Sub debits a pool. Underflow is refused. There is no force path.
func (p *Pools) Sub(k Key, amount math.Int) error {
	if !amount.IsPositive() {
		return ErrAmount
	}
	cur := p.Balance(k)
	if cur.LT(amount) {
		return ErrUnderflow
	}
	p.bal[k] = cur.Sub(amount)
	return nil
}

// Move sends amount from an older vintage to a newer one of the same asset.
func (p *Pools) Move(from, to Key, amount math.Int) error {
	if from.Asset != to.Asset || to.Vintage <= from.Vintage {
		return ErrVintage
	}
	if err := p.Sub(from, amount); err != nil {
		return err
	}
	return p.Add(to, amount)
}

// AllowAsset refuses a non-native asset until multi-asset activation.
func AllowAsset(multiAsset bool, asset [32]byte) error {
	if !multiAsset && asset != NativeAsset {
		return ErrMultiAssetInactive
	}
	return nil
}

// AllowToken refuses a token that still has freeze, permanent-delegate, or
// pause authority.
func AllowToken(freeze, permanentDelegate, pause bool) error {
	if freeze || permanentDelegate || pause {
		return ErrTokenPowers
	}
	return nil
}

// CapAmount is max(2% of pool, floor).
func CapAmount(poolBalance, floor math.Int) math.Int {
	pct := poolBalance.MulRaw(UnshieldNumerator).QuoRaw(UnshieldDenominator)
	if pct.LT(floor) {
		return floor
	}
	return pct
}

// Kind classifies an outflow for the cap.
type Kind uint8

const (
	// KindFeeBurn leaves the pool and does not count against the cap.
	KindFeeBurn Kind = iota
	// KindAdapter is a contract or adapter unshield. Over the cap it fails.
	KindAdapter
	// KindFeeTopup counts against the cap and fails when the cap is exhausted.
	KindFeeTopup
	// KindBond queues when it does not fit.
	KindBond
	// KindDeposit queues when it does not fit.
	KindDeposit
)

// Outcome is what the limiter decided.
type Outcome uint8

const (
	OutcomeAllow Outcome = iota
	OutcomeQueue
	OutcomeReject
)

// Limiter is the rolling 24h net-outflow counter for one pool.
type Limiter struct {
	Start   time.Time
	Counted math.Int
}

// Apply updates the limiter. Fee burns are exempt. Adapter and fee top-ups
// fail atomically when they do not fit. Bond and deposit outflows queue.
func (l *Limiter) Apply(now time.Time, poolBalance, floor, amount math.Int, kind Kind) (Outcome, error) {
	if !amount.IsPositive() {
		return OutcomeReject, ErrAmount
	}
	if l.Counted.IsNil() {
		l.Counted = math.ZeroInt()
	}
	if l.Start.IsZero() || !now.Before(l.Start.Add(Window)) {
		l.Start = now
		l.Counted = math.ZeroInt()
	}
	if kind == KindFeeBurn {
		return OutcomeAllow, nil
	}
	cap := CapAmount(poolBalance, floor)
	if l.Counted.Add(amount).GT(cap) {
		if kind == KindBond || kind == KindDeposit {
			return OutcomeQueue, nil
		}
		return OutcomeReject, ErrCapExhausted
	}
	l.Counted = l.Counted.Add(amount)
	return OutcomeAllow, nil
}
