package sniproxy

import (
	"strings"
	"time"

	"go.uber.org/zap"
)

// discoveryWarnInterval rate-limits the "discovery scan failed" warning so a
// persistently-unreadable namespaces directory cannot flood the journal.
const discoveryWarnInterval = 5 * time.Minute

// StaticRoutes returns the operator-set routes parsed from the SNI router's own
// config file plus the fallback backend. The discoverer merges these with the
// auto-discovered TURN routes; static routes win on an SNI conflict.
type StaticRoutes func() (routes []Route, fallback Backend, err error)

// TURNRouteDiscoverer periodically rescans the namespaces directory for
// per-namespace TURNS listeners, merges the discovered routes with the static
// routes from the config file (static wins on conflict), and atomically
// installs the result on the Router.
//
// A transient failure (unreadable namespaces dir, or a bad static-config read)
// logs a rate-limited warning and KEEPS the previously-installed routes — a
// filesystem hiccup must never blackhole live :443 traffic.
type TURNRouteDiscoverer struct {
	cfg    TURNDiscoveryConfig
	static StaticRoutes
	router *Router
	logger *zap.Logger

	// lastWarn is only touched by the Run goroutine after the synchronous
	// startup Apply, so it needs no lock.
	lastWarn time.Time
}

// NewTURNRouteDiscoverer constructs a discoverer. static reads the operator's
// config-file routes + fallback; router receives the merged Replace calls.
func NewTURNRouteDiscoverer(cfg TURNDiscoveryConfig, static StaticRoutes, router *Router, logger *zap.Logger) *TURNRouteDiscoverer {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &TURNRouteDiscoverer{cfg: cfg, static: static, router: router, logger: logger}
}

// Apply performs one scan+merge and installs the result atomically. On any
// transient error it returns the error and leaves the Router untouched so the
// caller can decide whether to fail startup (Apply) or keep stale routes (Run).
func (d *TURNRouteDiscoverer) Apply() error {
	staticRoutes, fallback, err := d.static()
	if err != nil {
		return err
	}

	discovered, err := DiscoverTURNRoutes(d.cfg, d.logger)
	if err != nil {
		return err
	}

	merged := mergeRoutes(staticRoutes, discovered)
	d.router.Replace(merged, fallback)
	return nil
}

// Run scans immediately, then every rescan interval until stop is closed. A
// failed scan keeps the current routes and logs a rate-limited warning.
func (d *TURNRouteDiscoverer) Run(stop <-chan struct{}) {
	if err := d.Apply(); err != nil {
		d.warn("initial TURN route discovery failed; serving config-file routes only", err)
	}

	interval := d.cfg.RescanInterval
	if interval <= 0 {
		interval = DefaultDiscoveryRescanInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if err := d.Apply(); err != nil {
				d.warn("TURN route discovery failed; keeping current routes", err)
				continue
			}
		}
	}
}

// warn logs at most once per discoveryWarnInterval to avoid journal flooding
// when the namespaces directory is persistently unreadable.
func (d *TURNRouteDiscoverer) warn(msg string, err error) {
	now := time.Now()
	if now.Sub(d.lastWarn) < discoveryWarnInterval {
		return
	}
	d.lastWarn = now
	d.logger.Warn(msg,
		zap.String("namespaces_dir", d.cfg.NamespacesDir),
		zap.Error(err))
}

// mergeRoutes combines static and discovered routes with static taking
// precedence on an SNI-match conflict. Static routes keep their original order
// and precede discovered ones, matching Router.Pick's first-match semantics.
func mergeRoutes(static, discovered []Route) []Route {
	seen := make(map[string]struct{}, len(static))
	merged := make([]Route, 0, len(static)+len(discovered))
	for _, r := range static {
		seen[matchKey(r.Match)] = struct{}{}
		merged = append(merged, r)
	}
	for _, r := range discovered {
		if _, conflict := seen[matchKey(r.Match)]; conflict {
			continue // static wins
		}
		merged = append(merged, r)
	}
	return merged
}

// matchKey normalizes an SNI match for conflict comparison (matching is
// case-insensitive, mirroring Router.Pick / matchSNI).
func matchKey(match string) string {
	return strings.ToLower(match)
}
