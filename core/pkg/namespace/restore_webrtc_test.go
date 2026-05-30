package namespace

import "testing"

// Bugboard #25 — WebRTC config drift on restart.
//
// chooseRestoreWebRTC decides the gateway's WebRTC fields when a node
// restores namespace clusters from its local state file. The local state
// file is NOT updated by EnableWebRTC, so a namespace enabled after its
// state file was written has no SFU/TURN fields there — and because the
// from-disk restore runs first and succeeds, the DB-backed restore (which
// DOES read WebRTC) never runs. Result: the gateway config loses its
// webrtc block on every restart (SFU/TURN services keep running but the
// gateway reports configured:false and /v1/webrtc/turn/credentials 404s).
//
// These tests pin the precedence: state file when complete, DB fallback
// otherwise. The bug was the missing DB fallback.

func dbDisabled() (bool, int, string, string) { return false, 0, "", "" }

func dbEnabled(port int, domain, secret string) func() (bool, int, string, string) {
	return func() (bool, int, string, string) { return true, port, domain, secret }
}

func TestChooseRestoreWebRTC_stateFileCompleteWins(t *testing.T) {
	// State file has a full block → use it, and NEVER consult the DB
	// (the lazy dbFetch must not be called — saves a query on the hot
	// restart path).
	dbCalled := false
	got := chooseRestoreWebRTC(true, 7800, "turn.ns-x.dbrs.space", "state-secret",
		func() (bool, int, string, string) { dbCalled = true; return dbDisabled() })

	if dbCalled {
		t.Error("DB fetch was called even though the state file was complete (should short-circuit)")
	}
	if !got.enabled || got.sfuPort != 7800 || got.turnSecret != "state-secret" {
		t.Errorf("want state-file values; got %+v", got)
	}
	if got.turnDomain != "turn.ns-x.dbrs.space" {
		t.Errorf("turnDomain = %q; want state-file value", got.turnDomain)
	}
}

func TestChooseRestoreWebRTC_staleStateFallsBackToDB(t *testing.T) {
	// The actual bug-25 case: state file has NO webrtc (stale — written
	// before enable), but the DB says enabled. MUST fall back to the DB
	// so the block re-materializes instead of being silently dropped.
	got := chooseRestoreWebRTC(false, 0, "", "",
		dbEnabled(7801, "turn.ns-anchat-test.dbrs.space", "db-secret"))

	if !got.enabled {
		t.Fatal("BUG #25 REGRESSION: stale state file + DB-enabled WebRTC must fall back to DB; got disabled")
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

func TestChooseRestoreWebRTC_bothEmptyDisabled(t *testing.T) {
	// Namespace genuinely without WebRTC: state file empty, DB disabled.
	// Must return disabled so we don't register broken webrtc routes.
	got := chooseRestoreWebRTC(false, 0, "", "", dbDisabled)
	if got.enabled {
		t.Errorf("want disabled when neither source has WebRTC; got %+v", got)
	}
}

func TestChooseRestoreWebRTC_incompleteStateFileFallsToDB(t *testing.T) {
	// State file partially populated (HasSFU but missing secret, or
	// port 0) must NOT be treated as complete — fall through to DB.
	// Catches a regression where a half-written state file shadows the
	// DB and yields a broken (secret-less) gateway config.
	cases := []struct {
		name      string
		hasSFU    bool
		sfuPort   int
		turnSec   string
	}{
		{"hasSFU but port 0", true, 0, "s"},
		{"hasSFU but empty secret", true, 7800, ""},
		{"no hasSFU flag", false, 7800, "s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := chooseRestoreWebRTC(tc.hasSFU, tc.sfuPort, "d", tc.turnSec,
				dbEnabled(9000, "turn.db", "db-secret"))
			if !got.enabled || got.sfuPort != 9000 || got.turnSecret != "db-secret" {
				t.Errorf("incomplete state file should fall back to DB; got %+v", got)
			}
		})
	}
}

func TestChooseRestoreWebRTC_dbIncompleteStaysDisabled(t *testing.T) {
	// Defensive: if the DB row exists but is itself incomplete (no port
	// or no secret — e.g. a half-provisioned enable), do NOT enable with
	// a broken block. Better disabled than registering routes that 500.
	got := chooseRestoreWebRTC(false, 0, "", "",
		func() (bool, int, string, string) { return true, 0, "turn.db", "" })
	if got.enabled {
		t.Errorf("DB row incomplete (port 0, no secret): want disabled; got %+v", got)
	}
}
