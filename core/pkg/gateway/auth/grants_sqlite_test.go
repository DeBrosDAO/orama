package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// The tests above run against a fake that answers the statements this package
// issues. A fake that models a WHERE clause is not a test of that WHERE clause:
// delete `AND g.revoked_at IS NULL` from the query and the fake still filters
// revoked rows, so the test passes and every revoked grant authorises.
//
// These run the real SQL, against the real schema, on real SQLite. What they
// are for is the predicates — revoked, expired, disabled — which are the whole
// of what makes a grant stop working.

// sqliteDatabase is client.DatabaseClient over a real *sql.DB.
type sqliteDatabase struct {
	client.DatabaseClient
	db *sql.DB
}

func (s *sqliteDatabase) Query(ctx context.Context, query string, args ...interface{}) (*client.QueryResult, error) {
	// The write paths go through Query too, and a statement with no rows to
	// return is not a select.
	if !isSelect(query) {
		if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
			return nil, err
		}
		return &client.QueryResult{Count: 1}, nil
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	out := &client.QueryResult{}
	for rows.Next() {
		cells := make([]interface{}, len(columns))
		into := make([]interface{}, len(columns))
		for i := range cells {
			into[i] = &cells[i]
		}
		if err := rows.Scan(into...); err != nil {
			return nil, err
		}
		for i, cell := range cells {
			// The rqlite client hands back strings; []byte would compare
			// unequal to everything this package looks for.
			if b, ok := cell.([]byte); ok {
				cells[i] = string(b)
			}
		}
		out.Rows = append(out.Rows, cells)
	}
	out.Count = int64(len(out.Rows))
	return out, rows.Err()
}

func isSelect(query string) bool {
	for _, c := range query {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case 'S', 's':
			return true
		default:
			return false
		}
	}
	return false
}

type sqliteNet struct {
	client.NetworkClient
	db *sqliteDatabase
}

func (n *sqliteNet) Database() client.DatabaseClient { return n.db }

type sqliteRqlite struct {
	rqlite.Client
	db *sql.DB
}

func (r *sqliteRqlite) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return r.db.ExecContext(ctx, query, args...)
}

// realRegistry is a migrated registry with one namespace in it.
func realRegistry(t *testing.T) (*Service, *sqliteDatabase, interface{}) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO namespaces(id, name) VALUES (10, 'anchat')`); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	wrapped := &sqliteDatabase{db: db}
	s := createTestService(t)
	s.orm = &sqliteNet{db: wrapped}
	s.apiKeyORM = nil
	s.SetRqliteClient(&sqliteRqlite{db: db})
	return s, wrapped, int64(10)
}

func TestGrantIn_againstTheRealSchema(t *testing.T) {
	s, db, nsID := realRegistry(t)
	ctx := context.Background()

	if err := s.Grant(ctx, GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xTeammate",
		Role: RoleRuntime, CreatedBy: "0xowner",
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	grant, err := s.GrantIn(ctx, db, nsID, PrincipalWallet, "0xteammate")
	if err != nil {
		t.Fatalf("a live grant was not found: %v", err)
	}
	if grant.Role != RoleRuntime {
		t.Errorf("role %q", grant.Role)
	}

	t.Run("a revoked grant does not authorise", func(t *testing.T) {
		if _, err := db.db.Exec(`UPDATE grants SET revoked_at = datetime('now')`); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		defer db.db.Exec(`UPDATE grants SET revoked_at = NULL`)

		if _, err := s.GrantIn(ctx, db, nsID, PrincipalWallet, "0xteammate"); !errors.Is(err, ErrNotAMember) {
			t.Errorf("a revoked grant authorised: %v", err)
		}
	})

	t.Run("an expired grant does not authorise", func(t *testing.T) {
		past := time.Now().Add(-time.Hour).UTC().Format(sqliteTime)
		if _, err := db.db.Exec(`UPDATE grants SET expires_at = ?`, past); err != nil {
			t.Fatalf("expire: %v", err)
		}
		defer db.db.Exec(`UPDATE grants SET expires_at = NULL`)

		if _, err := s.GrantIn(ctx, db, nsID, PrincipalWallet, "0xteammate"); !errors.Is(err, ErrNotAMember) {
			t.Errorf("an expired grant authorised: %v", err)
		}
	})

	t.Run("a disabled principal does not authorise", func(t *testing.T) {
		if _, err := db.db.Exec(`UPDATE principals SET disabled_at = datetime('now')`); err != nil {
			t.Fatalf("disable: %v", err)
		}
		defer db.db.Exec(`UPDATE principals SET disabled_at = NULL`)

		if _, err := s.GrantIn(ctx, db, nsID, PrincipalWallet, "0xteammate"); !errors.Is(err, ErrNotAMember) {
			t.Errorf("a disabled principal authorised: %v", err)
		}
	})

	t.Run("a grant in another namespace does not authorise", func(t *testing.T) {
		if _, err := db.db.Exec(`INSERT INTO namespaces(id, name) VALUES (11, 'other')`); err != nil {
			t.Fatalf("create namespace: %v", err)
		}
		if _, err := s.GrantIn(ctx, db, int64(11), PrincipalWallet, "0xteammate"); !errors.Is(err, ErrNotAMember) {
			t.Errorf("a grant in one namespace authorised in another: %v", err)
		}
	})
}

// ListMembers carries the same predicates, and a member list that shows revoked
// or expired grants is a list nobody can act on.
func TestListMembers_againstTheRealSchema(t *testing.T) {
	s, db, _ := realRegistry(t)
	ctx := context.Background()

	for wallet, role := range map[string]Role{"0xadmin": RoleAdmin, "0xapp": RoleRuntime, "0xguest": RoleReader} {
		if err := s.Grant(ctx, GrantRequest{
			Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: wallet, Role: role,
		}); err != nil {
			t.Fatalf("grant %s: %v", wallet, err)
		}
	}

	members, err := s.ListMembers(ctx, "anchat")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("%d members, want 3", len(members))
	}
	// Strongest first, so the list reads as a hierarchy.
	if members[0].Role != RoleAdmin || members[2].Role != RoleReader {
		t.Errorf("members are not ordered by role: %v, %v, %v", members[0].Role, members[1].Role, members[2].Role)
	}

	if _, err := db.db.Exec(`UPDATE grants SET revoked_at = datetime('now') WHERE role = 'admin'`); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	members, err = s.ListMembers(ctx, "anchat")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(members) != 2 {
		t.Errorf("%d members after a revocation, want 2", len(members))
	}
}

// giveOwner records an owner grant the way namespace creation does. Signing in
// no longer writes one — that is the whole of bug-393 — so a test that needs an
// owned namespace has to say so.
func giveOwner(t *testing.T, s *Service, db client.DatabaseClient, nsID interface{}, wallet string) {
	t.Helper()
	ctx := context.Background()
	owner := NormalizeWallet(wallet)
	principalID, err := s.ensurePrincipal(ctx, db, PrincipalWallet, owner, "", "test")
	if err != nil {
		t.Fatalf("record the principal: %v", err)
	}
	if _, err := db.Query(client.WithInternalAuth(ctx),
		"INSERT INTO grants(principal_id, namespace_id, role, created_by) VALUES (?, ?, 'owner', 'test')",
		principalID, nsID); err != nil {
		t.Fatalf("record the owner grant: %v", err)
	}
}

// The single-owner invariant is the database's, not the code's: a second owner
// grant has to fail against the partial unique index migration 050 carries.
func TestOwnerGrant_isSingleAgainstTheRealSchema(t *testing.T) {
	s, db, nsID := realRegistry(t)
	ctx := context.Background()

	giveOwner(t, s, db, nsID, "0xCreator")

	if err := s.RequireNamespaceMember(ctx, "0xcreator", "anchat"); err != nil {
		t.Errorf("the owner was refused on their own namespace: %v", err)
	}

	var owned *ErrNamespaceOwnedByAnother
	if err := s.RequireNamespaceMember(ctx, "0xsquatter", "anchat"); !errors.As(err, &owned) {
		t.Fatalf("a second wallet was let in: %v", err)
	}

	owner, err := s.OwnerOf(ctx, "anchat")
	if err != nil || owner != "0xcreator" {
		t.Errorf("owner is %q, %v", owner, err)
	}
	if n, err := s.CountNamespacesOwnedBy(ctx, "0xCREATOR"); err != nil || n != 1 {
		t.Errorf("the owner owns %d namespaces, %v", n, err)
	}
}

// Signing in used to claim an unowned namespace, which is how `default` ended
// up belonging to whichever wallet reached it first on each cluster.
func TestRequireNamespaceMember_doesNotClaimAnUnownedNamespace(t *testing.T) {
	s, db, nsID := realRegistry(t)
	ctx := context.Background()

	err := s.RequireNamespaceMember(ctx, "0xpasserby", "anchat")
	if !errors.Is(err, ErrNamespaceUnowned) {
		t.Fatalf("an unowned namespace answered %v, want ErrNamespaceUnowned", err)
	}

	if owner, err := s.ownerOf(ctx, db, nsID); err != nil || owner != "" {
		t.Errorf("signing in wrote an owner: %q, %v", owner, err)
	}
}

// The lobby needs no grant and is given none: it is where a wallet stands
// before it owns anything, and the one thing it reaches is creating a
// namespace.
func TestRequireNamespaceMember_theLobbyNeedsNoGrant(t *testing.T) {
	s, db, _ := realRegistry(t)
	ctx := context.Background()
	// Migration 001 creates it, with no owner.
	var lobbyID int64
	if err := db.db.QueryRow(`SELECT id FROM namespaces WHERE name = ?`, LobbyNamespace).Scan(&lobbyID); err != nil {
		t.Fatalf("the lobby namespace is not in the schema: %v", err)
	}

	for _, wallet := range []string{"0xfirst", "0xsecond", "0xthird"} {
		if err := s.RequireNamespaceMember(ctx, wallet, LobbyNamespace); err != nil {
			t.Fatalf("%s was refused the lobby: %v", wallet, err)
		}
	}

	var grants int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM grants WHERE namespace_id = ?`, lobbyID).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 0 {
		t.Errorf("%d grants were written in the lobby; signing in there gives a session and nothing else", grants)
	}

	// And there is nothing to mint there. Which refusal it is matters: falling
	// through to "you hold no grant here" would be true of the lobby by
	// accident rather than by decision, and would start minting keys the
	// moment anything wrote a grant there.
	_, err := s.GetOrCreateAPIKey(ctx, "0xfirst", LobbyNamespace)
	if !errors.Is(err, ErrNoKeysInLobby) {
		t.Errorf("minting in the lobby answered %v, want ErrNoKeysInLobby", err)
	}
}

// A key minted by a login used to carry admin whatever the caller's role was,
// so a reader or a runtime member was handed the full control plane by the act
// of signing in.
func TestGetOrCreateAPIKey_carriesTheCallersOwnRole(t *testing.T) {
	s, db, nsID := realRegistry(t)
	ctx := context.Background()
	giveOwner(t, s, db, nsID, "0xowner")

	if err := s.Grant(ctx, GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xapp",
		Role: RoleRuntime, CreatedBy: "0xowner",
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := s.Grant(ctx, GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xguest",
		Role: RoleReader, CreatedBy: "0xowner",
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	if _, err := s.GetOrCreateAPIKey(ctx, "0xowner", "anchat"); err != nil {
		t.Fatalf("the owner could not mint a key: %v", err)
	}
	if _, err := s.GetOrCreateAPIKey(ctx, "0xapp", "anchat"); err != nil {
		t.Fatalf("a runtime member could not mint a key: %v", err)
	}

	scopes := map[string]string{}
	rows, err := db.db.Query(`SELECT scopes FROM api_keys ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			t.Fatal(err)
		}
		got = append(got, scope)
	}
	if len(got) != 2 {
		t.Fatalf("%d keys, want 2: %v", len(got), got)
	}
	scopes["owner"], scopes["runtime"] = got[0], got[1]

	if !ParseScopes(scopes["owner"]).IsAdmin() {
		t.Errorf("the owner's key holds %q, want admin", scopes["owner"])
	}
	if ParseScopes(scopes["runtime"]).IsAdmin() {
		t.Errorf("a runtime member's key holds admin (%q); signing in handed them the control plane", scopes["runtime"])
	}
	if scopes["runtime"] == "" {
		t.Error("a runtime member's key holds nothing, so it denies everywhere")
	}

	// A reader holds nothing, so there is nothing to mint.
	if _, err := s.GetOrCreateAPIKey(ctx, "0xguest", "anchat"); err == nil {
		t.Error("a reader was handed a key")
	}
}

func TestTransferOwnership_againstTheRealSchema(t *testing.T) {
	s, db, nsID := realRegistry(t)
	ctx := context.Background()

	giveOwner(t, s, db, nsID, "0xcreator")
	if err := s.TransferOwnership(ctx, "anchat", "0xcreator", "0xnext"); err != nil {
		t.Fatalf("transfer: %v", err)
	}

	owner, err := s.OwnerOf(ctx, "anchat")
	if err != nil || owner != "0xnext" {
		t.Fatalf("owner is %q, %v", owner, err)
	}

	// The namespace was never ownerless, so the partial unique index held
	// throughout: exactly one live owner row, and the previous owner kept a
	// place in the namespace.
	var owners int
	if err := db.db.QueryRow(
		`SELECT COUNT(*) FROM grants WHERE role = 'owner' AND revoked_at IS NULL`).Scan(&owners); err != nil {
		t.Fatalf("count owners: %v", err)
	}
	if owners != 1 {
		t.Errorf("%d live owner grants after a transfer", owners)
	}
	previous, err := s.GrantIn(ctx, db, nsID, PrincipalWallet, "0xcreator")
	if err != nil || previous.Role != RoleAdmin {
		t.Errorf("the previous owner holds %v, %v", previous, err)
	}
}

// Granting the same thing twice must be a no-op rather than two rows one
// revoke cannot clear.
func TestGrant_isIdempotentAgainstTheRealSchema(t *testing.T) {
	s, db, _ := realRegistry(t)
	ctx := context.Background()

	req := GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xteammate", Role: RoleRuntime,
	}
	for i := 0; i < 3; i++ {
		if err := s.Grant(ctx, req); err != nil {
			t.Fatalf("grant %d: %v", i+1, err)
		}
	}

	var n int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM grants WHERE revoked_at IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if n != 1 {
		t.Errorf("%d live grants after granting the same role three times", n)
	}

	if err := s.RevokeGrant(ctx, "anchat", PrincipalWallet, "0xteammate", "0xowner"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM grants WHERE revoked_at IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if n != 0 {
		t.Errorf("%d grants survived the revocation", n)
	}
}

// --- keys, against the real schema ----------------------------------------

// Every key has an expiry now. A key minted once used to be a bearer token that
// worked until somebody remembered to revoke it, which is a thing nobody
// remembers.
func TestIssueScopedKey_expiresAndCarriesItsGrants(t *testing.T) {
	s, db, _ := realRegistry(t)
	ctx := context.Background()

	raw, id, err := s.IssueScopedKey(ctx, "anchat", "invoke,storage", KeyOptions{Label: "app"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := ParseKey(raw); err != nil {
		t.Errorf("the key handed back is not a key: %v", err)
	}

	var scopes string
	var got time.Time
	if err := db.db.QueryRow(
		`SELECT scopes, expires_at FROM api_keys WHERE id = ?`, id).Scan(&scopes, &got); err != nil {
		t.Fatalf("read the key back: %v", err)
	}
	if scopes != "invoke,storage" {
		t.Errorf("scopes %q", scopes)
	}
	want := time.Now().Add(KeyLifetime).UTC()
	if got.Sub(want) > time.Minute || want.Sub(got) > time.Minute {
		t.Errorf("expiry %s, want about %s", got, want)
	}
}

func TestIssueScopedKey_refusals(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()

	if _, _, err := s.IssueScopedKey(ctx, "anchat", "", KeyOptions{}); err == nil {
		t.Error("a key with no grants was minted; an empty scope set denies, so it would be inert and confusing")
	}
	if _, _, err := s.IssueScopedKey(ctx, "anchat", "admin", KeyOptions{Lifetime: 2 * MaxKeyLifetime}); err == nil {
		t.Error("a key was minted with a lifetime past the ceiling")
	}
	if _, _, err := s.IssueScopedKey(ctx, "anchat", "admin", KeyOptions{Lifetime: -time.Hour}); err == nil {
		t.Error("a key was minted with a negative lifetime")
	}
}

// Rotating by minting a successor and revoking the original in the same breath
// is an outage. The overlap is the window in which to deploy the new key.
func TestRotateKey_keepsTheOriginalWorkingForTheOverlap(t *testing.T) {
	s, db, _ := realRegistry(t)
	ctx := context.Background()

	original, id, err := s.IssueScopedKey(ctx, "anchat", "invoke,storage", KeyOptions{Label: "app"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	successor, newID, err := s.RotateKey(ctx, "anchat", id, RotateOptions{Overlap: 48 * time.Hour})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if successor == original {
		t.Fatal("rotation handed back the same key")
	}

	var scopes, label string
	var rotatedFrom int64
	if err := db.db.QueryRow(
		`SELECT scopes, name, rotated_from FROM api_keys WHERE id = ?`, newID).Scan(&scopes, &label, &rotatedFrom); err != nil {
		t.Fatalf("read the successor: %v", err)
	}
	// A rotation is the same credential with new material. Quietly widening or
	// narrowing it would make rotating something people avoid.
	if scopes != "invoke,storage" || label != "app" {
		t.Errorf("the successor has scopes %q and label %q", scopes, label)
	}
	if rotatedFrom != id {
		t.Errorf("rotated_from is %d, want %d — the succession is what makes two live keys legible", rotatedFrom, id)
	}

	// The original is shortened, not revoked: whatever is deployed with it
	// keeps working until the overlap runs out.
	var revoked interface{}
	var got time.Time
	if err := db.db.QueryRow(
		`SELECT revoked_at, expires_at FROM api_keys WHERE id = ?`, id).Scan(&revoked, &got); err != nil {
		t.Fatalf("read the original: %v", err)
	}
	if revoked != nil {
		t.Error("the original was revoked, so anything deployed with it stopped the moment the successor existed")
	}
	if want := time.Now().Add(48 * time.Hour).UTC(); got.Sub(want) > time.Minute || want.Sub(got) > time.Minute {
		t.Errorf("the original expires %s, want about %s", got, want)
	}
}

// A key already expiring inside the overlap must not be given more life by
// being rotated.
func TestRotateKey_onlyEverShortens(t *testing.T) {
	s, db, _ := realRegistry(t)
	ctx := context.Background()

	_, id, err := s.IssueScopedKey(ctx, "anchat", "admin", KeyOptions{Lifetime: time.Hour})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, _, err := s.RotateKey(ctx, "anchat", id, RotateOptions{Overlap: 30 * 24 * time.Hour}); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	var got time.Time
	if err := db.db.QueryRow(`SELECT expires_at FROM api_keys WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("read the original: %v", err)
	}
	if got.After(time.Now().Add(2 * time.Hour).UTC()) {
		t.Errorf("rotating extended the original's life to %s", got)
	}
}

func TestRotateKey_refusals(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()

	_, id, err := s.IssueScopedKey(ctx, "anchat", "admin", KeyOptions{})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, _, err := s.RotateKey(ctx, "anchat", 999, RotateOptions{}); err == nil {
		t.Error("a key that does not exist was rotated")
	}
	if _, _, err := s.RotateKey(ctx, "anchat", id, RotateOptions{Overlap: 2 * MaxRotationOverlap}); err == nil {
		t.Error("an overlap past the ceiling was accepted; that is two live keys, not a rotation")
	}
	if err := s.RevokeKey(ctx, "anchat", id); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := s.RotateKey(ctx, "anchat", id, RotateOptions{}); err == nil {
		t.Error("a revoked key was rotated")
	}
}

// ListKeys shows the expiry and the succession, or a rotation in progress reads
// as two unrelated keys.
func TestListKeys_showsExpiryAndSuccession(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()

	_, id, err := s.IssueScopedKey(ctx, "anchat", "admin", KeyOptions{Label: "ci"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, _, err := s.RotateKey(ctx, "anchat", id, RotateOptions{}); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	keys, err := s.ListKeys(ctx, "anchat")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("%d keys, want 2 — both are live during the overlap", len(keys))
	}
	for _, k := range keys {
		if k.ExpiresAt == "" {
			t.Errorf("key %d has no expiry", k.ID)
		}
	}
	if keys[1].RotatedFrom != keys[0].ID {
		t.Errorf("the successor says it came from %d, want %d", keys[1].RotatedFrom, keys[0].ID)
	}
}
