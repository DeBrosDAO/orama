package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestParseSignInPolicy(t *testing.T) {
	for raw, want := range map[string]SignInPolicy{
		"members": SignInMembers, "open": SignInOpen, " OPEN ": SignInOpen, "Members": SignInMembers,
	} {
		if got, err := ParseSignInPolicy(raw); err != nil || got != want {
			t.Errorf("%q parsed to %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "everyone", "closed", "optional"} {
		if _, err := ParseSignInPolicy(raw); err == nil {
			t.Errorf("%q was accepted as a sign-in policy", raw)
		}
	}
}

// openNamespace is an owned namespace that has opened sign-in.
func openNamespace(t *testing.T) (*Service, *sqliteDatabase, interface{}) {
	t.Helper()
	s, db, nsID := realRegistry(t)
	giveOwner(t, s, db, nsID, "0xowner")
	if err := s.SetSignInPolicy(context.Background(), "anchat", SignInOpen, "0xowner"); err != nil {
		t.Fatalf("open sign-in: %v", err)
	}
	return s, db, nsID
}

func countRows(t *testing.T, db *sqliteDatabase, table string) int {
	t.Helper()
	var n int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestSignInPolicyOf_defaultsToMembers(t *testing.T) {
	s, _, _ := realRegistry(t)
	if p, err := s.SignInPolicyOf(context.Background(), "anchat"); err != nil || p != SignInMembers {
		t.Errorf("a namespace with no row has %q, %v; want members", p, err)
	}
}

func TestRequireSignInAllowed_membersRefusesAGrantlessWallet(t *testing.T) {
	s, db, nsID := realRegistry(t)
	giveOwner(t, s, db, nsID, "0xowner")

	var owned *ErrNamespaceOwnedByAnother
	if err := s.RequireSignInAllowed(context.Background(), "0xenduser", "anchat"); !errors.As(err, &owned) {
		t.Fatalf("a grantless wallet in a members namespace: %v", err)
	}
	if err := s.RequireSignInAllowed(context.Background(), "0xOWNER", "anchat"); err != nil {
		t.Errorf("the owner was refused: %v", err)
	}
}

// Open sign-in lets a wallet in and gives it nothing: no grant, no principal,
// no claim on the namespace.
func TestRequireSignInAllowed_openLetsAGrantlessWalletInAndWritesNothing(t *testing.T) {
	s, db, _ := openNamespace(t)
	grants, principals := countRows(t, db, "grants"), countRows(t, db, "principals")

	for _, wallet := range []string{"0xenduser", "0xAnotherEndUser"} {
		if err := s.RequireSignInAllowed(context.Background(), wallet, "anchat"); err != nil {
			t.Fatalf("%s was refused an open namespace: %v", wallet, err)
		}
	}
	if got := countRows(t, db, "grants"); got != grants {
		t.Errorf("grants went from %d to %d", grants, got)
	}
	if got := countRows(t, db, "principals"); got != principals {
		t.Errorf("principals went from %d to %d", principals, got)
	}
	if owner, err := s.OwnerOf(context.Background(), "anchat"); err != nil || owner != "0xowner" {
		t.Errorf("the owner is %q, %v", owner, err)
	}
}

// Nobody owns the namespace, so nobody could have opened it.
func TestRequireSignInAllowed_openDoesNotAdmitIntoAnUnownedNamespace(t *testing.T) {
	s, _, _ := realRegistry(t)
	if err := s.SetSignInPolicy(context.Background(), "anchat", SignInOpen, "0xsomeone"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.RequireSignInAllowed(context.Background(), "0xenduser", "anchat"); !errors.Is(err, ErrNamespaceUnowned) {
		t.Errorf("an unowned open namespace answered %v, want ErrNamespaceUnowned", err)
	}
}

func TestGetOrCreateAPIKey_aGrantlessWalletGetsNoKey(t *testing.T) {
	s, db, _ := openNamespace(t)
	keys := countRows(t, db, "api_keys")

	key, err := s.GetOrCreateAPIKey(context.Background(), "0xenduser", "anchat")
	if !errors.Is(err, ErrNoKeyForRole) || key != "" {
		t.Fatalf("got (%q, %v), want no key and ErrNoKeyForRole", key, err)
	}
	if got := countRows(t, db, "api_keys"); got != keys {
		t.Errorf("a key was stored for an end user: %d -> %d", keys, got)
	}
}

func TestSetSignInPolicy_keepsTheDevicePolicyAndIsKeptByIt(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()

	if err := s.SetSignInPolicy(ctx, "anchat", SignInOpen, "0xowner"); err != nil {
		t.Fatalf("set sign-in: %v", err)
	}
	if p, _ := s.DevicePolicyOf(ctx, "anchat"); p != DevicePolicyOptional {
		t.Errorf("a row made by sign-in alone has device policy %q, want optional", p)
	}
	if _, err := s.SetDevicePolicy(ctx, "anchat", DevicePolicyApproval, "0xowner"); err != nil {
		t.Fatalf("set device policy: %v", err)
	}
	if p, _ := s.SignInPolicyOf(ctx, "anchat"); p != SignInOpen {
		t.Errorf("setting the device policy changed sign-in to %q", p)
	}
	if err := s.SetSignInPolicy(ctx, "anchat", SignInMembers, "0xowner"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if p, _ := s.DevicePolicyOf(ctx, "anchat"); p != DevicePolicyApproval {
		t.Errorf("setting sign-in changed the device policy to %q", p)
	}
}

func TestSetSignInPolicy_refusesTheLobbyAndBadValues(t *testing.T) {
	s, _, _ := realRegistry(t)
	if err := s.SetSignInPolicy(context.Background(), LobbyNamespace, SignInOpen, "0xowner"); !errors.Is(err, ErrLobbyHasNoSignInPolicy) {
		t.Errorf("the lobby: %v", err)
	}
	if err := s.SetSignInPolicy(context.Background(), "anchat", SignInPolicy("all"), "0xowner"); err == nil {
		t.Error("an unknown policy was recorded")
	}
}

// A grantless session ends at its next refresh once sign-in closes; a
// member's does not.
func TestRefreshToken_closingSignInEndsAGrantlessSessionOnly(t *testing.T) {
	s, _, _ := openNamespace(t)
	ctx := context.Background()
	if err := s.Grant(ctx, GrantRequest{Namespace: "anchat", PrincipalType: PrincipalWallet,
		Identifier: "0xmember", Role: RoleRuntime, CreatedBy: "0xowner"}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	_, endUser, _, err := s.IssueTokens(ctx, "0xenduser", "anchat")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	_, member, _, err := s.IssueTokens(ctx, "0xmember", "anchat")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Still open: the grantless session refreshes, and so rotates.
	if _, endUser, _, _, err = s.RefreshToken(ctx, endUser, "anchat", nil); err != nil {
		t.Fatalf("a grantless session was refused while sign-in is open: %v", err)
	}

	if err := s.SetSignInPolicy(ctx, "anchat", SignInMembers, "0xowner"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, _, _, _, err := s.RefreshToken(ctx, endUser, "anchat", nil); !errors.Is(err, ErrSignInClosed) {
		t.Errorf("a grantless session refreshed after sign-in closed: %v", err)
	}
	if _, _, _, _, err := s.RefreshToken(ctx, member, "anchat", nil); err != nil {
		t.Errorf("a member's refresh was refused when sign-in closed: %v", err)
	}
}

// A gateway that has not yet seen the closing judges a refresh by what it read
// within devicePolicyStaleness; once the entry is older it reads again.
func TestCachedSignInPolicyOf_isBoundedByTheStaleness(t *testing.T) {
	s, db, nsID := openNamespace(t)
	ctx := context.Background()
	if p, _ := s.cachedSignInPolicyOf(ctx, "anchat"); p != SignInOpen {
		t.Fatalf("cache primed with %q", p)
	}
	// Another gateway closes it: this one's row changes under its cache.
	if _, err := db.db.Exec(`UPDATE namespace_session_policy SET sign_in = 'members' WHERE namespace_id = ?`, nsID); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.cachedSignInPolicyOf(ctx, "anchat"); p != SignInOpen {
		t.Errorf("a read inside the staleness bound went to the registry: %q", p)
	}
	s.signInPolicies = signInPolicyCache{}
	if p, _ := s.cachedSignInPolicyOf(ctx, "anchat"); p != SignInMembers {
		t.Errorf("after the bound the read still said %q", p)
	}
}

// A refresh ends a session only on the policy as recorded now: a `members`
// this gateway cached just before the owner opened sign-in elsewhere would
// otherwise end a session that is entitled to go on.
func TestRefreshToken_aStaleMembersReadDoesNotEndASession(t *testing.T) {
	s, _, _ := openNamespace(t)
	ctx := context.Background()
	_, endUser, _, err := s.IssueTokens(ctx, "0xenduser", "anchat")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	s.signInPolicies.put("anchat", SignInMembers, time.Now())
	if _, _, _, _, err := s.RefreshToken(ctx, endUser, "anchat", nil); err != nil {
		t.Errorf("a session in an open namespace was ended on a stale cached policy: %v", err)
	}
}

// The lobby has no sign-in policy and nobody holds a grant there: its sessions
// refresh whatever any namespace says.
func TestRefreshToken_theLobbyIsNotJudgedBySignIn(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()
	_, refresh, _, err := s.IssueTokens(ctx, "0xenduser", LobbyNamespace)
	if err != nil {
		t.Fatalf("issue in the lobby: %v", err)
	}
	if _, _, _, _, err := s.RefreshToken(ctx, refresh, LobbyNamespace, nil); err != nil {
		t.Errorf("a lobby session was refused its refresh: %v", err)
	}
}

// Every path that hands out a session passes IssueDeviceTokens, so the rule
// holds there: a device-link claim reached it with no check, and a grantless
// wallet kept minting sessions after sign-in closed by approving its own next
// link from a device it still held.
func TestIssueDeviceTokens_aGrantlessWalletIsRefusedOnceSignInCloses(t *testing.T) {
	s, _, _ := openNamespace(t)
	ctx := context.Background()
	d := p256Device(t)
	mustEnrol(t, s, "0xenduser", d, DeviceStateActive)
	if _, _, _, err := s.IssueDeviceTokens(ctx, "0xenduser", "anchat", d.id); err != nil {
		t.Fatalf("issue while open: %v", err)
	}
	if err := s.SetSignInPolicy(ctx, "anchat", SignInMembers, "0xowner"); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, _, _, err := s.IssueDeviceTokens(ctx, "0xenduser", "anchat", d.id)
	var owned *ErrNamespaceOwnedByAnother
	if !errors.As(err, &owned) {
		t.Errorf("a session was issued to a grantless wallet after sign-in closed: %v", err)
	}
}
