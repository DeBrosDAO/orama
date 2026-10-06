package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// A deployed app used to run as whatever key somebody had pasted into it — a
// namespace key, so an application compromise was a namespace takeover. It is a
// principal of its own now, holding only what its owner granted it.

func TestWorkloadSubject_roundTrips(t *testing.T) {
	subject := WorkloadSubject("acme", "web")
	if !IsWorkloadSubject(subject) {
		t.Fatalf("%q is not recognised as a workload", subject)
	}
	if IsWalletSubject(subject) {
		t.Error("a workload subject is being read as a wallet, so it would be treated as a logged-in user")
	}

	ns, name, ok := ParseWorkloadSubject(subject)
	if !ok || ns != "acme" || name != "web" {
		t.Errorf("ParseWorkloadSubject(%q) = (%q, %q, %v)", subject, ns, name, ok)
	}

	for _, bad := range []string{"0xabc", "app:", "app:acme", "app:/web", "app:acme/"} {
		if _, _, ok := ParseWorkloadSubject(bad); ok {
			t.Errorf("%q parsed as a workload subject", bad)
		}
	}
}

// workloadService is a gateway with a real registry and its own signing key.
func workloadService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	// Two namespaces, so a test that a workload cannot renew into another
	// namespace fails on the check rather than on the namespace not existing.
	if _, err := db.Exec(`INSERT INTO namespaces(name) VALUES ('acme'), ('other')`); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService(nil, &sqliteNet{db: &sqliteDatabase{db: db}}, "", "default")
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetEdDSAKey(priv, "")
	return svc, db
}

// A deployment nobody has granted anything to gets a token that reaches
// nothing. The alternative — starting every app with the namespace's data
// plane — is the permanent key this replaces wearing a different hat.
func TestMintWorkloadToken_grantsNothingUntilSomethingIsGranted(t *testing.T) {
	svc, _ := workloadService(t)

	if err := svc.EnsureWorkloadPrincipal(context.Background(), "acme", "web"); err != nil {
		t.Fatalf("EnsureWorkloadPrincipal: %v", err)
	}
	token, _, err := svc.MintWorkloadToken(context.Background(), "acme", "web")
	if err != nil {
		t.Fatalf("MintWorkloadToken: %v", err)
	}

	claims, err := svc.ParseAndVerifyJWT(token)
	if err != nil {
		t.Fatalf("the token does not verify: %v", err)
	}
	if claims.Sub != "app:acme/web" || claims.Namespace != "acme" {
		t.Errorf("claims = %+v", claims)
	}
	if scopes := claims.Custom["scopes"]; scopes != "" {
		t.Errorf("an ungranted deployment was handed %q", scopes)
	}
}

func TestMintWorkloadToken_carriesTheGrantsItsOwnerGave(t *testing.T) {
	svc, _ := workloadService(t)
	ctx := context.Background()

	if err := svc.EnsureWorkloadPrincipal(ctx, "acme", "web"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Grant(ctx, GrantRequest{
		Namespace:     "acme",
		PrincipalType: PrincipalApp,
		Identifier:    WorkloadSubject("acme", "web"),
		Role:          RoleRuntime,
		CreatedBy:     "0xowner",
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	token, _, err := svc.MintWorkloadToken(ctx, "acme", "web")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := svc.ParseAndVerifyJWT(token)
	if err != nil {
		t.Fatal(err)
	}
	scopes := ParseScopes(claims.Custom["scopes"])
	if !scopes.Has(ScopeStorage) || !scopes.Has(ScopePubsub) {
		t.Errorf("a runtime deployment was handed %q", claims.Custom["scopes"])
	}
	if scopes.IsAdmin() {
		t.Error("a deployment was handed the control plane")
	}
}

// Grants are resolved when the token is minted, not baked in at deploy: taking
// one away has to reach a running deployment on its next renewal rather than
// only on its next deploy.
func TestRenewWorkloadToken_picksUpAGrantThatWasTakenAway(t *testing.T) {
	svc, db := workloadService(t)
	ctx := context.Background()

	if err := svc.EnsureWorkloadPrincipal(ctx, "acme", "web"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Grant(ctx, GrantRequest{
		Namespace: "acme", PrincipalType: PrincipalApp,
		Identifier: WorkloadSubject("acme", "web"), Role: RoleRuntime, CreatedBy: "0xowner",
	}); err != nil {
		t.Fatal(err)
	}

	token, _, err := svc.MintWorkloadToken(ctx, "acme", "web")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := svc.ParseAndVerifyJWT(token)
	if err != nil {
		t.Fatal(err)
	}

	// The owner revokes it.
	if _, err := db.Exec(`UPDATE grants SET revoked_at = datetime('now')`); err != nil {
		t.Fatal(err)
	}

	renewed, _, err := svc.RenewWorkloadToken(ctx, claims)
	if err != nil {
		t.Fatalf("RenewWorkloadToken: %v", err)
	}
	next, err := svc.ParseAndVerifyJWT(renewed)
	if err != nil {
		t.Fatal(err)
	}
	if next.Custom["scopes"] != "" {
		t.Errorf("a revoked deployment renewed into %q", next.Custom["scopes"])
	}
}

// Only a workload renews itself. A user's session is renewed by a refresh token
// that rotates and can be revoked; letting any token mint its own successor
// would make a stolen access token good for ever.
func TestRenewWorkloadToken_refusesAnythingThatIsNotAWorkload(t *testing.T) {
	svc, _ := workloadService(t)

	for _, claims := range []*JWTClaims{
		nil,
		{Sub: "0xwallet", Namespace: "acme"},
		{Sub: "orama_sk_abc_1", Namespace: "acme"},
	} {
		if _, _, err := svc.RenewWorkloadToken(context.Background(), claims); err == nil {
			t.Errorf("%+v was allowed to renew itself", claims)
		}
	}
}

// The subject and the claim have to agree, or a workload in one namespace could
// renew into a token for another.
func TestRenewWorkloadToken_refusesASubjectThatDisagreesWithTheClaim(t *testing.T) {
	svc, _ := workloadService(t)

	ctx := context.Background()
	// The namespace it names exists and it has a principal there, so the only
	// thing that can refuse this is the check itself.
	if err := svc.EnsureWorkloadPrincipal(ctx, "other", "web"); err != nil {
		t.Fatal(err)
	}

	_, _, err := svc.RenewWorkloadToken(ctx, &JWTClaims{
		Sub:       WorkloadSubject("other", "web"),
		Namespace: "acme",
	})
	if err == nil {
		t.Fatal("a workload renewed into a namespace its subject does not name")
	}
	if !strings.Contains(err.Error(), "other") {
		t.Errorf("the refusal does not say what disagreed: %v", err)
	}
}
