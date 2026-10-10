package auth

import (
	"strings"
	"testing"
)

// The mapping is where a migration would go wrong, and it would go wrong
// silently: a key in the fleet gaining access on the deploy that shipped it.
// So every legacy form is held against what it authorised.

func TestPermissionsFromScopes_giveEachLegacyWordExactlyWhatItHad(t *testing.T) {
	for _, tc := range []struct {
		scope  string
		domain Domain
		action Action
	}{
		{ScopeStorage, DomainStorage, ActionWrite},
		{ScopePubsub, DomainPubsub, ActionWrite},
		{ScopeCache, DomainCache, ActionRead},
		{ScopePush, DomainPush, ActionWrite},
		{ScopeWebRTC, DomainWebRTC, ActionRead},
		{ScopeProxy, DomainProxy, ActionWrite},
		{ScopeInvoke, DomainFn, ActionInvoke},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			perms := PermissionsFromScopes(tc.scope)

			if !perms.PermitsDomain(tc.domain, tc.action) {
				t.Errorf("%q no longer reaches %s:%s", tc.scope, tc.domain, tc.action)
			}
			if perms.IsAdmin() {
				t.Errorf("%q became the unrestricted set", tc.scope)
			}
			// A data-plane word reaches its own domain and nothing else.
			for _, other := range []Domain{DomainDeploy, DomainMembers, DomainOperator, DomainNamespace} {
				if perms.PermitsDomain(other, ActionWrite) {
					t.Errorf("%q now reaches %s, which is the control plane", tc.scope, other)
				}
			}
		})
	}
}

// `invoke` was running a function. Managing one was part of `admin`, and a
// credential that could only invoke must not be able to replace the code.
func TestPermissionsFromScopes_invokeDoesNotManage(t *testing.T) {
	perms := PermissionsFromScopes(ScopeInvoke)

	if !perms.PermitsDomain(DomainFn, ActionInvoke) {
		t.Error("the invoke scope cannot invoke")
	}
	if perms.PermitsDomain(DomainFn, ActionManage) {
		t.Error("the invoke scope can now replace a function's code")
	}
}

func TestPermissionsFromScopes_adminIsEverything(t *testing.T) {
	perms := PermissionsFromScopes(ScopeAdmin)

	if !perms.IsAdmin() {
		t.Fatal("admin is not the unrestricted set")
	}
	for domain := range domains {
		for _, action := range []Action{ActionRead, ActionWrite, ActionInvoke, ActionManage} {
			if !perms.PermitsDomain(domain, action) {
				t.Errorf("admin no longer reaches %s:%s", domain, action)
			}
		}
	}
	if !perms.Permits(Resource{Domain: DomainStorage, Name: "anything/at/all", Action: ActionWrite}) {
		t.Error("admin no longer reaches a named object")
	}
}

// A word this binary does not recognise contributes nothing. Inventing a
// meaning for it is how a typo becomes an authority.
func TestPermissionsFromScopes_ignoresWhatItDoesNotKnow(t *testing.T) {
	perms := PermissionsFromScopes("storage,not-a-scope,")

	if !perms.PermitsDomain(DomainStorage, ActionRead) {
		t.Error("the word it did know was dropped too")
	}
	if len(perms) != 1 {
		t.Errorf("permissions = %v, want only the one it recognised", perms.List())
	}
	if len(PermissionsFromScopes("")) != 0 {
		t.Error("an empty scope string granted something")
	}
}

// A selector said which part of a domain a grant reached, in a grammar that had
// to carry a key (`topic=`) because a domain had no other way to say which of
// its things it meant. The permission says it structurally, and has to mean the
// same thing.
func TestPermissionFromSelector_saysWhatTheSelectorSaid(t *testing.T) {
	for selector, want := range map[string]string{
		"storage:avatars/*":    "storage:*:avatars/*",
		"pubsub:topic=chat.*":  "pubsub:*:chat.*",
		"cache:key=sessions/*": "cache:*:sessions/*",
		"push:topic=alerts":    "push:*:alerts",
		"fn:name=checkout":     "fn:invoke:checkout",
		"db:table=posts:read":  "db:read:posts",
		"db:table=posts:write": "db:write:posts",
	} {
		t.Run(selector, func(t *testing.T) {
			got, err := PermissionFromSelector(selector)
			if err != nil {
				t.Fatalf("PermissionFromSelector(%q): %v", selector, err)
			}
			if got.String() != want {
				t.Errorf("= %q, want %q", got.String(), want)
			}
		})
	}
}

// A grant with a selector holds that permission and no other. It used to hold
// the scope the selector narrowed, which the data path narrowed again — two
// steps, because the two models did not line up.
func TestPermissionsFor_narrowsAndNeverWidens(t *testing.T) {
	t.Run("a runtime grant narrowed to a prefix", func(t *testing.T) {
		perms := PermissionsFor(RoleRuntime, "storage:avatars/*")

		if !perms.Permits(Resource{Domain: DomainStorage, Name: "avatars/me.png", Action: ActionWrite}) {
			t.Error("it cannot reach what it names")
		}
		if perms.Permits(Resource{Domain: DomainStorage, Name: "keys/private.pem", Action: ActionRead}) {
			t.Error("it reaches an object outside the prefix")
		}
		if perms.PermitsDomain(DomainPubsub, ActionWrite) {
			t.Error("a storage selector left the credential holding pubsub")
		}
	})

	t.Run("a selector for something the role never had", func(t *testing.T) {
		// A reader holds nothing, so `storage:avatars/*` on one describes
		// something it never reached. Handing it over would let a selector
		// *widen* a grant.
		if perms := PermissionsFor(RoleReader, "storage:avatars/*"); len(perms) != 0 {
			t.Errorf("a reader was given %v", perms.List())
		}
		// Same shape one level up: a runtime member never had the database.
		if perms := PermissionsFor(RoleRuntime, "db:table=posts:read"); len(perms) != 0 {
			t.Errorf("a runtime member was given %v", perms.List())
		}
	})

	t.Run("a selector this binary cannot read", func(t *testing.T) {
		if perms := PermissionsFor(RoleAdmin, "this is not a selector"); len(perms) != 0 {
			t.Errorf("an unreadable selector granted %v", perms.List())
		}
	})

	t.Run("no selector is the whole role", func(t *testing.T) {
		if !PermissionsFor(RoleAdmin, "").IsAdmin() {
			t.Error("an admin grant with no selector is not admin")
		}
		if PermissionsFor(RoleRuntime, "").IsAdmin() {
			t.Error("a runtime grant with no selector is admin")
		}
	})
}

// The role vocabulary is what a member list shows and what `orama members add`
// takes, so each has to mean something different from the next.
func TestRolePermissions(t *testing.T) {
	owner, admin := RoleOwner.Permissions(), RoleAdmin.Permissions()
	if !owner.IsAdmin() || !admin.IsAdmin() {
		t.Fatal("owner and admin are not unrestricted")
	}

	developer := RoleDeveloper.Permissions()
	for _, reach := range []struct {
		domain Domain
		action Action
	}{
		{DomainDeploy, ActionWrite}, {DomainDB, ActionWrite}, {DomainSecrets, ActionWrite},
		{DomainFn, ActionManage}, {DomainFn, ActionInvoke}, {DomainStorage, ActionWrite},
	} {
		if !developer.PermitsDomain(reach.domain, reach.action) {
			t.Errorf("a developer cannot %s:%s", reach.domain, reach.action)
		}
	}
	// The boundary that makes the role worth having.
	for _, denied := range []Domain{DomainMembers, DomainNamespace, DomainOperator} {
		if developer.PermitsDomain(denied, ActionWrite) {
			t.Errorf("a developer reaches %s, which is the owner's", denied)
		}
	}
	if developer.IsAdmin() {
		t.Error("a developer is unrestricted, so the role is a label on admin")
	}

	runtime := RoleRuntime.Permissions()
	if !runtime.PermitsDomain(DomainStorage, ActionWrite) || !runtime.PermitsDomain(DomainFn, ActionInvoke) {
		t.Error("a runtime member cannot reach the data plane")
	}
	for _, denied := range []Domain{DomainDB, DomainDeploy, DomainSecrets, DomainMembers} {
		if runtime.PermitsDomain(denied, ActionWrite) {
			t.Errorf("a runtime member reaches %s, which is the control plane", denied)
		}
	}
	if runtime.PermitsDomain(DomainFn, ActionManage) {
		t.Error("a runtime member can replace a function's code")
	}

	if len(RoleReader.Permissions()) != 0 {
		t.Error("a reader holds something")
	}
	// A role from a newer gateway grants nothing rather than being guessed at.
	if len(Role("archivist").Permissions()) != 0 {
		t.Error("an unrecognised role granted something")
	}
}

func TestParsePermission(t *testing.T) {
	for raw, want := range map[string]string{
		"storage:read:avatars/*": "storage:read:avatars/*",
		"storage:avatars/*":      "storage:*:avatars/*",
		"storage":                "storage:*:*",
		"*:*:*":                  "*:*:*",
		"STORAGE:READ:x":         "storage:read:x",
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := ParsePermission(raw)
			if err != nil {
				t.Fatalf("ParsePermission(%q): %v", raw, err)
			}
			if got.String() != want {
				t.Errorf("= %q, want %q", got.String(), want)
			}
		})
	}

	for _, bad := range []string{"", "filesystem:read:/etc", "storage:erase:x", "a:b:c:d", "storage:read:has space"} {
		t.Run("refuses "+bad, func(t *testing.T) {
			if got, err := ParsePermission(bad); err == nil {
				t.Errorf("accepted %q as %q", bad, got)
			}
		})
	}
}

// The scope gate runs before the handler, which is the only thing that knows
// which object a request is about. So a permission answers a question with no
// object in it — and must not answer it by matching the empty string.
func TestPermits_aRequestWithNoObjectAsksAboutTheDomain(t *testing.T) {
	narrow := PermissionSet{{Domain: DomainStorage, Action: ActionRead, Resource: "avatars/*"}}

	if !narrow.PermitsDomain(DomainStorage, ActionRead) {
		t.Error("a narrowed permission does not reach its own domain, so the gate refuses before the handler can narrow")
	}
	// The handler's question, with no object to name: an object nothing could
	// name is not covered by a narrowed permission.
	if narrow.Permits(Resource{Domain: DomainStorage, Action: ActionRead}) {
		t.Error("an object this code could not name was covered by a narrowed permission")
	}
	if !PermissionsFromScopes(ScopeStorage).Permits(Resource{Domain: DomainStorage, Action: ActionRead}) {
		t.Error("an unnarrowed permission refused an object it could not name")
	}
	if narrow.PermitsDomain(DomainStorage, ActionWrite) {
		t.Error("a read permission reaches writes")
	}
	if narrow.PermitsDomain(DomainPubsub, ActionRead) {
		t.Error("a storage permission reaches pubsub")
	}
	if narrow.Permits(Resource{Domain: DomainStorage, Name: "keys/x", Action: ActionRead}) {
		t.Error("it reaches an object it does not name")
	}
}

func TestPermissionString_roundTrips(t *testing.T) {
	for _, raw := range []string{"storage:read:avatars/*", "*:*:*", "fn:invoke:checkout", "db:write:posts"} {
		p, err := ParsePermission(raw)
		if err != nil {
			t.Fatalf("ParsePermission(%q): %v", raw, err)
		}
		again, err := ParsePermission(p.String())
		if err != nil || again != p {
			t.Errorf("%q did not round-trip: %v / %v", raw, again, err)
		}
	}
	if got := (PermissionSet{{DomainStorage, ActionRead, "b"}, {DomainStorage, ActionRead, "a"}}).String(); !strings.Contains(got, "a") {
		t.Errorf("String dropped a permission: %q", got)
	}
}

// IsAdmin is what exempts a caller from the layer-1 token requirement — the
// thing that makes an extracted runtime key inert on the data plane. So it has
// to mean *every* permission, not "a wildcard somewhere in it".
func TestIsAdmin_meansEveryPermissionAndNotAWildcardSomewhere(t *testing.T) {
	if !Everything().IsAdmin() {
		t.Error("the unrestricted set is not admin")
	}

	for name, set := range map[string]PermissionSet{
		"every domain, reads only":     {{Domain: Domain(PermissionWildcard), Action: ActionRead, Resource: PermissionWildcard}},
		"every domain, one object":     {{Domain: Domain(PermissionWildcard), Action: ActionAny, Resource: "avatars/*"}},
		"one domain, everything in it": {{Domain: DomainStorage, Action: ActionAny, Resource: PermissionWildcard}},
		"nothing":                      {},
	} {
		t.Run(name, func(t *testing.T) {
			if set.IsAdmin() {
				t.Errorf("%v counted as admin, which exempts it from the logged-in-user requirement", set.List())
			}
		})
	}
}
