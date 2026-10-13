package auth

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"
)

// seedKey inserts an unbound or bound key row with explicit timestamps.
func seedKey(t *testing.T, keys *SigningKeys, kid string, namespace any, createdAt, lastSeen any, retiredAt any) {
	t.Helper()
	pub, _ := newKey(t)
	db := keys.database().(*sqliteDatabase).db
	if _, err := db.Exec(`INSERT INTO signing_keys(kid, namespace, algorithm, public_key, created_at, last_seen_at, retired_at)
		VALUES (?, ?, 'EdDSA', ?, ?, ?, ?)`, kid, namespace, encodePublicKey(pub), createdAt, lastSeen, retiredAt); err != nil {
		t.Fatal(err)
	}
}

func retiredAtOf(t *testing.T, keys *SigningKeys, kid string) any {
	t.Helper()
	db := keys.database().(*sqliteDatabase).db
	var at any
	if err := db.QueryRow(`SELECT retired_at FROM signing_keys WHERE kid = ?`, kid).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func ago(d time.Duration) string { return time.Now().Add(-d).UTC().Format(sqliteTime) }

// The retention has to outlast every token a key can have signed, or a retired
// key would refuse a token that is still valid.
func TestUnusedKeyRetention_outlastsEveryToken(t *testing.T) {
	if unusedKeyRetention <= MaxTokenLifetime+AccessTokenLifetime {
		t.Fatalf("unusedKeyRetention %s does not outlast MaxTokenLifetime %s", unusedKeyRetention, MaxTokenLifetime)
	}
	if signingKeyHeartbeatInterval*3 > unusedKeyRetention {
		t.Fatalf("a few missed heartbeats would retire a live key")
	}
}

func TestRetireUnusedKeys_retiresOnlyUnboundKeysNobodyHasUsed(t *testing.T) {
	keys, _ := signingKeyStore(t)
	old := ago(72 * time.Hour)
	seedKey(t, keys, "ed_dead", nil, old, old, nil)                     // restart leftover
	seedKey(t, keys, "ed_dead_unstamped", nil, old, nil, nil)           // pre-071 row, never stamped
	seedKey(t, keys, "ed_peer_live", nil, old, ago(5*time.Minute), nil) // old key, stamped by a running peer
	seedKey(t, keys, "ed_current", nil, old, old, nil)                  // this gateway's own
	seedKey(t, keys, "ed_fresh_unstamped", nil, ago(time.Hour), nil, nil)
	seedKey(t, keys, "ed_tenant", "acme", old, old, nil) // bound: not ours to retire
	seedKey(t, keys, "ed_rotated", nil, old, old, ago(-time.Hour))

	n, err := keys.RetireUnusedKeys(context.Background(), "ed_current", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("retired %d keys, want 2", n)
	}
	for _, kid := range []string{"ed_dead", "ed_dead_unstamped"} {
		if retiredAtOf(t, keys, kid) == nil {
			t.Errorf("%s was not retired", kid)
		}
	}
	for _, kid := range []string{"ed_peer_live", "ed_current", "ed_fresh_unstamped", "ed_tenant"} {
		if at := retiredAtOf(t, keys, kid); at != nil {
			t.Errorf("%s was retired (%v) although it could still verify a live token", kid, at)
		}
	}
}

func TestRetireUnusedKeys_nothingToRetireAndNoRegistry(t *testing.T) {
	keys, _ := signingKeyStore(t)
	if n, err := keys.RetireUnusedKeys(context.Background(), "ed_current", time.Now()); err != nil || n != 0 {
		t.Fatalf("empty table: n=%d err=%v", n, err)
	}
	local := NewSigningKeys(nil, nil)
	if n, err := local.RetireUnusedKeys(context.Background(), "x", time.Now()); err != nil || n != 0 {
		t.Fatalf("no registry: n=%d err=%v", n, err)
	}
}

func TestRetireUnusedKeys_aStampKeepsAKeyAlive(t *testing.T) {
	keys, _ := signingKeyStore(t)
	old := ago(72 * time.Hour)
	seedKey(t, keys, "ed_peer", nil, old, old, nil)
	if err := keys.Stamp(context.Background(), "ed_peer"); err != nil {
		t.Fatal(err)
	}
	if n, err := keys.RetireUnusedKeys(context.Background(), "ed_current", time.Now()); err != nil || n != 0 {
		t.Fatalf("a stamped key was retired: n=%d err=%v", n, err)
	}
}

func TestRetireUnusedKeys_aRestartOfARetiredKeyRevivesIt(t *testing.T) {
	keys, _ := signingKeyStore(t)
	pub, _ := newKey(t)
	old := ago(72 * time.Hour)
	kid := KeyIDFor(pub)
	seedKey(t, keys, kid, nil, old, old, nil)
	if _, err := keys.RetireUnusedKeys(context.Background(), "ed_other", time.Now()); err != nil {
		t.Fatal(err)
	}
	if retiredAtOf(t, keys, kid) == nil {
		t.Fatal("setup: key not retired")
	}
	if err := keys.Publish(context.Background(), SigningKey{KID: kid, Public: pub}); err != nil {
		t.Fatal(err)
	}
	if at := retiredAtOf(t, keys, kid); at != nil {
		t.Errorf("a gateway that restarted with its key could not bring it back: %v", at)
	}
}

func TestService_retireUnusedIndexKeys_skipsANamespaceGateway(t *testing.T) {
	svc := serviceWithKey(t, "acme")
	keys, _ := signingKeyStore(t)
	svc.signingKeys.registry = keys.registry
	seedKey(t, keys, "ed_dead", nil, ago(72*time.Hour), nil, nil)
	if n, err := svc.RetireUnusedIndexKeys(context.Background()); err != nil || n != 0 {
		t.Fatalf("a namespace gateway retired index keys: n=%d err=%v", n, err)
	}
	if retiredAtOf(t, keys, "ed_dead") != nil {
		t.Fatal("index key retired by a namespace gateway")
	}

	index := serviceWithKey(t, "")
	index.signingKeys.registry = keys.registry
	if n, err := index.RetireUnusedIndexKeys(context.Background()); err != nil || n != 1 {
		t.Fatalf("index gateway: n=%d err=%v", n, err)
	}
}

// Rotation writes through the store it was given, whatever that is.
func TestRotate_writesThroughTheKeyStore(t *testing.T) {
	svc := serviceWithKey(t, "")
	keys, _ := signingKeyStore(t)
	svc.signingKeys.registry = keys.registry
	var stored []ed25519.PrivateKey
	svc.SetKeyStore(KeyStore{Where: "the sink", Store: func(p ed25519.PrivateKey) error { stored = append(stored, p); return nil }})

	next, err := svc.Rotate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || KeyIDFor(stored[0].Public().(ed25519.PublicKey)) != next.KID {
		t.Fatalf("the sink got %d key(s), not the replacement", len(stored))
	}
}

func TestRotate_refusesWithoutAKeyStore(t *testing.T) {
	svc := serviceWithKey(t, "")
	before := svc.SigningKID()
	if _, err := svc.Rotate(context.Background()); err == nil {
		t.Fatal("rotated with nowhere to store the key")
	}
	if svc.SigningKID() != before {
		t.Fatal("the key changed")
	}
}

// TestStamp_bringsBackARetiredKeyStillInUse: a gateway whose key a peer retired
// (stamping failed for a day, or the peer ran before this release stamped)
// kept signing tokens every other node refused until it restarted.
func TestStamp_bringsBackARetiredKeyStillInUse(t *testing.T) {
	keys, _ := signingKeyStore(t)
	old := ago(72 * time.Hour)
	seedKey(t, keys, "ed_mine", nil, old, old, nil)
	if _, err := keys.RetireUnusedKeys(context.Background(), "ed_other", time.Now()); err != nil {
		t.Fatal(err)
	}
	if retiredAtOf(t, keys, "ed_mine") == nil {
		t.Fatal("setup: key not retired")
	}
	if err := keys.Stamp(context.Background(), "ed_mine"); err != nil {
		t.Fatal(err)
	}
	if at := retiredAtOf(t, keys, "ed_mine"); at != nil {
		t.Errorf("the heartbeat of a gateway still signing with its key left it retired: %v", at)
	}
}
