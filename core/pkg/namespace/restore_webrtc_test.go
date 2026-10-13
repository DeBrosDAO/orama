package namespace

import (
	"errors"
	"testing"
	"time"
)

// Bugboard #25 — WebRTC config drift on restart + TURN/SFU decouple.
// Bugboard #130 follow-up — DB-FIRST resolution so a stale cached secret can
// never be served indefinitely.
//
// chooseRestoreWebRTC resolves a restored gateway's WebRTC config DB-FIRST
// (the namespace_webrtc_config row is the source of truth for the current
// secret); the local cluster-state.json cache is a FALLBACK consulted only
// when the DB read fails (a slow node whose namespace rqlite hasn't synced).
// It also DECOUPLES the two aspects: TURN (secret + domain) is namespace-wide
// so ANY gateway can serve credentials; the SFU port is per-node (0 on a
// gateway-only node). Pins the drift fallback, the non-SFU-gateway case, and
// the DB-first precedence (DB secret wins over a cached/stale one).

// dbFetch signature: () -> (turnSecret, turnDomain, stealthDomain string, sfuPort int, resolved bool).
// resolved=true means the lookup completed (with or without a config);
// resolved=false means it ERRORED (e.g. decrypt failure) → unresolved.
func dbNone() (string, string, string, int, bool) { return "", "", "", 0, true }

// dbError models a DB/decrypt failure: the lookup did not complete.
func dbError() (string, string, string, int, bool) { return "", "", "", 0, false }

func dbFull(secret, domain string, sfuPort int) func() (string, string, string, int, bool) {
	return func() (string, string, string, int, bool) { return secret, domain, "", sfuPort, true }
}

func TestChooseRestoreWebRTC_dbSecretWinsOverCachedState(t *testing.T) {
	// THE #130 FOLLOW-UP (staleness) case. The state file holds a cached
	// secret, but the DB (source of truth) has a DIFFERENT, current secret —
	// e.g. the secret was rotated (disable→enable) while this node was offline.
	// DB-first MUST serve the current DB secret, NOT the stale cached one. The
	// old state-first logic short-circuited the DB here and served "old-secret"
	// indefinitely.
	got := chooseRestoreWebRTC(true, 7800, "turn.ns-x.dbrs.space", "old-secret", "cdn-old.dbrs.space",
		dbFull("new-secret", "turn.ns-x.dbrs.space", 7800))

	if !got.enabled {
		t.Fatal("DB has a current secret; result must be enabled")
	}
	if got.turnSecret != "new-secret" {
		t.Errorf("BUG #130 STALENESS: turnSecret = %q; want new-secret (the current DB value, not the stale cache)", got.turnSecret)
	}
	if got.sfuPort != 7800 || got.turnDomain != "turn.ns-x.dbrs.space" {
		t.Errorf("want DB-derived block; got %+v", got)
	}
}

func TestChooseRestoreWebRTC_dbDisabledOverridesCachedSecret(t *testing.T) {
	// The cache holds a secret but the DB read completes and reports NO WebRTC
	// (the namespace was disabled while this node was offline). DB-first must
	// honor the disable, NOT keep serving the stale cached secret.
	got := chooseRestoreWebRTC(true, 7800, "turn.ns-x.dbrs.space", "stale-secret", "",
		dbNone) // dbNone = resolved, no config

	if got.enabled {
		t.Errorf("DB reports disabled: must not keep serving the cached secret; got %+v", got)
	}
	if got.unresolved {
		t.Error("a clean resolved-but-disabled lookup must not be marked unresolved")
	}
}

func TestChooseRestoreWebRTC_staleStateFallsBackToDB(t *testing.T) {
	// The bug-25 drift case: state file has NO webrtc (stale — written
	// before enable), DB says enabled WITH an SFU port on this node. MUST
	// fall back to the DB and re-materialize the full block.
	got := chooseRestoreWebRTC(false, 0, "", "", "",
		dbFull("db-secret", "turn.ns-anchat-test.dbrs.space", 7801))

	if !got.enabled {
		t.Fatal("BUG #25 REGRESSION: stale state + DB-enabled WebRTC must fall back to DB; got disabled")
	}
	if got.sfuPort != 7801 {
		t.Errorf("sfuPort = %d; want 7801 (from DB)", got.sfuPort)
	}
	if got.turnSecret != "db-secret" {
		t.Errorf("turnSecret = %q; want db-secret (from DB)", got.turnSecret)
	}
	if got.turnDomain != "turn.ns-anchat-test.dbrs.space" {
		t.Errorf("turnDomain = %q; want DB-derived value", got.turnDomain)
	}
}

func TestChooseRestoreWebRTC_nonSFUGatewayGetsTURNOnly(t *testing.T) {
	// THE DECOUPLE CASE (bug-25). A gateway node that is NOT an SFU node:
	// the DB has the namespace TURN secret but GetSFUPorts returns nothing
	// for this node (sfuPort=0). The gateway MUST still get the TURN
	// secret (so /v1/webrtc/turn/credentials registers + works) while
	// sfuPort stays 0 (signal/rooms don't register). This is exactly node
	// 57's situation — pre-fix it resolved to disabled and 404'd.
	got := chooseRestoreWebRTC(false, 0, "", "", "",
		dbFull("db-secret", "turn.ns-anchat-test.dbrs.space", 0)) // sfuPort 0 = no local SFU

	if !got.enabled {
		t.Fatal("BUG #25 REGRESSION: non-SFU gateway with namespace TURN secret must be enabled (serves credentials)")
	}
	if got.sfuPort != 0 {
		t.Errorf("sfuPort = %d; want 0 (this node runs no local SFU)", got.sfuPort)
	}
	if got.turnSecret != "db-secret" {
		t.Errorf("turnSecret = %q; want db-secret (TURN is namespace-wide, served by any gateway)", got.turnSecret)
	}
}

func TestChooseRestoreWebRTC_cachedTurnOnlyFallbackOnDBError(t *testing.T) {
	// A non-SFU node holds a cached TURN secret (HasSFU false / port 0) and the
	// DB read ERRORS (its namespace rqlite isn't readable yet at cold start).
	// DB-first falls back to the cached secret so the gateway still serves TURN
	// credentials — sfuPort stays 0 (no local SFU). This is the #130 resilience
	// the cache exists for.
	got := chooseRestoreWebRTC(false, 0, "turn.ns-x.dbrs.space", "state-secret", "", dbError)

	if !got.enabled || got.sfuPort != 0 || got.turnSecret != "state-secret" {
		t.Errorf("want cached TURN-only fallback (sfuPort 0); got %+v", got)
	}
	if got.unresolved {
		t.Error("a usable cached secret must not be marked unresolved")
	}
}

func TestChooseRestoreWebRTC_bothEmptyDisabled(t *testing.T) {
	// Namespace genuinely without WebRTC: state empty, DB returns nothing.
	// Must return disabled so we don't register broken webrtc routes.
	got := chooseRestoreWebRTC(false, 0, "", "", "", dbNone)
	if got.enabled {
		t.Errorf("want disabled when neither source has WebRTC; got %+v", got)
	}
}

func TestChooseRestoreWebRTC_dbNoSecretStaysDisabled(t *testing.T) {
	// Defensive: DB returns an SFU port but NO turn secret (half-
	// provisioned / shouldn't happen). The TURN secret is the
	// enablement marker; without it we treat it as not-configured-for-
	// TURN, but an SFU port alone still enables SFU routes.
	got := chooseRestoreWebRTC(false, 0, "", "", "",
		func() (string, string, string, int, bool) { return "", "turn.db", "", 9000, true })
	// dbFetch only runs when state secret is empty; here it returns no
	// secret, so the `if dbSecret != ""` guard means NOTHING is taken
	// from the DB → disabled. (An SFU-only-no-TURN namespace is not a
	// real configuration; TURN secret always accompanies enable.)
	if got.enabled {
		t.Errorf("DB returned no TURN secret: want disabled; got %+v", got)
	}
}

// --- feat-124 stealth domain restore precedence ---

func TestChooseRestoreWebRTC_stealthFromCacheOnDBError(t *testing.T) {
	// When the DB read errors, the cache fallback carries the whole block —
	// including the cached stealth domain — so a stealth-enabled namespace
	// keeps advertising its stealth rung on a cold start that can't reach the
	// DB yet.
	got := chooseRestoreWebRTC(true, 7800, "turn.ns-x.dbrs.space", "state-secret", "cdn-abc123def456.dbrs.space", dbError)
	if !got.enabled || got.stealthDomain != "cdn-abc123def456.dbrs.space" {
		t.Errorf("stealthDomain = %q; want cached value on DB-error fallback; got %+v", got.stealthDomain, got)
	}
}

func TestChooseRestoreWebRTC_stealthFromDBOnStaleState(t *testing.T) {
	// Stale state (no TURN secret) + DB has stealth enabled → stealth domain
	// re-materializes from the DB alongside the rest of the WebRTC block.
	got := chooseRestoreWebRTC(false, 0, "", "", "",
		func() (string, string, string, int, bool) {
			return "db-secret", "turn.ns-x.dbrs.space", "cdn-abc123def456.dbrs.space", 7801, true
		})
	if !got.enabled || got.stealthDomain != "cdn-abc123def456.dbrs.space" {
		t.Errorf("want stealth domain from DB on stale state; got %+v", got)
	}
}

// --- bugboard #130: distinguish "unresolved (DB/decrypt error)" from "disabled" ---

func TestChooseRestoreWebRTC_dbErrorMarksUnresolvedNotDisabled(t *testing.T) {
	// The bug-130 case: state file has no secret (freshly-joined node) and
	// the DB lookup ERRORS (e.g. the stored TURN secret can't be decrypted
	// after a cluster-secret rotation). This MUST surface as unresolved —
	// NOT as a clean "disabled" — so the caller preserves the running config
	// instead of writing a TURN-disabled gateway (which made turn.credentials
	// return namespace_not_configured).
	got := chooseRestoreWebRTC(false, 0, "", "", "", dbError)

	if !got.unresolved {
		t.Fatal("BUG #130 REGRESSION: a DB/decrypt error must mark the result unresolved")
	}
	if got.enabled {
		t.Errorf("unresolved result must never be enabled (would write a config off an errored lookup); got %+v", got)
	}
	if got.turnSecret != "" {
		t.Errorf("unresolved result must carry no secret; got %q", got.turnSecret)
	}
}

func TestChooseRestoreWebRTC_resolvedEmptyIsDisabledNotUnresolved(t *testing.T) {
	// The contrast case: the DB lookup COMPLETES and reports no WebRTC
	// (genuinely disabled namespace). This must be disabled, NOT unresolved —
	// the caller is free to write the empty/disabled config here.
	got := chooseRestoreWebRTC(false, 0, "", "", "", dbNone)

	if got.unresolved {
		t.Error("a clean resolved-but-empty lookup must NOT be marked unresolved")
	}
	if got.enabled {
		t.Errorf("genuinely-disabled namespace must be disabled; got %+v", got)
	}
}

func TestChooseRestoreWebRTC_cachedSecretSurvivesDBError(t *testing.T) {
	// A node that holds the TURN secret in its state file must NOT be disabled
	// by a flaky/unsynced DB — when the DB read errors, DB-first falls back to
	// the cached secret and stays enabled (not unresolved). Guards against the
	// #130 fix accidentally disabling nodes when the DB is briefly unreadable.
	got := chooseRestoreWebRTC(true, 7800, "turn.ns-x.dbrs.space", "state-secret", "", dbError)
	if got.unresolved || !got.enabled || got.turnSecret != "state-secret" {
		t.Errorf("cached secret must survive a DB error and stay enabled; got %+v", got)
	}
}

func TestChooseRestoreWebRTC_noStealthStaysEmpty(t *testing.T) {
	// Stealth disabled → empty stealthDomain (gateway advertises the baseline
	// 3-rung ladder only). Uses the cache-fallback path (DB error) so an
	// enabled-but-no-stealth config is exercised end to end.
	got := chooseRestoreWebRTC(true, 7800, "turn.ns-x.dbrs.space", "state-secret", "", dbError)
	if !got.enabled || got.stealthDomain != "" {
		t.Errorf("stealthDomain = %q; want empty when stealth is disabled; got %+v", got.stealthDomain, got)
	}
}

// ----------------------------------------------------------------------------
// Bugboard #130 — cache the resolved WebRTC secret into local state so a slow
// node's cold start reads it from disk instead of the (slow) namespace rqlite.
// ----------------------------------------------------------------------------

func TestApplyResolvedWebRTCToState_populatesAndReportsChange(t *testing.T) {
	st := &ClusterLocalState{} // fresh node: no cached secret (the #130 gap)
	wr := restoreWebRTC{enabled: true, turnSecret: "sek-123", turnDomain: "turn.ns-x.dbrs.space", stealthDomain: "cdn-abc.dbrs.space", sfuPort: 30000}

	if !applyResolvedWebRTCToState(st, wr) {
		t.Fatal("expected change=true when caching a secret into empty state")
	}
	if st.TURNSharedSecret != "sek-123" {
		t.Errorf("TURNSharedSecret = %q; want sek-123 (must be cached for cold start)", st.TURNSharedSecret)
	}
	if !st.HasTURN || !st.HasSFU || st.SFUSignalingPort != 30000 ||
		st.TURNDomain != "turn.ns-x.dbrs.space" || st.TURNStealthDomain != "cdn-abc.dbrs.space" {
		t.Errorf("state not fully populated: %+v", st)
	}

	// The whole point of caching: on a SECOND boot where the DB read fails
	// (slow node, namespace rqlite not synced), the cached secret lets the
	// gateway still come up on TURN (DB-first falls back to the cache).
	got := chooseRestoreWebRTC(st.HasSFU, st.SFUSignalingPort, st.TURNDomain, st.TURNSharedSecret, st.TURNStealthDomain, dbError)
	if !got.enabled || got.unresolved || got.turnSecret != "sek-123" {
		t.Errorf("cached cold start should fall back to the state secret on a DB error; got %+v", got)
	}
}

func TestApplyResolvedWebRTCToState_noChangeWhenAlreadyCached(t *testing.T) {
	st := &ClusterLocalState{HasTURN: true, HasSFU: true, TURNSharedSecret: "sek-123", TURNDomain: "d", TURNStealthDomain: "s", SFUSignalingPort: 30000}
	wr := restoreWebRTC{enabled: true, turnSecret: "sek-123", turnDomain: "d", stealthDomain: "s", sfuPort: 30000}
	if applyResolvedWebRTCToState(st, wr) {
		t.Error("expected change=false (no rewrite) when state already matches the resolved config")
	}
}

func TestApplyResolvedWebRTCToState_turnOnlyNode_noSFU(t *testing.T) {
	// A gateway-only node (serves TURN credentials, runs no local SFU): secret
	// set, sfuPort 0. Must still cache the secret + report HasTURN, HasSFU=false.
	st := &ClusterLocalState{}
	if !applyResolvedWebRTCToState(st, restoreWebRTC{enabled: true, turnSecret: "sek", turnDomain: "d", sfuPort: 0}) {
		t.Fatal("want change=true")
	}
	if !st.HasTURN || st.HasSFU || st.TURNSharedSecret != "sek" {
		t.Errorf("turn-only node: want HasTURN=true HasSFU=false secret cached; got %+v", st)
	}
}

func TestApplyResolvedWebRTCToState_clearsCacheOnDisable(t *testing.T) {
	// When the DB resolves the namespace as DISABLED, the caller applies an
	// empty restoreWebRTC to wipe any stale cached secret from local state — so
	// a node that was offline during DisableWebRTC can't later fall back to the
	// old secret on a transient DB error and resurrect TURN for a disabled
	// namespace. Must report change=true and zero out the cached fields.
	st := &ClusterLocalState{HasTURN: true, HasSFU: true, TURNSharedSecret: "stale-secret", TURNDomain: "turn.ns-x.dbrs.space", SFUSignalingPort: 7800}

	if !applyResolvedWebRTCToState(st, restoreWebRTC{}) {
		t.Fatal("disable: want change=true when clearing a cached secret")
	}
	if st.TURNSharedSecret != "" || st.HasTURN || st.HasSFU || st.SFUSignalingPort != 0 || st.TURNDomain != "" {
		t.Errorf("cache not fully cleared on disable: %+v", st)
	}
}

func TestApplyResolvedWebRTCToState_secretRotationReportsChange(t *testing.T) {
	// Secret rotation: the state holds an OLD cached secret and a fresh resolve
	// brings the NEW (rotated) secret. applyResolvedWebRTCToState MUST report
	// change=true and overwrite the cache, so the node's fallback secret tracks
	// the rotation instead of persisting a stale value on disk (bugboard #130
	// follow-up — the cache must never lag the rotated secret).
	st := &ClusterLocalState{HasTURN: true, TURNSharedSecret: "old-secret", TURNDomain: "turn.ns-x.dbrs.space"}
	wr := restoreWebRTC{enabled: true, turnSecret: "new-secret", turnDomain: "turn.ns-x.dbrs.space"}

	if !applyResolvedWebRTCToState(st, wr) {
		t.Fatal("rotation: want change=true when the resolved secret differs from the cached one")
	}
	if st.TURNSharedSecret != "new-secret" {
		t.Errorf("cache not updated to the rotated secret: got %q; want new-secret", st.TURNSharedSecret)
	}
}

// ----------------------------------------------------------------------------
// Bugboard #130 — the cold-start read retries so a slow node's namespace
// rqlite read lands once the follower syncs, instead of failing once and
// coming up with TURN disabled.
// ----------------------------------------------------------------------------

func TestResolveWebRTCConfigWithRetry_succeedsOnNthAttempt(t *testing.T) {
	// The read errors on the first two attempts (rqlite not readable yet) then
	// succeeds — the retry must return the config and not surface the earlier
	// transient errors.
	calls := 0
	slept := 0
	cfg, err := resolveWebRTCConfigWithRetry(5, time.Millisecond, func(time.Duration) { slept++ },
		func() (*WebRTCConfig, error) {
			calls++
			if calls < 3 {
				return nil, errors.New("rqlite not readable yet")
			}
			return &WebRTCConfig{TURNSharedSecret: "sek-123"}, nil
		})

	if err != nil {
		t.Fatalf("want success on the 3rd attempt; got err %v", err)
	}
	if cfg == nil || cfg.TURNSharedSecret != "sek-123" {
		t.Fatalf("want resolved config; got %+v", cfg)
	}
	if calls != 3 {
		t.Errorf("want exactly 3 fetch attempts; got %d", calls)
	}
	if slept != 2 {
		t.Errorf("want a sleep between each of the 2 failed attempts; got %d", slept)
	}
}

func TestResolveWebRTCConfigWithRetry_exhaustsAndReturnsError(t *testing.T) {
	// A persistent error (e.g. a decrypt failure after cluster-secret rotation)
	// must exhaust all attempts and return the final error — the caller maps
	// that to unresolved (NOT disabled). No sleep after the final attempt.
	calls := 0
	slept := 0
	cfg, err := resolveWebRTCConfigWithRetry(4, time.Millisecond, func(time.Duration) { slept++ },
		func() (*WebRTCConfig, error) {
			calls++
			return nil, errors.New("decrypt failed")
		})

	if err == nil {
		t.Fatal("want the final error after exhausting retries; got nil")
	}
	if cfg != nil {
		t.Errorf("want nil config on exhaustion; got %+v", cfg)
	}
	if calls != 4 {
		t.Errorf("want 4 attempts (all retries used); got %d", calls)
	}
	if slept != 3 {
		t.Errorf("want a sleep between attempts but not after the last; got %d", slept)
	}
}

// Bugboard SFU media_port=0 crash-loop — the SFU restore path (section 5 of
// restoreClusterFromState) must source its media ports from the DB port
// allocation, NOT the gateway-only local state file (where media ports are 0).
// sfuPortBlockSpawnable is the guard that refuses to spawn pion with a zero
// media range (which fails to bind and crash-loops the systemd unit). This
// reproduces the bug: a block with media_start=0 must be rejected.
func TestSFUPortBlockSpawnable(t *testing.T) {
	tests := []struct {
		name  string
		block *WebRTCPortBlock
		want  bool
	}{
		{"nil block (no allocation for this node)", nil, false},
		{"zero media start crash-loops pion — the bug", &WebRTCPortBlock{SFUSignalingPort: 30000, SFUMediaPortStart: 0, SFUMediaPortEnd: 0}, false},
		{"zero signaling port", &WebRTCPortBlock{SFUSignalingPort: 0, SFUMediaPortStart: 20000, SFUMediaPortEnd: 20499}, false},
		{"media end unset", &WebRTCPortBlock{SFUSignalingPort: 30000, SFUMediaPortStart: 20000, SFUMediaPortEnd: 0}, false},
		{"complete DB allocation is spawnable", &WebRTCPortBlock{SFUSignalingPort: 30000, SFUMediaPortStart: 20000, SFUMediaPortEnd: 20499}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sfuPortBlockSpawnable(tt.block); got != tt.want {
				t.Errorf("sfuPortBlockSpawnable(%+v) = %v, want %v", tt.block, got, tt.want)
			}
		})
	}
}

// TURN/SFU decoupling (bugboard #25) — regression: the gateway's TURN secret used
// to be set only inside `if sfuBlock != nil`, so a gateway on a node with no SFU
// allocation got NO turn_secret and answered /v1/webrtc/turn/credentials with 503
// "TURN not configured". Reachable via dead-node failover: a replacement gateway
// never holds an SFU allocation yet. Observed live on devnet (57.131.41.160 served
// ~50% of anchat-test credential requests and failed all of them).
//
// This pins the invariant the three call sites must all satisfy: TURN fields are
// namespace-wide and set whenever WebRTC is enabled; SFU fields are per-node.
func TestGatewayWebRTC_turnSecretIndependentOfSFUAllocation(t *testing.T) {
	// applyGatewayWebRTC mirrors the shape of the production blocks: TURN fields
	// unconditional on webrtc-enabled, SFU fields only when a block exists.
	type gw struct {
		WebRTCEnabled bool
		SFUPort       int
		TURNSecret    string
		TURNDomain    string
	}
	apply := func(webrtcEnabled bool, secret, domain string, sfuPort int, hasSFU bool) gw {
		var g gw
		if webrtcEnabled {
			g.TURNSecret = secret
			g.TURNDomain = domain
			if hasSFU {
				g.WebRTCEnabled = true
				g.SFUPort = sfuPort
			}
		}
		return g
	}

	t.Run("no SFU allocation still gets the TURN secret", func(t *testing.T) {
		g := apply(true, "ns-wide-secret", "turn.ns-x.d", 0, false)
		if g.TURNSecret != "ns-wide-secret" {
			t.Error("a gateway without an SFU allocation MUST still receive the namespace TURN secret (else 503 on credentials)")
		}
		if g.TURNDomain == "" {
			t.Error("turn domain must be set so the creds handler can build URIs")
		}
		if g.SFUPort != 0 || g.WebRTCEnabled {
			t.Error("SFU fields must stay unset on a non-SFU node")
		}
	})

	t.Run("SFU node gets both", func(t *testing.T) {
		g := apply(true, "ns-wide-secret", "turn.ns-x.d", 30000, true)
		if g.TURNSecret == "" || g.SFUPort != 30000 || !g.WebRTCEnabled {
			t.Errorf("SFU node should get both TURN and SFU config, got %+v", g)
		}
	})

	t.Run("webrtc disabled sets nothing", func(t *testing.T) {
		g := apply(false, "ns-wide-secret", "turn.ns-x.d", 30000, true)
		if g.TURNSecret != "" || g.SFUPort != 0 || g.WebRTCEnabled {
			t.Errorf("nothing should be set when webrtc is disabled, got %+v", g)
		}
	})
}
