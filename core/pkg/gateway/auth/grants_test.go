package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// The roles are the answer to "a second person on this team". What matters is
// that they are not all the same thing: the old model had one row meaning
// owner, so everybody who could do anything could do everything.

func TestRole_scopes(t *testing.T) {
	if !RoleOwner.Scopes().IsAdmin() || !RoleAdmin.Scopes().IsAdmin() {
		t.Error("an owner or an admin does not reach the control plane")
	}

	runtime := RoleRuntime.Scopes()
	if runtime.IsAdmin() {
		t.Error("a runtime member reaches the control plane, which is the whole thing the roles exist to stop")
	}
	for _, grant := range []string{ScopeInvoke, ScopeStorage, ScopePush, ScopeWebRTC, ScopeProxy, ScopePubsub, ScopeCache} {
		if !runtime.Has(grant) {
			t.Errorf("a runtime member does not hold %q, so an application cannot run", grant)
		}
	}

	reader := RoleReader.Scopes()
	if reader.IsAdmin() || reader.Has(ScopeStorage) || len(reader) != 0 {
		t.Errorf("a reader holds %v; it is a member with no grant", reader.Canonical())
	}

	// A row this binary does not understand — written by a newer gateway —
	// grants nothing rather than defaulting to something.
	if s := Role("superuser").Scopes(); len(s) != 0 {
		t.Errorf("an unknown role granted %v", s.Canonical())
	}
}

func TestRole_atLeast(t *testing.T) {
	if !RoleOwner.AtLeast(RoleAdmin) || !RoleAdmin.AtLeast(RoleRuntime) || !RoleRuntime.AtLeast(RoleReader) {
		t.Error("the hierarchy does not hold downwards")
	}
	if RoleRuntime.AtLeast(RoleAdmin) || RoleAdmin.AtLeast(RoleOwner) || RoleReader.AtLeast(RoleRuntime) {
		t.Error("a weaker role satisfied a stronger requirement")
	}
	if Role("nonsense").AtLeast(RoleReader) {
		t.Error("an unknown role satisfied the weakest requirement")
	}
}

func TestParseRole(t *testing.T) {
	for _, in := range []string{"owner", "OWNER", " admin ", "runtime", "reader"} {
		if _, err := ParseRole(in); err != nil {
			t.Errorf("ParseRole(%q): %v", in, err)
		}
	}
	// `developer` is deliberately not a role yet: every control-plane route
	// requires the single admin grant, so it would be an alias for admin
	// claiming a boundary that is not there.
	for _, in := range []string{"", "developer", "superuser"} {
		var unknown *ErrUnknownRole
		if _, err := ParseRole(in); !errors.As(err, &unknown) {
			t.Errorf("ParseRole(%q) = %v, want an unknown-role error", in, err)
		}
	}
}

// A grant narrowed to a resource authorises nothing until the data plane can
// enforce the selector. Handing over the whole role would turn "may write to
// storage:avatars/*" into "may write to all storage".
func TestGrant_aResourceScopedGrantAuthorisesNothingYet(t *testing.T) {
	whole := Grant{Role: RoleRuntime}
	if !whole.Scopes().Has(ScopePubsub) {
		t.Fatal("a whole-role runtime grant does not hold pubsub")
	}

	narrowed := Grant{Role: RoleRuntime, Resource: "pubsub:topic=chat.*"}
	if len(narrowed.Scopes()) != 0 {
		t.Errorf("a grant narrowed to a selector authorised %v", narrowed.Scopes().Canonical())
	}
}

func TestRoleForScopes(t *testing.T) {
	if got := RoleForScopes("admin"); got != RoleAdmin {
		t.Errorf("an admin key maps to %q", got)
	}
	if got := RoleForScopes("invoke,storage"); got != RoleRuntime {
		t.Errorf("a runtime key maps to %q", got)
	}
	// An empty scope set denies (migration 043 wrote the grant that was being
	// inferred), so it is not a control-plane credential.
	if got := RoleForScopes(""); got != RoleRuntime {
		t.Errorf("a key with no scopes maps to %q, want the weaker role", got)
	}
}

// --- the database-backed half ---------------------------------------------

// grantsDB is principals and grants, enough of them to answer the statements
// the membership paths issue.
type grantsDB struct {
	client.DatabaseClient

	principals map[int64]string // id -> identifier
	types      map[int64]string // id -> type
	rows       []*grantRow
	nextID     int64

	failWrite bool
}

type grantRow struct {
	principalID int64
	namespaceID string
	role        string
	resource    string
	expiresAt   string
	createdBy   string
	revoked     bool
}

func newGrantsDB() *grantsDB {
	return &grantsDB{principals: map[int64]string{}, types: map[int64]string{}}
}

func (d *grantsDB) live(nsID, ptype, identifier string) *grantRow {
	for _, row := range d.rows {
		if row.revoked || row.namespaceID != nsID {
			continue
		}
		if d.principals[row.principalID] != identifier || d.types[row.principalID] != ptype {
			continue
		}
		if row.expiresAt != "" && row.expiresAt < time.Now().UTC().Format(sqliteTime) {
			continue
		}
		return row
	}
	return nil
}

func (d *grantsDB) Query(_ context.Context, query string, args ...interface{}) (*client.QueryResult, error) {
	switch {
	case strings.Contains(query, "INSERT OR IGNORE INTO namespaces"):
		return &client.QueryResult{Count: 1}, nil
	case strings.Contains(query, "SELECT id FROM namespaces"):
		return rows(int64(1)), nil

	case strings.Contains(query, "INSERT OR IGNORE INTO principals"):
		ptype, identifier := getStringVal(args[0]), getStringVal(args[1])
		for id, name := range d.principals {
			if name == identifier && d.types[id] == ptype {
				return &client.QueryResult{Count: 1}, nil
			}
		}
		d.nextID++
		d.principals[d.nextID] = identifier
		d.types[d.nextID] = ptype
		return &client.QueryResult{Count: 1}, nil

	case strings.Contains(query, "SELECT id FROM principals"):
		ptype, identifier := getStringVal(args[0]), getStringVal(args[1])
		for id, name := range d.principals {
			if name == identifier && d.types[id] == ptype {
				return rows(id), nil
			}
		}
		return &client.QueryResult{}, nil

	case strings.Contains(query, "SELECT g.role, g.resource"):
		row := d.live(getStringVal(args[0]), getStringVal(args[1]), getStringVal(args[2]))
		if row == nil {
			return &client.QueryResult{}, nil
		}
		return &client.QueryResult{Count: 1, Rows: [][]interface{}{
			{row.role, row.resource, row.expiresAt, "", row.createdBy, ""},
		}}, nil

	case strings.Contains(query, "SELECT p.type, p.identifier"):
		nsID := getStringVal(args[0])
		out := &client.QueryResult{}
		for _, row := range d.rows {
			if row.revoked || row.namespaceID != nsID {
				continue
			}
			if row.expiresAt != "" && row.expiresAt < time.Now().UTC().Format(sqliteTime) {
				continue
			}
			out.Rows = append(out.Rows, []interface{}{
				d.types[row.principalID], d.principals[row.principalID], "",
				row.role, row.resource, row.expiresAt, "", row.createdBy,
			})
		}
		out.Count = int64(len(out.Rows))
		return out, nil

	case strings.Contains(query, "SELECT p.identifier FROM grants"):
		nsID := getStringVal(args[0])
		for _, row := range d.rows {
			if !row.revoked && row.namespaceID == nsID && row.role == string(RoleOwner) {
				return rows(d.principals[row.principalID]), nil
			}
		}
		return &client.QueryResult{}, nil

	case strings.Contains(query, "INSERT INTO grants"), strings.Contains(query, "INSERT OR IGNORE INTO grants"):
		if d.failWrite {
			return nil, errString("grant write failed")
		}
		principalID := toInt64(args[0])
		nsID := getStringVal(args[1])
		role := getStringVal(args[2])
		if strings.Contains(query, "'owner'") {
			role = string(RoleOwner)
		}
		resource, expires, createdBy := "", "", ""
		if len(args) > 5 {
			resource, expires, createdBy = getStringVal(args[3]), getStringVal(args[4]), getStringVal(args[5])
		}
		// The unique index on (principal, namespace, role, resource).
		for _, row := range d.rows {
			if !row.revoked && row.principalID == principalID && row.namespaceID == nsID &&
				row.role == role && row.resource == resource {
				return nil, errString("UNIQUE constraint failed: grants")
			}
		}
		d.rows = append(d.rows, &grantRow{
			principalID: principalID, namespaceID: nsID, role: role,
			resource: resource, expiresAt: expires, createdBy: createdBy,
		})
		return &client.QueryResult{Count: 1}, nil
	}
	return nil, errString("unexpected sql: " + query)
}

// grantsRqlite is the same store behind the lower-level client, which is what
// the paths that need an affected-row count use.
type grantsRqlite struct {
	rqlite.Client
	db *grantsDB
}

func (r *grantsRqlite) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return r.db.exec(ctx, query, args...)
}

func (d *grantsDB) exec(_ context.Context, query string, args ...interface{}) (sql.Result, error) {
	switch {
	case strings.Contains(query, "UPDATE grants SET revoked_at"):
		nsID, ptype, identifier := getStringVal(args[0]), getStringVal(args[1]), getStringVal(args[2])
		n := 0
		for _, row := range d.rows {
			if !row.revoked && row.namespaceID == nsID &&
				d.principals[row.principalID] == identifier && d.types[row.principalID] == ptype {
				row.revoked = true
				n++
			}
		}
		return grantsResult(n), nil

	case strings.Contains(query, "UPDATE grants SET principal_id"):
		newOwner, nsID := toInt64(args[0]), getStringVal(args[2])
		n := 0
		for _, row := range d.rows {
			if !row.revoked && row.namespaceID == nsID && row.role == string(RoleOwner) {
				row.principalID = newOwner
				n++
			}
		}
		return grantsResult(n), nil
	}
	return grantsResult(0), nil
}

type grantsResult int64

func (r grantsResult) LastInsertId() (int64, error) { return 0, nil }
func (r grantsResult) RowsAffected() (int64, error) { return int64(r), nil }

type grantsNet struct {
	client.NetworkClient
	db *grantsDB
}

func (n *grantsNet) Database() client.DatabaseClient { return n.db }

func grantsService(t *testing.T, db *grantsDB) *Service {
	t.Helper()
	s := createTestService(t)
	s.orm = &grantsNet{db: db}
	s.apiKeyORM = nil
	s.SetRqliteClient(&grantsRqlite{db: db})
	return s
}

func TestGrant_recordsARoleAndListsIt(t *testing.T) {
	db := newGrantsDB()
	s := grantsService(t, db)

	if err := s.Grant(context.Background(), GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xTeammate",
		Role: RoleRuntime, DisplayName: "Sam", CreatedBy: "0xowner",
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	members, err := s.ListMembers(context.Background(), "anchat")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("%d members, want 1", len(members))
	}
	if members[0].Identifier != "0xteammate" {
		t.Errorf("identifier %q; a wallet in two capitalisations is two principals", members[0].Identifier)
	}
	if members[0].Role != RoleRuntime {
		t.Errorf("role %q", members[0].Role)
	}
}

// Ownership is established by creating the namespace and moved by transferring
// it. Every path that could make somebody an owner is how the takeover worked.
func TestGrant_refusesToHandOutOwnership(t *testing.T) {
	s := grantsService(t, newGrantsDB())

	err := s.Grant(context.Background(), GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xsquatter",
		Role: RoleOwner,
	})
	if err == nil {
		t.Fatal("an owner grant was handed out")
	}
	if !strings.Contains(err.Error(), "transferring") {
		t.Errorf("the error does not say what to do instead: %v", err)
	}
}

func TestGrant_validatesTheSelector(t *testing.T) {
	s := grantsService(t, newGrantsDB())

	for _, tc := range []struct {
		name, resource string
		role           Role
	}{
		{"not a selector at all", "chat.*", RoleRuntime},
		{"an unknown domain", "filesystem:/etc/passwd", RoleRuntime},
		{"an empty pattern", "pubsub:", RoleRuntime},
		{"a domain the role cannot reach", "pubsub:topic=chat.*", RoleReader},
		// A selector nothing applies would show as a narrowed grant and
		// authorise nothing, which is the worse of the two ways to be wrong.
		{"a domain the data path cannot enforce yet", "storage:avatars/*", RoleRuntime},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Grant(context.Background(), GrantRequest{
				Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xteammate",
				Role: tc.role, Resource: tc.resource,
			})
			if err == nil {
				t.Errorf("accepted %q", tc.resource)
			}
		})
	}

	if err := s.Grant(context.Background(), GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xteammate",
		Role: RoleRuntime, Resource: "pubsub:topic=chat.*",
	}); err != nil {
		t.Errorf("a valid selector on a role that holds the grant was refused: %v", err)
	}
}

func TestGrant_refusesAnExpiryInThePast(t *testing.T) {
	s := grantsService(t, newGrantsDB())

	err := s.Grant(context.Background(), GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xteammate",
		Role: RoleRuntime, ExpiresAt: time.Now().Add(-time.Hour),
	})
	if err == nil {
		t.Fatal("a grant that had already expired was written")
	}
}

// An expired grant is not a grant. Without this the expiry column is decoration.
func TestGrantIn_ignoresAnExpiredGrant(t *testing.T) {
	db := newGrantsDB()
	s := grantsService(t, db)

	if err := s.Grant(context.Background(), GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xteammate",
		Role: RoleRuntime, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := s.GrantIn(context.Background(), db, "1", PrincipalWallet, "0xteammate"); err != nil {
		t.Fatalf("a live grant was not found: %v", err)
	}

	// Move it into the past the way time would.
	db.rows[0].expiresAt = time.Now().Add(-time.Minute).UTC().Format(sqliteTime)
	if _, err := s.GrantIn(context.Background(), db, "1", PrincipalWallet, "0xteammate"); !errors.Is(err, ErrNotAMember) {
		t.Errorf("an expired grant still authorised: %v", err)
	}
}

func TestRevokeGrant(t *testing.T) {
	db := newGrantsDB()
	s := grantsService(t, db)

	if err := s.Grant(context.Background(), GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xteammate", Role: RoleAdmin,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := s.RevokeGrant(context.Background(), "anchat", PrincipalWallet, "0xTeammate", "0xowner"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.GrantIn(context.Background(), db, "1", PrincipalWallet, "0xteammate"); !errors.Is(err, ErrNotAMember) {
		t.Errorf("a revoked grant still authorised: %v", err)
	}

	// The row survives with revoked_at set: what somebody was once allowed to
	// do is what an incident asks about.
	if len(db.rows) != 1 || !db.rows[0].revoked {
		t.Error("the grant was deleted rather than revoked, so there is no record of it")
	}

	if err := s.RevokeGrant(context.Background(), "anchat", PrincipalWallet, "0xstranger", "0xowner"); !errors.Is(err, ErrNotAMember) {
		t.Errorf("revoking a grant nobody holds reported %v", err)
	}
}

// A namespace with no owner is claimable by whoever signs in to it next, which
// is the bug the single-owner invariant exists to prevent.
func TestRevokeGrant_refusesToRemoveTheOwner(t *testing.T) {
	db := newGrantsDB()
	s := grantsService(t, db)
	seedOwner(t, s, db, "0xowner")

	err := s.RevokeGrant(context.Background(), "anchat", PrincipalWallet, "0xowner", "0xowner")
	if !errors.Is(err, ErrOwnerCannotBeRemoved) {
		t.Fatalf("got %v, want a refusal to unown the namespace", err)
	}
}

func TestTransferOwnership(t *testing.T) {
	db := newGrantsDB()
	s := grantsService(t, db)
	seedOwner(t, s, db, "0xowner")

	if err := s.TransferOwnership(context.Background(), "anchat", "0xOwner", "0xNext"); err != nil {
		t.Fatalf("transfer: %v", err)
	}

	next, err := s.GrantIn(context.Background(), db, "1", PrincipalWallet, "0xnext")
	if err != nil || next.Role != RoleOwner {
		t.Fatalf("the new owner holds %v, %v", next, err)
	}

	// Handing a project over must not lock you out of it in the same instant.
	previous, err := s.GrantIn(context.Background(), db, "1", PrincipalWallet, "0xowner")
	if err != nil {
		t.Fatalf("the previous owner holds nothing: %v", err)
	}
	if previous.Role != RoleAdmin {
		t.Errorf("the previous owner holds %q, want admin", previous.Role)
	}
}

func TestTransferOwnership_refusals(t *testing.T) {
	db := newGrantsDB()
	s := grantsService(t, db)
	seedOwner(t, s, db, "0xowner")

	var owned *ErrNamespaceOwnedByAnother
	if err := s.TransferOwnership(context.Background(), "anchat", "0xsomebodyelse", "0xnext"); !errors.As(err, &owned) {
		t.Errorf("a wallet that does not own the namespace transferred it: %v", err)
	}
	if err := s.TransferOwnership(context.Background(), "anchat", "0xowner", "0xOWNER"); err == nil {
		t.Error("a namespace was transferred to the wallet that already owns it")
	}
	if err := s.TransferOwnership(context.Background(), "anchat", "0xowner", "  "); err == nil {
		t.Error("a namespace was transferred to nobody")
	}
}

// seedOwner writes the owner grant the way creating a namespace does.
func seedOwner(t *testing.T, s *Service, db *grantsDB, wallet string) {
	t.Helper()
	if err := s.writeGrant(context.Background(), GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: wallet,
		Role: RoleOwner, CreatedBy: "namespace creation",
	}); err != nil {
		t.Fatalf("seed the owner: %v", err)
	}
}
