// Package pace keeps a whole fleet run under the gateway's credential rate
// limits. The gateway allows 30 credential operations a minute (burst 10) per
// client address, and 10 challenges a minute (burst 5) per wallet. The
// per-address limiter is the edge's: it runs on the cluster gateway of the
// node that takes the request, whichever host the request names (the
// cluster's own, a namespace's ns-<name>, an app's), because a namespace
// gateway only sees the overlay, which is exempt (core/pkg/gateway/
// rate_limit_key.go, package clientkey). The runner is one address running
// many packages and many namespaces at once, and DNS decides which node
// answers it, so without pacing every sign-in would race for the same budget
// and fail with 429 RATE_LIMITED for a reason that is not a bug.
//
// A Pacer is a token bucket per bucket name (the address, or one wallet's
// challenges) for the whole run, whatever host a call goes to: any call may
// land on any node, so the budget of one node's limiter is what the whole run
// may spend. Its state lives in one small file in the run's work dir, beside
// state.json, guarded by flock: every feature process and every CLI
// invocation of the run draws from the same buckets. The budgets sit slightly
// under the product's limits, so a 429 on a paced call means something real
// (an unpaced caller on the same address, or a limiter regression), never
// contention inside the harness.
package pace

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
)

// Environment variables that override the budgets.
const (
	EnvCredPerMin      = "E2E_PACE_CRED_PER_MIN"
	EnvCredBurst       = "E2E_PACE_CRED_BURST"
	EnvChallengePerMin = "E2E_PACE_CHALLENGE_PER_MIN"
	EnvChallengeBurst  = "E2E_PACE_CHALLENGE_BURST"
)

// Product limits: core/pkg/gateway/gateway.go configureRateLimiters (per
// address) and core/pkg/gateway/handlers/auth/wallet_rate_limit.go (per
// wallet). A budget above them would let the harness itself trip the limiter.
const (
	ProductCredPerMin      = 30
	ProductCredBurst       = 10
	ProductChallengePerMin = 10
	ProductChallengeBurst  = 5
)

// Default budgets, slightly under the product limits.
const (
	DefaultCredPerMin      = 24
	DefaultCredBurst       = 8
	DefaultChallengePerMin = 8
	DefaultChallengeBurst  = 4
)

// FileName is the pacing state, in the run's work dir beside state.json.
const FileName = "pace-state.json"

// BucketCred is the per-address credential bucket (challenge, verify,
// api-key, token, refresh and the device endpoints) of the whole run.
const BucketCred = "cred"

// challengePrefix starts every per-wallet challenge bucket.
const challengePrefix = "challenge:"

// ChallengeBucket is the per-wallet challenge bucket of wallet.
func ChallengeBucket(wallet string) string {
	return challengePrefix + strings.ToLower(strings.TrimSpace(wallet))
}

// Budget is one bucket's refill rate and capacity.
type Budget struct {
	PerMinute int
	Burst     int
}

// Budgets holds the budget of each bucket kind.
type Budgets struct {
	Cred      Budget
	Challenge Budget
}

// DefaultBudgets are the budgets when no variable overrides them.
func DefaultBudgets() Budgets {
	return Budgets{
		Cred:      Budget{PerMinute: DefaultCredPerMin, Burst: DefaultCredBurst},
		Challenge: Budget{PerMinute: DefaultChallengePerMin, Burst: DefaultChallengeBurst},
	}
}

// BudgetsFromEnv reads the budgets through lookup. Each value must be a
// positive integer no larger than the product limit it paces.
func BudgetsFromEnv(lookup func(string) (string, bool)) (Budgets, error) {
	b := DefaultBudgets()
	fields := []struct {
		name  string
		dst   *int
		limit int
	}{
		{EnvCredPerMin, &b.Cred.PerMinute, ProductCredPerMin},
		{EnvCredBurst, &b.Cred.Burst, ProductCredBurst},
		{EnvChallengePerMin, &b.Challenge.PerMinute, ProductChallengePerMin},
		{EnvChallengeBurst, &b.Challenge.Burst, ProductChallengeBurst},
	}
	for _, f := range fields {
		v, ok := lookup(f.name)
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 || n > f.limit {
			return Budgets{}, fmt.Errorf("%s=%q must be an integer from 1 to %d (the product limit it paces)", f.name, v, f.limit)
		}
		*f.dst = n
	}
	return b, nil
}

// Pacer draws tokens from the run's shared buckets. A nil *Pacer paces
// nothing: Wait and Charge return at once (unit tests, runs by hand without a
// fleet).
type Pacer struct {
	path    string
	budgets Budgets
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
}

// New returns a pacer whose state lives at path (absolute).
func New(path string, b Budgets) (*Pacer, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("pacing state path %q must be absolute", path)
	}
	for name, bud := range map[string]Budget{"credential": b.Cred, "challenge": b.Challenge} {
		if bud.PerMinute < 1 || bud.Burst < 1 {
			return nil, fmt.Errorf("the %s budget %+v needs a positive rate and burst", name, bud)
		}
	}
	return &Pacer{path: path, budgets: b, now: time.Now, sleep: timerSleep}, nil
}

// WithClock returns a copy of p on an injected clock: now reads it, sleep
// waits on it (unit tests advance a fake clock in sleep).
func (p *Pacer) WithClock(now func() time.Time, sleep func(context.Context, time.Duration) error) *Pacer {
	cp := *p
	cp.now, cp.sleep = now, sleep
	return &cp
}

// Path is the pacing state of the run whose state file is statePath.
func Path(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), FileName)
}

// FromEnv is the run's pacer as a feature process (or the runner) sees it:
// the state file beside E2E_FLEET_STATE and the budgets from the environment.
// Outside a fleet run (E2E_FLEET_STATE unset) it is nil: nothing to pace.
func FromEnv(lookup func(string) (string, bool)) (*Pacer, error) {
	mode, err := config.FromEnv(lookup)
	if err != nil {
		return nil, err
	}
	if mode.StatePath == "" {
		return nil, nil
	}
	b, err := BudgetsFromEnv(lookup)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(mode.StatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to make %s=%s absolute: %w", config.EnvState, mode.StatePath, err)
	}
	return New(Path(abs), b)
}

// Budgets returns the pacer's budgets.
func (p *Pacer) Budgets() Budgets { return p.budgets }

// budgetFor is the budget of bucket, or an error for an unknown bucket.
func (p *Pacer) budgetFor(bucket string) (Budget, error) {
	switch {
	case bucket == BucketCred:
		return p.budgets.Cred, nil
	case strings.HasPrefix(bucket, challengePrefix) && len(bucket) > len(challengePrefix):
		return p.budgets.Challenge, nil
	}
	return Budget{}, fmt.Errorf("unknown pacing bucket %q: use pace.BucketCred or pace.ChallengeBucket(wallet)", bucket)
}

// timerSleep waits d or until ctx ends. Pacing is a budget over time, not a
// readiness signal to poll, so this is a plain timer wait (the lint allows
// timers in the harness only, never in feature packages).
func timerSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
