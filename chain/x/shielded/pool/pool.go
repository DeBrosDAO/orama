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
	// Window is the period over which the cap drains back.
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

// WindowSeconds is Window in whole seconds, the unit the limiter counts time in.
const WindowSeconds = int64(Window / time.Second)

// Capacity is what the bucket holds under cap: half of it, rounded down. The bucket drains its
// capacity per Window, so an interval of length T at most Window admits at most
// Capacity + Capacity*T/Window <= cap: never more than one cap leaves in any 24 hours. The
// sustained rate is half a cap per Window.
func Capacity(cap math.Int) math.Int { return cap.QuoRaw(2) }

// Limiter is the net-outflow counter for one pool: a leaky bucket that holds Capacity(cap) and
// drains that much per Window. Counted is the level and Start is the instant the drain has been
// accounted up to, so the bucket never resets all at once.
type Limiter struct {
	Start   time.Time
	Counted math.Int
}

// Roll drains the bucket for the time since Start at Capacity(cap) per Window, in whole norama,
// rounded down, so it never drains more than the exact rate. Start moves
// forward only by the time the drained norama stand for, rounded up, so a short interval that
// drains nothing is kept and added to the next one; a bucket that drains empty restarts at now.
func (l *Limiter) Roll(now time.Time, cap math.Int) {
	rate := Capacity(cap)
	if l.Counted.IsNil() {
		l.Counted = math.ZeroInt()
	}
	if l.Start.IsZero() || !l.Counted.IsPositive() {
		l.Start = now
		return
	}
	elapsed := now.Unix() - l.Start.Unix()
	if elapsed <= 0 || !rate.IsPositive() {
		return
	}
	drained := rate.MulRaw(elapsed).QuoRaw(WindowSeconds)
	if !drained.IsPositive() {
		return
	}
	if drained.GTE(l.Counted) {
		l.Counted = math.ZeroInt()
		l.Start = now
		return
	}
	used := drained.MulRaw(WindowSeconds).Add(rate).SubRaw(1).Quo(rate) // ceil(drained * Window / rate)
	l.Counted = l.Counted.Sub(drained)
	l.Start = l.Start.Add(time.Duration(used.Int64()) * time.Second)
}

// Remaining is what the bucket still takes under cap right now; call Roll first. It is never
// negative, even when the cap fell below the level because the pool shrank.
func (l *Limiter) Remaining(cap math.Int) math.Int {
	left := Capacity(cap).Sub(l.Counted)
	if left.IsNegative() {
		return math.ZeroInt()
	}
	return left
}

// Apply updates the limiter. Fee burns are exempt. Adapter and fee top-ups
// fail atomically when they do not fit. Bond and deposit outflows queue.
func (l *Limiter) Apply(now time.Time, poolBalance, floor, amount math.Int, kind Kind) (Outcome, error) {
	if !amount.IsPositive() {
		return OutcomeReject, ErrAmount
	}
	cap := CapAmount(poolBalance, floor)
	l.Roll(now, cap)
	if kind == KindFeeBurn {
		return OutcomeAllow, nil
	}
	if amount.GT(l.Remaining(cap)) {
		if kind == KindBond || kind == KindDeposit {
			return OutcomeQueue, nil
		}
		return OutcomeReject, ErrCapExhausted
	}
	l.Counted = l.Counted.Add(amount)
	return OutcomeAllow, nil
}
