package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// Who may sign in to a namespace, per namespace and opt-in.
//
// An application's public end users — every user of a chat app signs in with
// their own wallet to the app's namespace — hold no grant, and inviting each
// one as a member would make every user of the application a member of its
// control plane's roster. The platform already models such a wallet: its
// permissions are NoGrantPermissions, and the no-grant pubsub rules and the
// end-user device sessions are written for it. What was missing was a way for
// the namespace's owner to let it in, so a namespace says so here.

// SignInPolicy is a namespace's rule for wallets that hold no grant.
type SignInPolicy string

const (
	// SignInMembers lets in only wallets holding a grant: sign-in is by
	// invitation. It is what a namespace with no policy has, and how every
	// namespace worked before the policy existed.
	SignInMembers SignInPolicy = "members"
	// SignInOpen also lets in a wallet that holds no grant, as a grantless end
	// user. It never gets a key, a grant or a claim on the namespace.
	SignInOpen SignInPolicy = "open"
)

// ErrSignInClosed is a session refreshing, or being issued, for a wallet that
// holds no grant in a namespace that is not open: sign-in was closed again
// after it was issued, or the wallet's grant was revoked, expired or disabled.
var ErrSignInClosed = errors.New("this wallet holds no grant in this namespace, and the namespace does not let in wallets it has not invited")

// ErrLobbyHasNoSignInPolicy is a sign-in policy set on the lobby, which is no
// tenant's namespace and lets in every wallet by design.
var ErrLobbyHasNoSignInPolicy = errors.New("the lobby has no sign-in policy; set one on a namespace you own")

// ParseSignInPolicy reads a policy name.
func ParseSignInPolicy(raw string) (SignInPolicy, error) {
	switch p := SignInPolicy(strings.ToLower(strings.TrimSpace(raw))); p {
	case SignInMembers, SignInOpen:
		return p, nil
	default:
		return "", fmt.Errorf("sign-in policy is one of members, open (got %q)", raw)
	}
}

// signInPolicyCache holds what each namespace's sign-in policy was, briefly. It
// is devicePolicyCache for the other column of the same row, and is read by the
// same callers for the same reason: a refresh asks on every rotation.
type signInPolicyCache struct {
	mu      sync.Mutex
	entries map[string]cachedSignInPolicy
}

type cachedSignInPolicy struct {
	policy SignInPolicy
	readAt time.Time
}

func (c *signInPolicyCache) get(namespace string, now time.Time) (SignInPolicy, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[namespace]
	if !ok || now.Sub(e.readAt) >= devicePolicyStaleness {
		return "", false
	}
	return e.policy, true
}

func (c *signInPolicyCache) put(namespace string, policy SignInPolicy, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cachedSignInPolicy{}
	}
	c.entries[namespace] = cachedSignInPolicy{policy: policy, readAt: now}
}

// SignInPolicyOf returns a namespace's sign-in policy, as recorded now.
// The lobby has none and lets every wallet in, which is SignInMembers' answer
// for it only because nothing asks.
func (s *Service) SignInPolicyOf(ctx context.Context, namespace string) (SignInPolicy, error) {
	namespace = sessionNamespace(namespace)
	if IsLobbyNamespace(namespace) {
		return SignInMembers, nil
	}
	db, nsID, err := s.deviceRegistry(ctx, namespace)
	if err != nil {
		return "", err
	}
	res, err := db.Query(client.WithInternalAuth(ctx),
		`SELECT sign_in FROM namespace_session_policy WHERE namespace_id = ? LIMIT 1`, nsID)
	if err != nil {
		return "", fmt.Errorf("read the sign-in policy of %q: %w", namespace, err)
	}
	policy := SignInMembers
	if res != nil && len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
		if policy, err = ParseSignInPolicy(getStringVal(res.Rows[0][0])); err != nil {
			return "", err
		}
	}
	s.signInPolicies.put(namespace, policy, time.Now())
	return policy, nil
}

// cachedSignInPolicyOf is SignInPolicyOf, answered from what this gateway read
// within devicePolicyStaleness.
func (s *Service) cachedSignInPolicyOf(ctx context.Context, namespace string) (SignInPolicy, error) {
	namespace = sessionNamespace(namespace)
	if p, ok := s.signInPolicies.get(namespace, time.Now()); ok {
		return p, nil
	}
	return s.SignInPolicyOf(ctx, namespace)
}

// SetSignInPolicy records a namespace's sign-in policy.
//
// It writes only the sign-in column: a namespace's device policy is left as it
// is, and a row this creates starts with the device policy a namespace with no
// row has, optional. Closing sign-in again revokes nothing here. The grantless
// sessions already issued end at their next refresh (checkSignInStillOpen),
// which is what keeps the closing from costing a sweep of every end user.
func (s *Service) SetSignInPolicy(ctx context.Context, namespace string, policy SignInPolicy, actor string) error {
	if _, err := ParseSignInPolicy(string(policy)); err != nil {
		return err
	}
	if s.db == nil {
		return ErrRotationNotConfigured
	}
	namespace = sessionNamespace(namespace)
	if IsLobbyNamespace(namespace) {
		return ErrLobbyHasNoSignInPolicy
	}
	_, nsID, err := s.deviceRegistry(ctx, namespace)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(client.WithInternalAuth(ctx),
		`INSERT INTO namespace_session_policy(namespace_id, device_policy, sign_in, updated_by, updated_at)
		 VALUES (?, ?, ?, ?, datetime('now'))
		 ON CONFLICT(namespace_id) DO UPDATE SET
		   sign_in = excluded.sign_in,
		   updated_by = excluded.updated_by,
		   updated_at = excluded.updated_at`,
		nsID, string(DevicePolicyOptional), string(policy), actor); err != nil {
		return fmt.Errorf("record the sign-in policy of %q: %w", namespace, err)
	}
	s.signInPolicies.put(namespace, policy, time.Now())
	return nil
}

// checkSignInStillOpen is what a refresh asks beyond the device checks: a
// session of a wallet holding no grant ends when the namespace is no longer
// open. A member's session is untouched whatever the policy says, and so is a
// session in an open namespace.
//
// The policy is judged by what this gateway read within devicePolicyStaleness.
// Only a namespace that is not open pays for the grant read.
func (s *Service) checkSignInStillOpen(ctx context.Context, namespace, wallet string) error {
	namespace = sessionNamespace(namespace)
	if IsLobbyNamespace(namespace) {
		return nil
	}
	policy, err := s.cachedSignInPolicyOf(ctx, namespace)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRefreshTransient, err)
	}
	if policy == SignInOpen {
		return nil
	}
	if s.keyORM() == nil {
		return fmt.Errorf("%w: client not initialized", ErrRefreshTransient)
	}
	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return fmt.Errorf("%w: resolve the namespace %q: %v", ErrRefreshTransient, namespace, err)
	}
	_, err = s.GrantIn(ctx, s.keyORM().Database(), nsID, PrincipalWallet, NormalizeWallet(wallet))
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotAMember) {
		return fmt.Errorf("%w: %v", ErrRefreshTransient, err)
	}
	// Ending a session is final for the client, so it is judged by the policy
	// as recorded now: a cached `members` read just before the owner opened
	// sign-in on another gateway would end a session that is entitled to go
	// on. Only a refusal pays for the read.
	if policy, err = s.SignInPolicyOf(ctx, namespace); err != nil {
		return fmt.Errorf("%w: %v", ErrRefreshTransient, err)
	}
	if policy == SignInOpen {
		return nil
	}
	return ErrSignInClosed
}
