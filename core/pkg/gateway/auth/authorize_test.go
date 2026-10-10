package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// A grant narrowed to `pubsub:topic=chat.*` used to authorise nothing, because
// nothing read the selector. This is where it comes to mean what it says.

func TestGrant_permits(t *testing.T) {
	tests := []struct {
		name     string
		selector string
		resource Resource
		allowed  bool
	}{
		{
			name:     "a grant with no selector is the whole role",
			resource: Resource{Domain: SelectorPubsub, Name: "anything"},
			allowed:  true,
		},
		{
			name:     "an exact topic",
			selector: "pubsub:topic=chat.general",
			resource: Resource{Domain: SelectorPubsub, Name: "chat.general"},
			allowed:  true,
		},
		{
			name:     "a topic the selector does not name",
			selector: "pubsub:topic=chat.general",
			resource: Resource{Domain: SelectorPubsub, Name: "chat.private"},
			allowed:  false,
		},
		{
			name:     "a wildcard suffix",
			selector: "pubsub:topic=chat.*",
			resource: Resource{Domain: SelectorPubsub, Name: "chat.general"},
			allowed:  true,
		},
		{
			name:     "a wildcard that does not reach",
			selector: "pubsub:topic=chat.*",
			resource: Resource{Domain: SelectorPubsub, Name: "billing.invoices"},
			allowed:  false,
		},
		{
			name:     "a wildcard is not a prefix of the prefix",
			selector: "pubsub:topic=chat.*",
			resource: Resource{Domain: SelectorPubsub, Name: "cha"},
			allowed:  false,
		},
		{
			// `chat.*` covering `chat.` itself is deliberate: `*` stands for
			// any run of characters including none.
			name:     "a wildcard matching nothing",
			selector: "pubsub:topic=chat.*",
			resource: Resource{Domain: SelectorPubsub, Name: "chat."},
			allowed:  true,
		},
		{
			name:     "a wildcard in the middle",
			selector: "pubsub:topic=chat.*.private",
			resource: Resource{Domain: SelectorPubsub, Name: "chat.team.private"},
			allowed:  true,
		},
		{
			name:     "a function by name",
			selector: "fn:name=checkout",
			resource: Resource{Domain: SelectorFn, Name: "checkout"},
			allowed:  true,
		},
		{
			name:     "another function",
			selector: "fn:name=checkout",
			resource: Resource{Domain: SelectorFn, Name: "refund"},
			allowed:  false,
		},
		{
			// The selector says nothing about the other domain, so it permits
			// nothing there. A grant narrowed to one topic must not become a
			// licence for every function.
			name:     "a resource in another domain entirely",
			selector: "pubsub:topic=chat.*",
			resource: Resource{Domain: SelectorFn, Name: "checkout"},
			allowed:  false,
		},
		{
			// The domain has to be compared, not inferred from the key: push
			// and pubsub both key their selectors on `topic`, so a grant over
			// one would otherwise silently cover the other.
			name:     "a domain whose selector key is the same",
			selector: "pubsub:topic=chat.*",
			resource: Resource{Domain: SelectorPush, Name: "chat.general"},
			allowed:  false,
		},
		{
			name:     "the wrong key for the domain",
			selector: "pubsub:name=chat.general",
			resource: Resource{Domain: SelectorPubsub, Name: "chat.general"},
			allowed:  false,
		},
		{
			name:     "a selector this binary cannot read",
			selector: "pubsub:",
			resource: Resource{Domain: SelectorPubsub, Name: "chat.general"},
			allowed:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Grant{Role: RoleRuntime, Resource: tc.selector}.Permits(tc.resource)
			if allowed := err == nil; allowed != tc.allowed {
				t.Errorf("Permits(%v) with %q = %v, want allowed=%v", tc.resource, tc.selector, err, tc.allowed)
			}
			if err != nil {
				var refused *ErrResourceNotPermitted
				if !errors.As(err, &refused) {
					t.Errorf("the refusal is not an ErrResourceNotPermitted: %v", err)
				}
			}
		})
	}
}

// A selector may narrow a domain to one action.
func TestGrant_permits_actions(t *testing.T) {
	readOnly := Grant{Role: RoleAdmin, Resource: "db:table=posts:read"}

	if err := readOnly.Permits(Resource{Domain: SelectorDB, Name: "posts", Action: ActionRead}); err != nil {
		t.Errorf("a read against a read grant was refused: %v", err)
	}
	if err := readOnly.Permits(Resource{Domain: SelectorDB, Name: "posts", Action: ActionWrite}); err == nil {
		t.Error("a write was allowed by a grant that says read")
	}
	// A selector with no action covers both.
	both := Grant{Role: RoleAdmin, Resource: "db:table=posts"}
	for _, action := range []Action{ActionRead, ActionWrite} {
		if err := both.Permits(Resource{Domain: SelectorDB, Name: "posts", Action: action}); err != nil {
			t.Errorf("an unqualified selector refused %s: %v", action, err)
		}
	}
}

// A storage selector crosses `/` on purpose: `avatars/*` is meant to cover
// `avatars/2026/03/me.png`. Stopping at the separator would grant less than it
// appears to, which nobody notices until something fails in production.
func TestGrant_permits_wildcardsCrossSeparators(t *testing.T) {
	g := Grant{Role: RoleRuntime, Resource: "storage:avatars/*"}
	for _, name := range []string{"avatars/me.png", "avatars/2026/03/me.png", "avatars/"} {
		if err := g.Permits(Resource{Domain: SelectorStorage, Name: name}); err != nil {
			t.Errorf("%q was refused by avatars/*: %v", name, err)
		}
	}
	if err := g.Permits(Resource{Domain: SelectorStorage, Name: "documents/tax.pdf"}); err == nil {
		t.Error("avatars/* covered documents/")
	}
}

func TestAuthorizeResource(t *testing.T) {
	resource := Resource{Domain: SelectorPubsub, Name: "billing.invoices"}

	// No grant in context: this only ever takes access away, and the scope gate
	// has already decided whether the caller may reach pub/sub at all.
	if err := AuthorizeResource(context.Background(), resource); err != nil {
		t.Errorf("a request with no grant was narrowed: %v", err)
	}

	whole := context.WithValue(context.Background(), ctxkeys.Permissions, PermissionsFor(RoleRuntime, ""))
	if err := AuthorizeResource(whole, resource); err != nil {
		t.Errorf("a whole-role grant was narrowed: %v", err)
	}

	narrow := context.WithValue(context.Background(), ctxkeys.Permissions,
		PermissionsFor(RoleRuntime, "pubsub:topic=chat.*"))
	if err := AuthorizeResource(narrow, resource); err == nil {
		t.Error("a grant for chat.* authorised billing.invoices")
	}
	if err := AuthorizeResource(narrow, Resource{Domain: SelectorPubsub, Name: "chat.general"}); err != nil {
		t.Errorf("a grant for chat.* refused chat.general: %v", err)
	}
}

func TestMatchGlob(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"", "", true},
		{"", "anything", false},
		{"*", "", true},
		{"*", "anything", true},
		{"exact", "exact", true},
		{"exact", "exactly", false},
		{"exact", "exac", false},
		{"a*", "a", true},
		{"a*", "abc", true},
		{"a*", "b", false},
		{"*z", "z", true},
		{"*z", "abz", true},
		{"*z", "za", false},
		{"a*z", "az", true},
		{"a*z", "abcz", true},
		{"a*z", "ab", false},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "abc", true},
		{"a*b*c", "acb", false},
		// The middle segment has to actually be there: prefix and suffix
		// matching alone would let `chat.*.private` cover `chat.private`.
		{"a*b*c", "axxc", false},
		{"chat.*.private", "chat.private", false},
		// The overlap trap: the run matched by `*` must not be allowed to
		// double-count the suffix.
		{"a*bc", "abc", true},
		{"a*bc", "abbc", true},
		{"ab*bc", "abc", false},
	} {
		if got := matchGlob(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}
