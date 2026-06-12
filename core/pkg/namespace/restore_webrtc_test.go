package namespace

import "testing"

// Bugboard #25 — WebRTC config drift on restart + TURN/SFU decouple.
//
// chooseRestoreWebRTC resolves a restored gateway's WebRTC config from the
// local state file (which EnableWebRTC does NOT update) with a DB fallback
// (source of truth). It also DECOUPLES the two aspects: TURN (secret +
// domain) is namespace-wide so ANY gateway can serve credentials; the SFU
// port is per-node (0 on a gateway-only node). Pins both the drift
// fallback and the non-SFU-gateway case.

// dbFetch signature: () -> (turnSecret, turnDomain, stealthDomain string, sfuPort int, resolved bool).
// resolved=true means the lookup completed (with or without a config);
// resolved=false means it ERRORED (e.g. decrypt failure) → unresolved.
func dbNone() (string, string, string, int, bool) { return "", "", "", 0, true }

// dbError models a DB/decrypt failure: the lookup did not complete.
func dbError() (string, string, string, int, bool) { return "", "", "", 0, false }

func dbFull(secret, domain string, sfuPort int) func() (string, string, string, int, bool) {
	return func() (string, string, string, int, bool) { return secret, domain, "", sfuPort, true }
}

func TestChooseRestoreWebRTC_stateFileCompleteWins(t *testing.T) {
	// State file has TURN secret → use it, and NEVER consult the DB
	// (the lazy dbFetch must not be called — saves a query on the hot
	// restart path).
	dbCalled := false
	got := chooseRestoreWebRTC(true, 7800, "turn.ns-x.dbrs.space", "state-secret", "",
		func() (string, string, string, int, bool) { dbCalled = true; return dbNone() })

	if dbCalled {
		t.Error("DB fetch was called even though the state file had the TURN secret (should short-circuit)")
	}
	if !got.enabled || got.sfuPort != 7800 || got.turnSecret != "state-secret" {
		t.Errorf("want state-file values; got %+v", got)
	}
	if got.turnDomain != "turn.ns-x.dbrs.space" {
		t.Errorf("turnDomain = %q; want state-file value", got.turnDomain)
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

func TestChooseRestoreWebRTC_stateHasTURNButNoSFU(t *testing.T) {
	// State file for a non-SFU node: it has the TURN secret but HasSFU is
	// false / port 0. Must use the state TURN secret with sfuPort=0 and
	// NOT consult the DB (TURN secret present = complete enough).
	dbCalled := false
	got := chooseRestoreWebRTC(false, 0, "turn.ns-x.dbrs.space", "state-secret", "",
		func() (string, string, string, int, bool) { dbCalled = true; return dbNone() })

	if dbCalled {
		t.Error("DB fetch called even though state file had the TURN secret")
	}
	if !got.enabled || got.sfuPort != 0 || got.turnSecret != "state-secret" {
		t.Errorf("want TURN-only from state (sfuPort 0); got %+v", got)
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

func TestChooseRestoreWebRTC_stealthFromStateFile(t *testing.T) {
	// Stealth toggles rewrite cluster state, so a fresh state file carries
	// the stealth domain and must win without a DB call.
	got := chooseRestoreWebRTC(true, 7800, "turn.ns-x.dbrs.space", "state-secret", "cdn-abc123def456.dbrs.space",
		func() (string, string, string, int, bool) {
			t.Error("DB fetch called even though state file was complete")
			return dbNone()
		})
	if got.stealthDomain != "cdn-abc123def456.dbrs.space" {
		t.Errorf("stealthDomain = %q; want state-file value", got.stealthDomain)
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

func TestChooseRestoreWebRTC_stateSecretWinsOverDBError(t *testing.T) {
	// A node that already holds the TURN secret in its state file must NOT be
	// affected by a DB error — it short-circuits before dbFetch and stays
	// enabled/resolved. Guards against the #130 fix accidentally disabling
	// healthy nodes when the DB is flaky.
	got := chooseRestoreWebRTC(true, 7800, "turn.ns-x.dbrs.space", "state-secret", "",
		func() (string, string, string, int, bool) {
			t.Error("DB fetch must not be called when the state file has the secret")
			return dbError()
		})
	if got.unresolved || !got.enabled || got.turnSecret != "state-secret" {
		t.Errorf("state-file secret must win and stay enabled/resolved; got %+v", got)
	}
}

func TestChooseRestoreWebRTC_noStealthStaysEmpty(t *testing.T) {
	// Stealth disabled everywhere → empty stealthDomain (gateway advertises
	// the baseline 3-rung ladder only).
	got := chooseRestoreWebRTC(true, 7800, "turn.ns-x.dbrs.space", "state-secret", "", dbNone)
	if got.stealthDomain != "" {
		t.Errorf("stealthDomain = %q; want empty when stealth is disabled", got.stealthDomain)
	}
}
