package turn

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// TenantReloadInterval is how often the shared server re-reads its config file
// to pick up tenants that were added or removed. Enabling WebRTC advertises the
// relay once its host has written the tenant set, so this is the window in which
// a client's credentials can still be refused; it is kept short, and a tick
// costs the parse of one small file.
const TenantReloadInterval = 2 * time.Second

// tenantSet is an immutable snapshot of who this server serves. It is swapped
// wholesale under lock rather than mutated, so an in-flight authHandler always
// reads one internally-consistent set of tenants.
type tenantSet struct {
	tenants []TenantConfig
	// byHost maps a lower-cased stealth SNI hostname to its cert watcher.
	byHost map[string]*stealthCert
}

// stealthCert is one tenant's stealth certificate together with the stop channel
// for its reload watcher.
//
// Each tenant gets its OWN stop channel rather than sharing the server's: a
// tenant that disables stealth, or changes its cert paths, must have its watcher
// stopped when it leaves the tenant set. Sharing one channel would leave an
// orphan goroutine polling a file nobody serves any more — one leaked per
// stealth enable/disable, for the life of the node.
type stealthCert struct {
	reloader *certReloader
	stop     chan struct{}
	// once guards close(stop). A reload racing a shutdown can otherwise reach the
	// same channel twice — stopAllStealthWatchers closes prev's channels while an
	// in-flight reloadTenants still holds prev and then closes the ones its new
	// set did not carry forward. Closing a closed channel panics, and a panic at
	// shutdown hides whatever caused the shutdown.
	once sync.Once
}

// close stops this cert's watcher exactly once.
func (sc *stealthCert) close() {
	if sc == nil {
		return
	}
	sc.once.Do(func() { close(sc.stop) })
}

// secret resolves a namespace to its own auth secret. A miss is "not
// authorized" — never a reason to fall back to another tenant's secret.
func (t *tenantSet) secret(namespace string) (string, bool) {
	if t == nil {
		return "", false
	}
	for _, tc := range t.tenants {
		if tc.Namespace == namespace {
			if tc.AuthSecret == "" {
				return "", false
			}
			return tc.AuthSecret, true
		}
	}
	return "", false
}

// namespaces returns the served namespaces, for logging.
func (t *tenantSet) namespaces() []string {
	if t == nil {
		return nil
	}
	out := make([]string, 0, len(t.tenants))
	for _, tc := range t.tenants {
		out = append(out, tc.Namespace)
	}
	return out
}

// currentTenants returns the live snapshot.
func (s *Server) currentTenants() *tenantSet {
	s.tenantMu.RLock()
	defer s.tenantMu.RUnlock()
	return s.tenants
}

// tenantSecret is the authorization boundary for the shared server: it resolves
// a credential's namespace to that namespace's OWN secret, from the current
// snapshot.
func (s *Server) tenantSecret(namespace string) (string, bool) {
	return s.currentTenants().secret(namespace)
}

// stealthCertFor returns the cert reloader for a stealth SNI hostname.
func (s *Server) stealthCertFor(host string) (*certReloader, bool) {
	set := s.currentTenants()
	if set == nil || set.byHost == nil {
		return nil, false
	}
	sc, ok := set.byHost[strings.ToLower(host)]
	if !ok || sc == nil || sc.reloader == nil {
		return nil, false
	}
	return sc.reloader, true
}

// buildTenantSet turns a parsed config into a snapshot, reusing cert reloaders
// from prev for stealth hostnames that are unchanged.
//
// Reuse is not just an optimisation: a reloader holds the parsed certificate, so
// rebuilding one on every reload would re-read every tenant's cert from disk on
// a tick, and a transient read error would take a working tenant's stealth
// endpoint down. Only genuinely new hostnames are loaded.
func (s *Server) buildTenantSet(cfg *Config, prev *tenantSet) (*tenantSet, []*stealthCert, error) {
	tenants := cfg.ResolvedTenants()
	if len(tenants) == 0 {
		return nil, nil, fmt.Errorf("turn: config lists no tenants")
	}

	set := &tenantSet{tenants: tenants}
	var pending []*stealthCert
	for _, t := range tenants {
		if t.Namespace == "" {
			return nil, nil, fmt.Errorf("turn: tenant with an empty namespace")
		}
		if t.AuthSecret == "" {
			return nil, nil, fmt.Errorf("turn: tenant %q has no auth secret", t.Namespace)
		}

		set0, set1, set2 := t.StealthDomain != "", t.TLSStealthCertPath != "", t.TLSStealthKeyPath != ""
		n := 0
		for _, b := range []bool{set0, set1, set2} {
			if b {
				n++
			}
		}
		if n == 0 {
			continue // stealth disabled for this tenant
		}
		if n != stealthConfigFieldCount {
			return nil, nil, fmt.Errorf("turn: partial stealth config for tenant %q — set all of [stealth_domain, tls_stealth_cert_path, tls_stealth_key_path] or none", t.Namespace)
		}

		host := strings.ToLower(t.StealthDomain)

		// Two tenants claiming one stealth SNI host makes which one is served
		// order-dependent — the same hazard Validate refuses for namespaces. The
		// host is a truncated hash of the namespace, so a collision is not purely
		// accidental: on a shared server it is a way to contend for another
		// tenant's censorship-resistant identity. Refuse the later claimant.
		if _, dup := set.byHost[host]; dup {
			s.logger.Error("Stealth host claimed by more than one tenant; serving this tenant without stealth",
				zap.String("namespace", t.Namespace), zap.String("stealth_host", host))
			continue
		}

		if prev != nil {
			if sc, ok := prev.byHost[host]; ok && sc != nil && sc.reloader.samePaths(t.TLSStealthCertPath, t.TLSStealthKeyPath) {
				if set.byHost == nil {
					set.byHost = make(map[string]*stealthCert)
				}
				set.byHost[host] = sc // carries its watcher forward, still running
				continue
			}
		}
		r, err := newCertReloader(t.TLSStealthCertPath, t.TLSStealthKeyPath, s.logger)
		if err != nil {
			// Scope a stealth failure to the tenant, never to the host. Failing the
			// whole set here would freeze every OTHER tenant's membership —
			// including revocations — and at startup would crash-loop the shared
			// server, taking TURN away from every namespace on the node. A
			// stat-able but unloadable pair is a real state: Caddy renewals write
			// cert and key separately.
			s.logger.Error("Tenant stealth cert unloadable; serving this tenant without stealth",
				zap.String("namespace", t.Namespace),
				zap.String("cert_path", t.TLSStealthCertPath), zap.Error(err))
			continue
		}
		if set.byHost == nil {
			set.byHost = make(map[string]*stealthCert)
		}
		// Each tenant's stealth cert renews independently, so it needs its own
		// watcher. The watcher is NOT started here: buildTenantSet can still fail
		// after this point, and a goroutine started for a set that is never
		// installed is never referenced again — one leak per failed reload, every
		// tick, forever. startPendingWatchers runs once the set is live.
		set.byHost[host] = &stealthCert{reloader: r, stop: make(chan struct{})}
		pending = append(pending, set.byHost[host])
	}
	return set, pending, nil
}

// startPendingWatchers starts cert watchers for stealth certs newly created by
// buildTenantSet. Called only after the set is installed, so a build that fails
// partway leaks nothing.
func startPendingWatchers(pending []*stealthCert) {
	for _, sc := range pending {
		go sc.reloader.watch(turnCertReloadInterval, sc.stop)
	}
}

// stopDroppedStealthWatchers stops the watchers for stealth certs that prev held
// and next does not carry forward. Without this every tenant removal or cert-path
// change leaks a goroutine polling a file nothing serves.
func stopDroppedStealthWatchers(prev, next *tenantSet) {
	if prev == nil {
		return
	}
	for host, sc := range prev.byHost {
		if sc == nil {
			continue
		}
		if next != nil {
			if kept, ok := next.byHost[host]; ok && kept == sc {
				continue // same watcher carried forward
			}
		}
		sc.close()
	}
}

// stopAllStealthWatchers stops every watcher in the current set, on shutdown.
func (s *Server) stopAllStealthWatchers() {
	s.tenantMu.Lock()
	defer s.tenantMu.Unlock()
	if s.tenants == nil {
		return
	}
	for _, sc := range s.tenants.byHost {
		sc.close()
	}
	s.tenants = &tenantSet{tenants: s.tenants.tenants}
}

// reloadTenants re-reads the config file and swaps in the new tenant set.
//
// This is what makes a shared TURN server viable at all. Tenants change whenever
// any namespace enables or disables WebRTC, and restarting the process to pick
// that up would drop every OTHER tenant's live relays — turning a routine
// per-namespace operation into a host-wide call drop. Only the tenant set is
// reloaded; listeners, ports and the relay range are fixed for the process
// lifetime and a change to those still requires a restart.
func (s *Server) reloadTenants(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read TURN config %s: %w", path, err)
	}
	return s.applyTenantConfig(data)
}

// applyTenantConfig swaps in the tenant set that config bytes describe.
func (s *Server) applyTenantConfig(data []byte) error {
	cfg, err := ParseConfig(data)
	if err != nil {
		return err
	}
	// The reload path is an ingest path, so it gets the same guard as startup.
	// Without this, a config listing one namespace twice would silently authorize
	// it with whichever secret happens to come first in the slice — exactly what
	// Validate refuses to let happen at startup.
	if errs := cfg.Validate(); len(errs) > 0 {
		return fmt.Errorf("invalid TURN config on reload: %v", errs[0])
	}

	s.tenantMu.RLock()
	prev := s.tenants
	s.tenantMu.RUnlock()

	next, pending, err := s.buildTenantSet(cfg, prev)
	if err != nil {
		return err
	}

	s.tenantMu.Lock()
	s.tenants = next
	s.tenantMu.Unlock()

	startPendingWatchers(pending)
	stopDroppedStealthWatchers(prev, next)

	// Publish what is now live, so a host can confirm the running server — not
	// just the file it wrote — serves a namespace.
	if s.servedPath == "" {
		return nil
	}
	if err := writeServedTenants(s.servedPath, ServedTenants{Namespaces: next.namespaces(), ConfigSHA256: ConfigDigest(data)}); err != nil {
		return fmt.Errorf("tenant set loaded but not published: %w", err)
	}
	return nil
}

// reloadState is what the watcher remembers between ticks.
type reloadState struct {
	digest  string
	lastErr string
}

// tick runs one watcher iteration. The file is read and hashed every tick (it is
// small) and the tenant set is rebuilt only when the content changed: mtime and
// size cannot be trusted, since a same-size rewrite within the filesystem's
// timestamp granularity looks untouched, and a rotated secret would go unseen.
// A failure is logged once per distinct error: the watcher ticks every few
// seconds forever, so a config that stays broken would otherwise fill the
// journal. After a failure every tick retries.
func (s *Server) tick(path string, st *reloadState) {
	data, err := os.ReadFile(path)
	if err != nil {
		st.digest = ""
		s.logReloadFailure(path, st, fmt.Errorf("read TURN config %s: %w", path, err))
		return
	}
	digest := ConfigDigest(data)
	if digest == st.digest && st.lastErr == "" {
		return
	}

	before := s.currentTenants().namespaces()
	if err := s.applyTenantConfig(data); err != nil {
		st.digest = ""
		s.logReloadFailure(path, st, err)
		return
	}
	st.digest, st.lastErr = digest, ""
	after := s.currentTenants().namespaces()
	if !sameStringSet(before, after) {
		s.logger.Info("TURN tenant set reloaded",
			zap.Strings("before", before),
			zap.Strings("after", after))
	}
}

func (s *Server) logReloadFailure(path string, st *reloadState, err error) {
	if msg := err.Error(); msg != st.lastErr {
		st.lastErr = msg
		s.logger.Warn("TURN tenant reload failed",
			zap.String("config_path", path), zap.Error(err))
	}
}

// watchTenants polls the config file so tenant changes take effect without a
// restart. A failed reload keeps the previous set: a half-written or briefly
// unreadable config must never revoke tenants that are currently working.
func (s *Server) watchTenants(path string, interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var st reloadState
	// Load once now so the live set is published as soon as the watcher starts,
	// not a tick later.
	s.tick(path, &st)
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.tick(path, &st)
		}
	}
}

// sameStringSet reports whether a and b hold the same elements, ignoring order.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

// WatchTenantConfig starts polling path for tenant changes, so namespaces can be
// added to or removed from this shared server without restarting it.
//
// Callers pass the same path the config was loaded from, and the path the
// served tenant set is published to (empty to publish nothing). Safe to call once, after
// NewServer; the watcher stops when the server is closed.
func (s *Server) WatchTenantConfig(path, servedPath string) {
	if path == "" {
		return
	}
	// A file left by an earlier process must not be read as this one's: remove
	// it before the first load.
	if servedPath != "" {
		if err := os.Remove(servedPath); err != nil && !os.IsNotExist(err) {
			s.logger.Error("Could not remove the served-tenants file of an earlier process; hosts may read it as this server's",
				zap.String("path", servedPath), zap.Error(err))
		}
	}
	s.servedPath = servedPath
	if s.certStop == nil {
		// TURNS disabled, so no stop channel was created by the TLS block.
		s.certStop = make(chan struct{})
	}
	go s.watchTenants(path, TenantReloadInterval, s.certStop)
}
