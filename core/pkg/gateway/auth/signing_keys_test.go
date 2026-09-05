package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// The signing key used to be HKDF-derived from the cluster secret, which every
// node holds. Every gateway could therefore mint a token for every namespace,
// and there was nothing to rotate to. These are the properties that has to be
// replaced by.

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// signingKeyStore is a real SQLite registry with the real migrations applied.
func signingKeyStore(t *testing.T) (*SigningKeys, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return NewSigningKeys(registryOf(&sqliteNet{db: &sqliteDatabase{db: db}}), nil), db
}

// A key bound to a namespace signs for that namespace and nothing else. This is
// the property the whole change exists for: one tenant's gateway cannot mint a
// token for another.
func TestSigningKey_boundKeySignsOnlyForItsNamespace(t *testing.T) {
	pub, _ := newKey(t)
	key := SigningKey{KID: KeyIDFor(pub), Namespace: "acme", Public: pub}

	if !key.Binds("acme") {
		t.Error("a key refused its own namespace")
	}
	if !key.Binds("ACME") {
		t.Error("the binding is case-sensitive; a namespace is not")
	}
	if key.Binds("other") {
		t.Error("a namespace-bound key signed for another namespace")
	}
}

// The index gateway's key is bound to nothing: it is the control plane, and it
// mints the tokens the CLI signs in with for every namespace.
func TestSigningKey_theIndexKeyIsNotBound(t *testing.T) {
	pub, _ := newKey(t)
	key := SigningKey{KID: KeyIDFor(pub), Public: pub}

	for _, ns := range []string{"acme", "other", ""} {
		if !key.Binds(ns) {
			t.Errorf("the index key refused to sign for %q", ns)
		}
	}
}

func TestKeyIDFor_isDerivedFromTheKey(t *testing.T) {
	pubA, _ := newKey(t)
	pubB, _ := newKey(t)

	if KeyIDFor(pubA) == KeyIDFor(pubB) {
		t.Error("two keys share a kid, so one gateway's tokens verify against the other's key")
	}
	if KeyIDFor(pubA) != KeyIDFor(pubA) {
		t.Error("a key's id is not stable, so a token minted a moment ago names a key nobody can find")
	}
}

func TestSigningKeys_publishThenLookUp(t *testing.T) {
	keys, _ := signingKeyStore(t)
	pub, _ := newKey(t)
	key := SigningKey{KID: KeyIDFor(pub), Namespace: "acme", Public: pub}

	if err := keys.Publish(context.Background(), key); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// A second set, as another gateway would see it.
	other, _ := signingKeyStore(t)
	_ = other

	found, ok := keys.Lookup(key.KID)
	if !ok {
		t.Fatal("a published key could not be looked up")
	}
	if found.Namespace != "acme" || !found.Public.Equal(pub) {
		t.Errorf("looked up %+v", found)
	}
}

// Another gateway has to see the key, or the tokens one gateway mints are
// refused everywhere else.
func TestSigningKeys_aPublishedKeyIsVisibleToAnotherGateway(t *testing.T) {
	keys, db := signingKeyStore(t)
	pub, _ := newKey(t)
	key := SigningKey{KID: KeyIDFor(pub), Namespace: "acme", Public: pub}
	if err := keys.Publish(context.Background(), key); err != nil {
		t.Fatal(err)
	}

	// A second gateway reading the same registry.
	elsewhere := NewSigningKeys(registryOf(&sqliteNet{db: &sqliteDatabase{db: db}}), nil)
	if err := elsewhere.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	found, ok := elsewhere.Lookup(key.KID)
	if !ok {
		t.Fatal("another gateway cannot see the published key")
	}
	if found.Namespace != "acme" {
		t.Errorf("the binding did not survive publication: %+v", found)
	}
}

// Retiring is what a rotation does to the outgoing key. It must stop being
// accepted at the moment it is retired, and not before: tokens already issued
// have to keep working until they expire.
func TestSigningKeys_aRetiredKeyStopsBeingAccepted(t *testing.T) {
	keys, _ := signingKeyStore(t)
	pub, _ := newKey(t)
	key := SigningKey{KID: KeyIDFor(pub), Namespace: "acme", Public: pub}
	if err := keys.Publish(context.Background(), key); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	keys.now = func() time.Time { return now }

	if err := keys.Retire(context.Background(), key.KID, now.Add(time.Minute)); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if err := keys.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, ok := keys.Lookup(key.KID); !ok {
		t.Error("a key retired a minute from now was refused immediately, which breaks every token already issued")
	}

	keys.now = func() time.Time { return now.Add(2 * time.Minute) }
	if err := keys.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys.Lookup(key.KID); ok {
		t.Error("a retired key is still accepted")
	}
}

// A key this gateway added locally — its own, and the previous cluster-derived
// one — must survive a reload that did not find it published, or a gateway
// stops verifying what it just minted.
func TestSigningKeys_reloadKeepsTheLocalKeys(t *testing.T) {
	keys, _ := signingKeyStore(t)
	pub, _ := newKey(t)
	local := SigningKey{KID: KeyIDFor(pub), Public: pub}
	keys.Add(local)

	if err := keys.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys.Lookup(local.KID); !ok {
		t.Error("a reload dropped this gateway's own key")
	}
}

// A failed read must not empty the set: forgetting every key because one query
// failed would refuse every token in the cluster.
func TestSigningKeys_aFailedReloadKeepsWhatIsKnown(t *testing.T) {
	keys := NewSigningKeys(registryOf(&failingNet{}), nil)
	pub, _ := newKey(t)
	key := SigningKey{KID: KeyIDFor(pub), Public: pub}
	keys.Add(key)

	if err := keys.Reload(context.Background()); err == nil {
		t.Fatal("a failing database reported success")
	}
	if _, ok := keys.Lookup(key.KID); !ok {
		t.Error("a failed reload dropped every key, which refuses every token in the cluster")
	}
}

type failingDatabase struct{ client.DatabaseClient }

func (failingDatabase) Query(context.Context, string, ...interface{}) (*client.QueryResult, error) {
	return nil, errString("the registry did not answer")
}

type failingNet struct{ client.NetworkClient }

func (failingNet) Database() client.DatabaseClient { return failingDatabase{} }

// The whole point, end to end: a namespace gateway signs a token claiming
// another namespace, and every verifier refuses it.
//
// Before this, the key was derived from the cluster secret every node holds, so
// the same token verified everywhere.
func TestParseAndVerifyJWT_refusesATokenSignedForAnotherNamespace(t *testing.T) {
	acme := serviceWithKey(t, "acme")
	other := serviceWithKey(t, "other")

	// acme's gateway mints a token claiming to be for "other".
	token, _, err := acme.GenerateJWT("other", "0xowner", time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	// It does not even verify on the gateway that signed it.
	if _, err := acme.ParseAndVerifyJWT(token); err == nil {
		t.Error("a namespace gateway minted a token for another namespace and accepted it")
	}

	// And the other namespace's gateway, told about acme's key, refuses it too.
	shareKeys(t, acme, other)
	if _, err := other.ParseAndVerifyJWT(token); err == nil {
		t.Fatal("a token signed by another tenant's gateway was accepted")
	}
}

// A namespace gateway's own tokens still work, or the binding has broken the
// thing it was protecting.
func TestParseAndVerifyJWT_acceptsATokenForItsOwnNamespace(t *testing.T) {
	acme := serviceWithKey(t, "acme")

	token, _, err := acme.GenerateJWT("acme", "0xowner", time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	claims, err := acme.ParseAndVerifyJWT(token)
	if err != nil {
		t.Fatalf("a gateway refused its own token: %v", err)
	}
	if claims.Namespace != "acme" {
		t.Errorf("namespace = %q", claims.Namespace)
	}
}

// The index gateway is the control plane and mints for every namespace, which
// is how `orama auth login --namespace X` works.
func TestParseAndVerifyJWT_theIndexKeySignsForAnyNamespace(t *testing.T) {
	index := serviceWithKey(t, "")

	for _, ns := range []string{"acme", "other"} {
		token, _, err := index.GenerateJWT(ns, "0xowner", time.Minute, nil)
		if err != nil {
			t.Fatalf("GenerateJWT(%s): %v", ns, err)
		}
		if _, err := index.ParseAndVerifyJWT(token); err != nil {
			t.Errorf("the index gateway refused its own token for %s: %v", ns, err)
		}
	}
}

// A token naming a key this gateway has never heard of is refused, rather than
// verified against whatever key happens to be loaded.
func TestParseAndVerifyJWT_refusesAnUnknownKeyID(t *testing.T) {
	acme := serviceWithKey(t, "acme")
	stranger := serviceWithKey(t, "acme")

	token, _, err := stranger.GenerateJWT("acme", "0xowner", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acme.ParseAndVerifyJWT(token); err == nil {
		t.Fatal("a token signed by an unknown key was accepted")
	}
}

// serviceWithKey is a gateway with its own freshly generated signing key, bound
// to a namespace (or to nothing, for the index gateway).
func serviceWithKey(t *testing.T, namespace string) *Service {
	t.Helper()
	svc, err := NewService(nil, nil, "", "default")
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetEdDSAKey(priv, namespace)
	return svc
}

// shareKeys tells one gateway about another's public key, as publication does.
func shareKeys(t *testing.T, from, to *Service) {
	t.Helper()
	for _, key := range from.SigningKeys().All() {
		to.SigningKeys().Add(key)
	}
}

// Rotation: the new key signs, the old one keeps verifying what it already
// signed, and both are in flight for one access-token lifetime.
func TestRotate_leavesTheOutgoingKeyVerifiable(t *testing.T) {
	svc := serviceWithKey(t, "acme")
	keys, db := signingKeyStore(t)
	svc.signingKeys.registry = keys.registry
	_ = db

	before, _, err := svc.GenerateJWT("acme", "0xowner", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	previousKID := svc.SigningKID()

	next, err := svc.Rotate(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if next.KID == previousKID {
		t.Fatal("rotation produced the same key")
	}
	if svc.SigningKID() != next.KID {
		t.Errorf("the gateway is still signing with %s", svc.SigningKID())
	}
	if next.Namespace != "acme" {
		t.Errorf("the replacement key is bound to %q, not to the namespace it signs for", next.Namespace)
	}

	// A token minted before the rotation still verifies.
	if _, err := svc.ParseAndVerifyJWT(before); err != nil {
		t.Errorf("rotation invalidated a token that had not expired: %v", err)
	}

	// And so does one minted after.
	after, _, err := svc.GenerateJWT("acme", "0xowner", time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ParseAndVerifyJWT(after); err != nil {
		t.Errorf("the replacement key does not verify its own token: %v", err)
	}
}

// The replacement has to be on disk before the process restarts, or the next
// boot signs with the old key and every token minted since the rotation is
// refused.
func TestRotate_writesTheReplacementWhereTheNextBootReadsIt(t *testing.T) {
	svc := serviceWithKey(t, "acme")
	keys, _ := signingKeyStore(t)
	svc.signingKeys.registry = keys.registry

	dir := t.TempDir()
	next, err := svc.Rotate(context.Background(), dir)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "secrets", EdDSAKeyFileName))
	if err != nil {
		t.Fatalf("the replacement key was not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("the replacement key is mode %o, want 0600", perm)
	}
	if next.KID != svc.SigningKID() {
		t.Error("the key on disk is not the one being signed with")
	}
}

// A retired_at that is present and cannot be read is a key somebody meant to
// retire. Treating it as live would keep accepting a key whose retirement the
// gateway could not understand — which is the direction that fails open.
func TestRetirementFrom_readsEveryShapeTheDatabaseReturns(t *testing.T) {
	when := time.Date(2026, 9, 5, 10, 30, 0, 0, time.UTC)

	cases := []struct {
		name string
		cell any
		want time.Time
	}{
		{"null is not retired", nil, time.Time{}},
		{"empty is not retired", "", time.Time{}},
		{"rqlite returns a string", "2026-09-05 10:30:00", when},
		{"a driver may return a time", when, when},
		{"RFC3339", "2026-09-05T10:30:00Z", when},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retirementFrom(tc.cell); !got.Equal(tc.want) {
				t.Errorf("retirementFrom(%v) = %v, want %v", tc.cell, got, tc.want)
			}
		})
	}

	unreadable := retirementFrom("whenever")
	if unreadable.IsZero() {
		t.Error("an unreadable retirement was treated as live; a key nobody can tell the retirement of must not sign")
	}
	if unreadable.After(time.Now()) {
		t.Errorf("an unreadable retirement resolved to %v, which is in the future", unreadable)
	}
}

// registryOf adapts a network client to the resolver NewSigningKeys takes.
//
// The resolver is a function rather than a handle because a namespace gateway
// is told where its registry is after the auth service is built; a set that
// captured the handle it was constructed with published its key into the
// tenant's own database.
func registryOf(orm client.NetworkClient) func() client.DatabaseClient {
	return func() client.DatabaseClient {
		if orm == nil {
			return nil
		}
		return orm.Database()
	}
}

// A namespace gateway holds two databases: the tenant's own, and the cluster
// registry it validates credentials against. It is told about the second one
// *after* the auth service is built.
//
// A signing-key set that captured the handle it was constructed with published
// the gateway's key into the tenant's database. Two things followed. The index
// never saw the key, so every token that gateway minted was refused anywhere
// else in the cluster. And the key list a gateway will accept tokens from sat
// in a database the tenant can write — including an *unbound* key, which by
// design signs for any namespace.
func TestSigningKeys_publishToTheRegistryAndNotTheTenantsDatabase(t *testing.T) {
	tenant, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tenant.Close() })
	registry, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })

	for _, db := range []*sql.DB{tenant, registry} {
		if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
			t.Fatalf("apply migrations: %v", err)
		}
	}

	logger, err := logging.NewColoredLogger(logging.ComponentGateway, false)
	if err != nil {
		t.Fatal(err)
	}
	// Built the way a namespace gateway is: the tenant's database is what the
	// service is constructed with, and the registry arrives afterwards.
	svc, err := NewService(logger, &sqliteNet{db: &sqliteDatabase{db: tenant}}, "", "acme")
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	svc.SetAPIKeyRegistry(&sqliteNet{db: &sqliteDatabase{db: registry}})

	pub, _, keyErr := ed25519.GenerateKey(rand.Reader)
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	key := SigningKey{KID: KeyIDFor(pub), Namespace: "acme", Public: pub}
	if err := svc.signingKeys.Publish(context.Background(), key); err != nil {
		t.Fatalf("publish: %v", err)
	}

	var inRegistry, inTenant int
	if err := registry.QueryRow(`SELECT COUNT(*) FROM signing_keys WHERE kid = ?`, key.KID).Scan(&inRegistry); err != nil {
		t.Fatal(err)
	}
	if err := tenant.QueryRow(`SELECT COUNT(*) FROM signing_keys WHERE kid = ?`, key.KID).Scan(&inTenant); err != nil {
		t.Fatal(err)
	}

	if inRegistry != 1 {
		t.Error("the key was not published to the cluster registry, so no other gateway will accept a token it signs")
	}
	if inTenant != 0 {
		t.Error("the key was published into the tenant's own database, where the tenant can add one of their own")
	}
}
