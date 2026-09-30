package auth

import "testing"

func TestKeyPermissions(t *testing.T) {
	appRuntime, _ := ProfileGrants("app-runtime")
	appScopes := ScopeSet(setOf(appRuntime)).Canonical()

	cases := []struct {
		name     string
		scopes   string
		role     Role
		selector string
		permits  map[Resource]bool
	}{
		{
			name: "an app-runtime key holds no pubsub or cache", scopes: appScopes, role: RoleRuntime,
			permits: map[Resource]bool{
				{Domain: DomainPubsub, Action: ActionWrite}: false,
				{Domain: DomainCache, Action: ActionWrite}:  false,
				{Domain: DomainStorage, Action: ActionRead}: true,
				{Domain: DomainFn, Action: ActionInvoke}:    true,
			},
		},
		{
			name: "a cache key holds the cache and nothing else", scopes: ScopeCache, role: RoleRuntime,
			permits: map[Resource]bool{
				{Domain: DomainCache, Action: ActionWrite}:   true,
				{Domain: DomainPubsub, Action: ActionWrite}:  false,
				{Domain: DomainStorage, Action: ActionRead}:  false,
				{Domain: DomainDeploy, Action: ActionManage}: false,
			},
		},
		{
			name: "an admin key holds everything its admin role does", scopes: ScopeAdmin, role: RoleAdmin,
			permits: map[Resource]bool{
				{Domain: DomainPubsub, Action: ActionWrite}: true,
				{Domain: DomainDB, Action: ActionWrite}:     true,
			},
		},
		{
			name: "a role bounds the scopes too", scopes: ScopeAdmin, role: RoleReader,
			permits: map[Resource]bool{{Domain: DomainPubsub, Action: ActionWrite}: false},
		},
		{
			name: "a selector narrows a scope the key holds", scopes: ScopePubsub, role: RoleRuntime,
			selector: "pubsub:topic=jobs.*",
			permits: map[Resource]bool{
				{Domain: DomainPubsub, Action: ActionWrite, Name: "jobs.a"}: true,
				{Domain: DomainPubsub, Action: ActionWrite, Name: "chat.a"}: false,
			},
		},
		{
			name: "a selector cannot widen a key past its scopes", scopes: ScopeCache, role: RoleRuntime,
			selector: "pubsub:topic=jobs.*",
			permits:  map[Resource]bool{{Domain: DomainPubsub, Action: ActionWrite, Name: "jobs.a"}: false},
		},
		{
			name: "no scopes, nothing", scopes: "", role: RoleRuntime,
			permits: map[Resource]bool{{Domain: DomainCache, Action: ActionRead}: false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			perms := KeyPermissions(tc.scopes, tc.role, tc.selector)
			for r, want := range tc.permits {
				if got := perms.Permits(r); got != want {
					t.Errorf("%s permitted = %v, want %v (holds %s)", r, got, want, perms)
				}
			}
		})
	}
}
