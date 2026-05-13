// Package ratelimit provides per-namespace rate-limit configuration storage
// and a Manager that builds per-namespace token-bucket limiters from that
// configuration (with a fallback to gateway-wide defaults).
//
// Feature #69. Mirrors the per-namespace push-config pattern from bug
// #220's follow-up: tenants self-serve their own quota via authenticated
// HTTP, and operators retain a hard cap so no tenant can raise their own
// limit beyond the global ceiling.
package ratelimit

import (
	"context"
	"fmt"
)

// Config is one row of `namespace_rate_limit_config`. A tenant's override
// of the gateway's default rate limits.
//
// IMPORTANT: per-gateway-bucket semantics. The values here apply to ONE
// gateway's token bucket. In an N-gateway deployment the effective
// cluster-wide rate cap for the namespace is N × RequestsPerMinute (and
// N × Burst), because each gateway maintains its own bucket. Operators
// who need a cluster-wide cap must either set the per-gateway value to
// (cluster-cap / N) or implement a shared-bucket backend. The GET
// handler surfaces this caveat in the response so tenants understand
// what they're setting.
type Config struct {
	Namespace         string
	RequestsPerMinute int
	Burst             int
	UpdatedAt         int64  // unix seconds
	UpdatedBy         string // free-form audit (wallet address, operator ID, etc.)
}

// Defaults are the gateway-YAML fallback when a namespace hasn't set its
// own config. These also serve as the OPERATOR CEILING — tenant PUT
// requests with values greater than MaxRequestsPerMinute / MaxBurst are
// rejected at the handler boundary. A tenant can request looser limits
// only up to (but not beyond) the cap.
//
// Setting Max* to 0 means "no cap; trust tenant input". Use with care in
// shared-infrastructure deployments.
type Defaults struct {
	RequestsPerMinute    int
	Burst                int
	MaxRequestsPerMinute int
	MaxBurst             int
}

// Sane returns a copy with any nonsensical values clamped to safe
// fallbacks. A Defaults with zero rate/burst would let every request
// through unconditionally; we treat that as misconfiguration and fall
// back to a reasonable cluster-friendly baseline.
//
// Max* values are NOT clamped: a value of zero (the zero-value) is
// meaningful — it disables the ceiling check, letting tenants set any
// value they want. Operators who want to disable the cap explicitly set
// 0. A negative value here is treated identically to 0 (disabled),
// since the cap-check in the handler uses `> 0` for "active".
func (d Defaults) Sane() Defaults {
	out := d
	if out.RequestsPerMinute <= 0 {
		out.RequestsPerMinute = 10_000
	}
	if out.Burst <= 0 {
		out.Burst = 5_000
	}
	// Normalise negatives to 0 so handler.go's `> 0` check has clean
	// semantics regardless of operator typo.
	if out.MaxRequestsPerMinute < 0 {
		out.MaxRequestsPerMinute = 0
	}
	if out.MaxBurst < 0 {
		out.MaxBurst = 0
	}
	return out
}

// ConfigStore reads and writes per-namespace rate-limit overrides.
// Implementations are usually RQLite-backed (see rqlite_store.go) but
// the interface lets tests swap in an in-memory map.
type ConfigStore interface {
	// Get returns the namespace's override, or (nil, nil) if no override
	// has been set (caller should fall back to Defaults).
	Get(ctx context.Context, namespace string) (*Config, error)

	// Upsert inserts or replaces the override for cfg.Namespace.
	// cfg.UpdatedAt and cfg.UpdatedBy must be populated by the caller.
	Upsert(ctx context.Context, cfg Config) error

	// Delete removes the override (caller falls back to Defaults).
	// No error if the row didn't exist — idempotent.
	Delete(ctx context.Context, namespace string) error
}

// ErrAboveOperatorCap is returned by the config handler when a PUT request
// would set a value above the operator-configured Defaults.Max* ceiling.
// Surfaced as 400 to the tenant with the cap value, so they know what the
// platform allows.
var ErrAboveOperatorCap = fmt.Errorf("requested rate limit exceeds operator-configured maximum")
