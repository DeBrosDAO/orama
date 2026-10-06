package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// ErrNamespaceOwnedByAnother is returned when a wallet asks for credentials in
// a namespace another wallet already owns.
//
// Ownership used to be a side effect of minting a key: GetOrCreateAPIKey
// inserted the caller into namespace_ownership unconditionally, so any wallet
// that signed a fresh nonce and named an existing namespace in the body of
// /v1/auth/verify became a co-owner of it. That row satisfied the namespace
// gate, which marked the caller a confirmed owner, which granted a wallet JWT
// admin — and the key handed back in the same response was minted with no
// scopes, which the read path also treated as admin.
//
// Ownership is checked before anything is written now, and a namespace that
// already has an owner accepts no second one.
type ErrNamespaceOwnedByAnother struct {
	Namespace string
}

func (e *ErrNamespaceOwnedByAnother) Error() string {
	return fmt.Sprintf("namespace %q belongs to another wallet", e.Namespace)
}

// LobbyNamespace is where a wallet stands before it owns anything.
//
// It is created by migration 001 with no owner, and every wallet that signs in
// without naming a namespace lands there. It is not a tenant's namespace: it is
// the index gateway's own, and nothing belonging to anybody is in it.
const LobbyNamespace = "default"

// IsLobbyNamespace reports whether a namespace is the lobby.
func IsLobbyNamespace(namespace string) bool {
	return strings.EqualFold(strings.TrimSpace(namespace), LobbyNamespace)
}

// ErrNoKeysInLobby is returned for a key asked for in the lobby. The lobby has
// none: a wallet there holds no role and owns nothing, and minting one would
// hand every wallet on the internet a credential in the index gateway's own
// namespace.
var ErrNoKeysInLobby = errors.New("the lobby namespace has no keys")

// ErrNoKeyForRole is returned for a key asked for by a member whose role holds
// no grant (a reader), or by a wallet that is no member at all: an end user of
// a namespace with open sign-in. There is nothing to put in the key; the
// session is its whole credential and reaches only the routes that ask for no
// permission, and what a grantless end user has beyond those is
// NoGrantPermissions.
var ErrNoKeyForRole = errors.New("this role holds no grant, so there is no key to mint")

// ErrNamespaceUnowned is returned for a namespace nobody holds. Signing in used
// to claim one; a namespace that belongs to nobody is now one nobody can enter,
// and creating a namespace is what makes it yours.
var ErrNamespaceUnowned = errors.New("this namespace has no owner, so nobody may sign in to it")

// RequireSignInAllowed is the gate every sign-in passes: it refuses a wallet
// that may not hold a session in the namespace.
//
// A login handler calls it as soon as the signature and the nonce are settled,
// before it issues a JWT, mints a key or triggers provisioning. Doing it there
// rather than only inside GetOrCreateAPIKey means a refused wallet causes no
// writes at all: no refresh token row, no cluster provisioning for someone
// else's namespace.
//
// A wallet holding a live grant is let in. A wallet holding none is let in only
// to a namespace that has an owner and has opened sign-in (SignInOpen): it is
// then an end user of the application, grantless. Letting it in writes nothing
// - no grant, no principal, no claim on the namespace - and what it can do is
// what NoGrantPermissions says. Every other grantless wallet is refused: the
// namespace belongs to another wallet (ErrNamespaceOwnedByAnother), or to
// nobody (ErrNamespaceUnowned), which stays refused whatever it says about
// sign-in, since a namespace with no owner has nobody who could have opened it.
//
// It used to claim: the first wallet to sign in to a namespace with no owner
// became its owner. That is how `default` ended up belonging to whichever
// wallet happened to sign in first on each cluster, and every wallet after it
// got a 403 on the namespace the docs called "where a wallet signs in before it
// owns anything" - true for exactly one wallet per cluster.
//
// The lobby needs no grant and is given none. What a wallet gets there is a
// session and nothing else: no key, no role, and the one thing it reaches is
// POST /v1/namespaces, which creates a namespace and makes the caller its
// owner. That is now the only path that writes an owner grant, which is the
// invariant the epic asks for.
func (s *Service) RequireSignInAllowed(ctx context.Context, wallet, namespace string) error {
	if s.keyORM() == nil {
		return fmt.Errorf("client not initialized")
	}
	if strings.TrimSpace(wallet) == "" {
		return fmt.Errorf("wallet is required")
	}
	if IsLobbyNamespace(namespace) {
		return nil
	}

	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}
	db := s.keyORM().Database()

	if _, err := s.GrantIn(ctx, db, nsID, PrincipalWallet, NormalizeWallet(wallet)); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotAMember) {
		return fmt.Errorf("failed to read the grants of namespace %q: %w", namespace, err)
	}

	// Which refusal it is matters to whoever reads it: a namespace with an
	// owner needs an invitation, and one with none needs somebody to have
	// created it properly.
	owner, err := s.ownerOf(ctx, db, nsID)
	if err != nil {
		return fmt.Errorf("failed to read the owner of namespace %q: %w", namespace, err)
	}
	if owner == "" {
		return fmt.Errorf("%w: %q", ErrNamespaceUnowned, namespace)
	}
	// Read now, not from the refresh cache: this is the path that issues a
	// session, and one issued in the stale window would outlive the closing.
	policy, err := s.SignInPolicyOf(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to read the sign-in policy of namespace %q: %w", namespace, err)
	}
	if policy == SignInOpen {
		return nil
	}
	return &ErrNamespaceOwnedByAnother{Namespace: namespace}
}

// ownerOf returns the namespace's owner, or "" when it has none.
func (s *Service) ownerOf(ctx context.Context, db client.DatabaseClient, nsID interface{}) (string, error) {
	res, err := db.Query(client.WithInternalAuth(ctx),
		`SELECT p.identifier FROM grants AS g
		   JOIN principals AS p ON p.id = g.principal_id
		  WHERE g.namespace_id = ? AND g.role = 'owner' AND g.revoked_at IS NULL
		  LIMIT 1`,
		nsID)
	if err != nil {
		return "", err
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return "", nil
	}
	return strings.TrimSpace(getStringVal(res.Rows[0][0])), nil
}

// OwnerOf returns the wallet that owns a namespace, or "" when nobody does.
func (s *Service) OwnerOf(ctx context.Context, namespace string) (string, error) {
	if s.keyORM() == nil {
		return "", fmt.Errorf("client not initialized")
	}
	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}
	return s.ownerOf(ctx, s.keyORM().Database(), nsID)
}

// TransferOwnership moves the owner grant from one wallet to another.
//
// It is the only way ownership moves, and it is one operation rather than a
// revoke and a grant: a namespace between the two would have no owner, and an
// unowned namespace is claimable by whoever signs in next. The old owner keeps
// a place in the namespace as an admin, because the alternative is that
// handing over a project locks you out of it in the same instant.
func (s *Service) TransferOwnership(ctx context.Context, namespace, from, to string) error {
	if s.db == nil {
		return fmt.Errorf("transferring ownership requires the rqlite client (SetRqliteClient): without an affected-row count a transfer that changed nothing looks like one that worked")
	}
	fromWallet, toWallet := NormalizeWallet(from), NormalizeWallet(to)
	if toWallet == "" {
		return fmt.Errorf("a namespace has to be transferred to somebody")
	}
	if fromWallet == toWallet {
		return fmt.Errorf("namespace %q already belongs to %s", namespace, toWallet)
	}

	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}
	db := s.keyORM().Database()

	current, err := s.ownerOf(ctx, db, nsID)
	if err != nil {
		return fmt.Errorf("failed to read the owner of namespace %q: %w", namespace, err)
	}
	if current == "" {
		return fmt.Errorf("namespace %q has no owner to transfer", namespace)
	}
	if current != fromWallet {
		return &ErrNamespaceOwnedByAnother{Namespace: namespace}
	}

	newOwnerID, err := s.ensurePrincipal(ctx, db, PrincipalWallet, toWallet, "", fromWallet)
	if err != nil {
		return err
	}

	// The old owner becomes an admin first. Doing it after the owner grant
	// moved would leave a window where the previous owner holds nothing, and a
	// failure in between would make handing over a project a way to lose it.
	if err := s.writeGrant(ctx, GrantRequest{
		Namespace:     namespace,
		PrincipalType: PrincipalWallet,
		Identifier:    fromWallet,
		Role:          RoleAdmin,
		CreatedBy:     fromWallet,
	}); err != nil {
		return fmt.Errorf("failed to keep the previous owner as an admin of %q: %w", namespace, err)
	}

	// One statement, so the namespace is never ownerless: the partial unique
	// index allows exactly one live owner, and this row is it.
	res, err := s.db.Exec(client.WithInternalAuth(ctx),
		`UPDATE grants SET principal_id = ?, created_by = ?, created_at = datetime('now')
		  WHERE namespace_id = ? AND role = 'owner' AND revoked_at IS NULL`,
		newOwnerID, fromWallet, nsID)
	if err != nil {
		return fmt.Errorf("failed to transfer namespace %q: %w", namespace, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to confirm the transfer of namespace %q: %w", namespace, err)
	}
	if affected == 0 {
		return fmt.Errorf("namespace %q has no owner to transfer", namespace)
	}
	// Ownership replaces whatever the new owner held before. A member's admin
	// row left live beside it was what made handing the namespace back fail.
	if err := retireNonOwnerGrants(ctx, db, newOwnerID, nsID); err != nil {
		return fmt.Errorf("namespace %q now belongs to %s, but %w", namespace, toWallet, err)
	}

	s.audit.Record(ctx, AuditEvent{
		Namespace: namespace,
		Actor:     fromWallet,
		Action:    AuditOwnerTransferred,
		Resource:  "wallet " + toWallet,
		Result:    AuditSuccess,
	})
	return nil
}

// CountNamespacesOwnedBy is the per-wallet namespace quota's input.
func (s *Service) CountNamespacesOwnedBy(ctx context.Context, wallet string) (int, error) {
	if s.keyORM() == nil {
		return 0, fmt.Errorf("client not initialized")
	}
	res, err := s.keyORM().Database().Query(client.WithInternalAuth(ctx),
		`SELECT COUNT(*) FROM grants AS g
		   JOIN principals AS p ON p.id = g.principal_id
		  WHERE p.type = 'wallet' AND p.identifier = ?
		    AND g.role = 'owner' AND g.revoked_at IS NULL`,
		NormalizeWallet(wallet))
	if err != nil {
		return 0, fmt.Errorf("failed to count the namespaces owned by %s: %w", wallet, err)
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return 0, nil
	}
	return int(toInt64(res.Rows[0][0])), nil
}
