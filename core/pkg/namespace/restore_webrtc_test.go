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

// dbFetch signature: () -> (turnSecret, turnDomain string, sfuPort int).
func dbNone() (string, string, int) { return "", "", 0 }

func dbFull(secret, domain string, sfuPort int) func() (string, string, int) {
	return func() (string, string, int) { return secret, domain, sfuPort }
}

func TestChooseRestoreWebRTC_stateFileCompleteWins(t *testing.T) {
	// State file has TURN secret → use it, and NEVER consult the DB
	// (the lazy dbFetch must not be called — saves a query on the hot
	// restart path).
	dbCalled := false
	got := chooseRestoreWebRTC(true, 7800, "turn.ns-x.dbrs.space", "state-secret",
		func() (string, string, int) { dbCalled = true; return dbNone() })

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
	got := chooseRestoreWebRTC(false, 0, "", "",
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
	got := chooseRestoreWebRTC(false, 0, "", "",
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
	got := chooseRestoreWebRTC(false, 0, "turn.ns-x.dbrs.space", "state-secret",
		func() (string, string, int) { dbCalled = true; return dbNone() })

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
	got := chooseRestoreWebRTC(false, 0, "", "", dbNone)
	if got.enabled {
		t.Errorf("want disabled when neither source has WebRTC; got %+v", got)
	}
}

func TestChooseRestoreWebRTC_dbNoSecretStaysDisabled(t *testing.T) {
	// Defensive: DB returns an SFU port but NO turn secret (half-
	// provisioned / shouldn't happen). The TURN secret is the
	// enablement marker; without it we treat it as not-configured-for-
	// TURN, but an SFU port alone still enables SFU routes.
	got := chooseRestoreWebRTC(false, 0, "", "",
		func() (string, string, int) { return "", "turn.db", 9000 })
	// dbFetch only runs when state secret is empty; here it returns no
	// secret, so the `if dbSecret != ""` guard means NOTHING is taken
	// from the DB → disabled. (An SFU-only-no-TURN namespace is not a
	// real configuration; TURN secret always accompanies enable.)
	if got.enabled {
		t.Errorf("DB returned no TURN secret: want disabled; got %+v", got)
	}
}
