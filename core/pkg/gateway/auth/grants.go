package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// Who may do what in a namespace.
//
// This used to be one boolean: a row in namespace_ownership meant owner, no row
// meant refused. A team of two had one setting between them — hand over the
// whole namespace or nothing — and a service account could only be modelled as
// another owner. There was nowhere to put "this wallet may deploy but not mint
// keys", nowhere to put an expiry, and nowhere to record who granted what.
//
// A principal is who: a wallet, or a service account (an API key). A grant is
// what they may do in one namespace, as a named role, optionally narrowed to a
// resource and optionally expiring. See migration 050.

// PrincipalType is the kind of thing a grant is held by.
//
// Only the two the platform can currently authenticate a request as. The epic
// names app, function and node as well; they are not here because a value
// nothing ever writes reads as a capability that exists, and adding one when it
// does is a line in this block and a line in the migration's comment.
type PrincipalType string

const (
	PrincipalWallet         PrincipalType = "wallet"
	PrincipalServiceAccount PrincipalType = "service_account"
)

// Role is what a principal may do.
//
// The vocabulary is deliberately short of what the epic proposes. `developer`
// — deployments, functions, database, keys, but not everything — cannot be
// expressed today: every control-plane route requires the single `admin` grant
// (see scopes.go), so a `developer` role would resolve to exactly the same
// scope set as `admin` and be a label claiming a boundary that is not there.
// It arrives when the control-plane vocabulary is split, which is the route
// policy work in Phase 2.
type Role string

const (
	// RoleOwner is the one principal a namespace belongs to. Exactly one per
	// namespace, enforced by a partial unique index rather than by a check the
	// code remembers to make. Only the owner may transfer ownership.
	RoleOwner Role = "owner"

	// RoleAdmin is the full control plane: deployments, functions, secrets,
	// keys, raw database. Everything the owner can do except being the owner.
	RoleAdmin Role = "admin"

	// RoleRuntime is the data plane: invoke, storage, push, webrtc, proxy,
	// pubsub, cache. What an application does, not what an operator does.
	RoleRuntime Role = "runtime"

	// RoleReader belongs to the namespace and holds no grant. It reaches the
	// routes that require none — whoami, namespace status — and nothing else.
	// It is how someone is listed as a member without being given anything.
	RoleReader Role = "reader"
)

// roleRank orders the roles so "at least this role" is a comparison rather than
// a list of cases. It is not the scope set: RoleOwner and RoleAdmin resolve to
// the same grants, and differ in what they may do to the grants themselves.
var roleRank = map[Role]int{
	RoleReader:  1,
	RoleRuntime: 2,
	RoleAdmin:   3,
	RoleOwner:   4,
}

// ErrUnknownRole names what was asked for and what exists.
type ErrUnknownRole struct{ Role string }

func (e *ErrUnknownRole) Error() string {
	return fmt.Sprintf("unknown role %q (valid: %s)", e.Role, strings.Join(AllRoles(), ", "))
}

// ErrNotAMember is returned when a principal holds no live grant in a namespace.
var ErrNotAMember = errors.New("this principal holds no grant in this namespace")

// ErrOwnerCannotBeRemoved is returned when something tries to revoke the owner's
// grant. A namespace with no owner is claimable by whoever signs in next, which
// is the bug the single-owner invariant exists to prevent — so ownership is
// transferred, never dropped.
var ErrOwnerCannotBeRemoved = errors.New("a namespace's owner cannot be removed; transfer ownership instead")

// AllRoles returns every role, weakest first.
func AllRoles() []string {
	out := make([]string, 0, len(roleRank))
	for r := range roleRank {
		out = append(out, string(r))
	}
	sort.Slice(out, func(i, j int) bool { return roleRank[Role(out[i])] < roleRank[Role(out[j])] })
	return out
}

// ParseRole reads a role name.
func ParseRole(raw string) (Role, error) {
	r := Role(strings.ToLower(strings.TrimSpace(raw)))
	if _, ok := roleRank[r]; !ok {
		return "", &ErrUnknownRole{Role: raw}
	}
	return r, nil
}

// AtLeast reports whether r is as strong as required.
func (r Role) AtLeast(required Role) bool {
	return roleRank[r] >= roleRank[required] && roleRank[r] > 0
}

// Scopes is the grant set this role resolves to.
//
// A resource-scoped grant does not come through here: see GrantScopes.
func (r Role) Scopes() ScopeSet {
	switch r {
	case RoleOwner, RoleAdmin:
		return ScopeSet{ScopeAdmin: {}}
	case RoleRuntime:
		return DataPlaneScopes()
	default:
		// RoleReader, and anything unrecognised. An unknown role granting
		// nothing is the only safe reading of a row this binary does not
		// understand — a newer gateway may have written a role this one has
		// never heard of.
		return ScopeSet{}
	}
}

// Grant is one principal's authority in one namespace.
type Grant struct {
	PrincipalType PrincipalType
	Identifier    string
	DisplayName   string
	Role          Role
	// Resource is the selector narrowing the role, or "" for the whole role.
	Resource  string
	ExpiresAt string
	CreatedAt string
	CreatedBy string
}

// Scopes is what this grant actually authorises.
//
// A grant narrowed to a resource authorises nothing until the data plane can
// enforce the selector. Returning the role's full scope set would turn "may
// write to storage:avatars/*" into "may write to all storage" — a narrower
// grant that is in fact the wide one, which is worse than refusing.
func (g Grant) Scopes() ScopeSet {
	if strings.TrimSpace(g.Resource) != "" {
		return ScopeSet{}
	}
	return g.Role.Scopes()
}

// sqliteTime is how a timestamp is written into a column the schema compares
// with datetime('now').
const sqliteTime = "2006-01-02 15:04:05"

// GrantIn returns the live grant a principal holds in a namespace.
//
// It is the read every authenticated request makes, so it is one query. A grant
// that is revoked, expired, or held by a disabled principal is not a grant.
func (s *Service) GrantIn(ctx context.Context, db client.DatabaseClient, nsID interface{}, ptype PrincipalType, identifier string) (*Grant, error) {
	if strings.TrimSpace(identifier) == "" {
		return nil, ErrNotAMember
	}
	res, err := db.Query(client.WithInternalAuth(ctx),
		`SELECT g.role, g.resource, g.expires_at, g.created_at, g.created_by, p.display_name
		   FROM grants AS g
		   JOIN principals AS p ON p.id = g.principal_id
		  WHERE g.namespace_id = ? AND p.type = ? AND p.identifier = ?
		    AND g.revoked_at IS NULL
		    AND p.disabled_at IS NULL
		    AND (g.expires_at IS NULL OR g.expires_at > datetime('now'))
		  ORDER BY g.id LIMIT 1`,
		nsID, string(ptype), identifier)
	if err != nil {
		return nil, fmt.Errorf("failed to read the grant for %s %q: %w", ptype, identifier, err)
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) < 6 {
		return nil, ErrNotAMember
	}
	row := res.Rows[0]
	return &Grant{
		PrincipalType: ptype,
		Identifier:    identifier,
		DisplayName:   getStringVal(row[5]),
		Role:          Role(getStringVal(row[0])),
		Resource:      getStringVal(row[1]),
		ExpiresAt:     getStringVal(row[2]),
		CreatedAt:     getStringVal(row[3]),
		CreatedBy:     getStringVal(row[4]),
	}, nil
}

// ensurePrincipal returns the id of a principal, creating it if this is the
// first time the platform has seen it.
func (s *Service) ensurePrincipal(ctx context.Context, db client.DatabaseClient, ptype PrincipalType, identifier, displayName, createdBy string) (interface{}, error) {
	internalCtx := client.WithInternalAuth(ctx)
	if _, err := db.Query(internalCtx,
		"INSERT OR IGNORE INTO principals(type, identifier, display_name, created_by) VALUES (?, ?, ?, ?)",
		string(ptype), identifier, displayName, createdBy,
	); err != nil {
		return nil, fmt.Errorf("failed to record the principal %s %q: %w", ptype, identifier, err)
	}
	res, err := db.Query(internalCtx,
		"SELECT id FROM principals WHERE type = ? AND identifier = ? LIMIT 1", string(ptype), identifier)
	if err != nil {
		return nil, fmt.Errorf("failed to read back the principal %s %q: %w", ptype, identifier, err)
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return nil, fmt.Errorf("the principal %s %q was written but could not be read back", ptype, identifier)
	}
	return res.Rows[0][0], nil
}

// GrantRequest is one grant to write.
type GrantRequest struct {
	Namespace     string
	PrincipalType PrincipalType
	Identifier    string
	DisplayName   string
	Role          Role
	// Resource is the optional selector. See selector.go for why a grant
	// carrying one authorises nothing yet.
	Resource  string
	ExpiresAt time.Time
	// CreatedBy is who is handing this out, for the record.
	CreatedBy string
}

// Grant records a role for a principal in a namespace.
//
// It refuses to write an owner grant: ownership is established by creating the
// namespace and moved by TransferOwnership, and every other path that used to
// be able to make somebody an owner is how the namespace-takeover bug worked.
func (s *Service) Grant(ctx context.Context, req GrantRequest) error {
	if req.Role == RoleOwner {
		return fmt.Errorf("ownership is not granted: it is established by creating the namespace and moved by transferring it")
	}
	return s.writeGrant(ctx, req)
}

func (s *Service) writeGrant(ctx context.Context, req GrantRequest) error {
	if s.keyORM() == nil {
		return fmt.Errorf("client not initialized")
	}
	if _, ok := roleRank[req.Role]; !ok {
		return &ErrUnknownRole{Role: string(req.Role)}
	}
	identifier := normalizeIdentifier(req.PrincipalType, req.Identifier)
	if identifier == "" {
		return fmt.Errorf("a grant needs a principal to belong to")
	}

	resource := strings.TrimSpace(req.Resource)
	if resource != "" {
		selector, err := ParseSelector(resource)
		if err != nil {
			return err
		}
		// A selector for something the role cannot reach anyway is a grant
		// nobody could ever act on, whichever way the enforcement lands.
		if want := selector.RequiredScope(); !req.Role.Scopes().Has(want) {
			return fmt.Errorf("role %q does not hold the %q grant that selector %q narrows",
				req.Role, want, resource)
		}
		resource = selector.String()
	}

	nsID, err := s.resolveKeyNamespaceID(ctx, req.Namespace)
	if err != nil {
		return fmt.Errorf("failed to resolve namespace %q: %w", req.Namespace, err)
	}

	db := s.keyORM().Database()
	principalID, err := s.ensurePrincipal(ctx, db, req.PrincipalType, identifier, req.DisplayName, req.CreatedBy)
	if err != nil {
		return err
	}

	var expires interface{}
	if !req.ExpiresAt.IsZero() {
		if !req.ExpiresAt.After(time.Now()) {
			return fmt.Errorf("a grant that has already expired authorises nothing; leave the expiry unset for one that does not expire")
		}
		expires = req.ExpiresAt.UTC().Format(sqliteTime)
	}

	var resourceValue interface{}
	if resource != "" {
		resourceValue = resource
	}

	if _, err := db.Query(client.WithInternalAuth(ctx),
		`INSERT INTO grants(principal_id, namespace_id, role, resource, expires_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		principalID, nsID, string(req.Role), resourceValue, expires, req.CreatedBy,
	); err != nil {
		// The unique index on (principal, namespace, role, resource) is what
		// stops the same grant existing twice, so a second write of the same
		// thing lands here rather than leaving two rows one revoke cannot
		// clear.
		existing, readErr := s.GrantIn(ctx, db, nsID, req.PrincipalType, identifier)
		if readErr == nil && existing.Role == req.Role && existing.Resource == resource {
			return nil
		}
		return fmt.Errorf("failed to record the grant in namespace %q: %w", req.Namespace, err)
	}

	s.audit.Record(ctx, AuditEvent{
		Namespace: req.Namespace,
		Actor:     req.CreatedBy,
		Action:    AuditGrantAdded,
		Resource:  string(req.PrincipalType) + " " + identifier,
		Result:    AuditSuccess,
		Metadata:  map[string]string{"role": string(req.Role), "selector": resource},
	})
	return nil
}

// RevokeGrant ends a principal's authority in a namespace.
//
// The row is kept with revoked_at set rather than deleted: what somebody was
// once allowed to do is the question an incident asks, and a deleted row cannot
// answer it.
func (s *Service) RevokeGrant(ctx context.Context, namespace string, ptype PrincipalType, identifier, actor string) error {
	if s.db == nil {
		return fmt.Errorf("revoking a grant requires the rqlite client (SetRqliteClient): without an affected-row count there is no way to tell a revoke from a no-op")
	}
	identifier = normalizeIdentifier(ptype, identifier)

	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}

	existing, err := s.GrantIn(ctx, s.keyORM().Database(), nsID, ptype, identifier)
	if err != nil {
		return err
	}
	if existing.Role == RoleOwner {
		return ErrOwnerCannotBeRemoved
	}

	res, err := s.db.Exec(client.WithInternalAuth(ctx),
		`UPDATE grants SET revoked_at = datetime('now')
		  WHERE namespace_id = ? AND revoked_at IS NULL
		    AND principal_id = (SELECT id FROM principals WHERE type = ? AND identifier = ?)`,
		nsID, string(ptype), identifier)
	if err != nil {
		return fmt.Errorf("failed to revoke the grant in namespace %q: %w", namespace, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to confirm the revocation in namespace %q: %w", namespace, err)
	}
	if affected == 0 {
		return ErrNotAMember
	}

	s.audit.Record(ctx, AuditEvent{
		Namespace: namespace,
		Actor:     actor,
		Action:    AuditGrantRevoked,
		Resource:  string(ptype) + " " + identifier,
		Result:    AuditSuccess,
	})
	return nil
}

// ListMembers returns every live grant in a namespace, strongest role first.
func (s *Service) ListMembers(ctx context.Context, namespace string) ([]Grant, error) {
	if s.keyORM() == nil {
		return nil, fmt.Errorf("client not initialized")
	}
	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}

	res, err := s.keyORM().Database().Query(client.WithInternalAuth(ctx),
		`SELECT p.type, p.identifier, p.display_name, g.role, g.resource, g.expires_at, g.created_at, g.created_by
		   FROM grants AS g
		   JOIN principals AS p ON p.id = g.principal_id
		  WHERE g.namespace_id = ?
		    AND g.revoked_at IS NULL
		    AND p.disabled_at IS NULL
		    AND (g.expires_at IS NULL OR g.expires_at > datetime('now'))
		  ORDER BY g.id`,
		nsID)
	if err != nil {
		return nil, fmt.Errorf("failed to list the members of namespace %q: %w", namespace, err)
	}
	if res == nil {
		return nil, nil
	}

	out := make([]Grant, 0, res.Count)
	for _, row := range res.Rows {
		if len(row) < 8 {
			continue
		}
		out = append(out, Grant{
			PrincipalType: PrincipalType(getStringVal(row[0])),
			Identifier:    getStringVal(row[1]),
			DisplayName:   getStringVal(row[2]),
			Role:          Role(getStringVal(row[3])),
			Resource:      getStringVal(row[4]),
			ExpiresAt:     getStringVal(row[5]),
			CreatedAt:     getStringVal(row[6]),
			CreatedBy:     getStringVal(row[7]),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return roleRank[out[i].Role] > roleRank[out[j].Role]
	})
	return out, nil
}

// normalizeIdentifier spells a principal the way the tables store it. A wallet
// in two capitalisations is two principals, which is how an owner stops being
// an owner.
func normalizeIdentifier(ptype PrincipalType, identifier string) string {
	if ptype == PrincipalWallet {
		return NormalizeWallet(identifier)
	}
	return strings.TrimSpace(identifier)
}

// grantServiceAccount records that a key belongs to a namespace.
//
// This is the row the authorization gate reads for a request authenticated by
// the key rather than by a wallet. Without it the caller holds a key that is
// refused everywhere, so it is not best-effort.
//
// The role is membership, not permission: what a key may reach comes from its
// own scopes column, which is written at mint time. Giving a service account a
// role as well would be two answers to one question.
func (s *Service) grantServiceAccount(ctx context.Context, db client.DatabaseClient, nsID interface{}, namespace, hashedKey string, role Role) error {
	principalID, err := s.ensurePrincipal(ctx, db, PrincipalServiceAccount, hashedKey, "", "key mint")
	if err != nil {
		return err
	}
	if _, err := db.Query(client.WithInternalAuth(ctx),
		`INSERT OR IGNORE INTO grants(principal_id, namespace_id, role, created_by)
		 VALUES (?, ?, ?, 'key mint')`,
		principalID, nsID, string(role),
	); err != nil {
		return fmt.Errorf("failed to record the key's grant in namespace %q: %w", namespace, err)
	}
	return nil
}

// revokeServiceAccount ends a key's membership of a namespace.
//
// Revoking rather than deleting: the row is the record of what that key could
// reach, and an incident asks about keys that have been taken away.
func (s *Service) revokeServiceAccount(ctx context.Context, db client.DatabaseClient, nsID interface{}, hashedKey string) error {
	if _, err := db.Query(client.WithInternalAuth(ctx),
		`UPDATE grants SET revoked_at = datetime('now')
		  WHERE namespace_id = ? AND revoked_at IS NULL
		    AND principal_id = (SELECT id FROM principals WHERE type = 'service_account' AND identifier = ?)`,
		nsID, hashedKey,
	); err != nil {
		return fmt.Errorf("failed to revoke the key's grant: %w", err)
	}
	return nil
}

// RoleForScopes is the membership role a key's grant set implies: a key holding
// admin is a control-plane credential, anything else is a runtime one.
func RoleForScopes(stored string) Role {
	if ScopesFromStored(stored).IsAdmin() {
		return RoleAdmin
	}
	return RoleRuntime
}
