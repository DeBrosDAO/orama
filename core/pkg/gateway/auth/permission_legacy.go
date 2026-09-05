package auth

import "strings"

// Reading what is already on disk.
//
// Every key carries a comma-separated scope string in `api_keys.scopes`, and
// every grant carries a role and an optional selector. Neither changes shape
// here: this is the one place that turns them into permissions, so the rest of
// the codebase asks one question — does this credential permit this — and the
// storage catches up on its own schedule.
//
// Nothing about a credential's authority changes in the mapping. `admin` is
// every permission there is, a data-plane scope is its whole domain, and a
// selector is the permission it was trying to describe. If any of those were to
// widen, a key in the fleet would gain access on the deploy that shipped it,
// which is why the round trip is tested for every one of them.

// scopePermissions is the permission each legacy scope word stands for.
var scopePermissions = map[string]PermissionSet{
	ScopeAdmin:   Everything(),
	ScopeStorage: wholeDomains(DomainStorage),
	ScopePubsub:  wholeDomains(DomainPubsub),
	ScopeCache:   wholeDomains(DomainCache),
	ScopePush:    wholeDomains(DomainPush),
	ScopeWebRTC:  wholeDomains(DomainWebRTC),
	ScopeProxy:   wholeDomains(DomainProxy),
	// `invoke` is running a function, never changing one. Managing functions
	// was part of `admin`, and is `fn:manage` now.
	ScopeInvoke: {{Domain: DomainFn, Action: ActionInvoke, Resource: PermissionWildcard}},
}

// PermissionsFromScopes turns a stored scope string into permissions.
//
// A word this binary does not recognise contributes nothing. It was written by
// something that understood it, and inventing a meaning for it is how a typo
// becomes an authority.
func PermissionsFromScopes(stored string) PermissionSet {
	out := PermissionSet{}
	for _, word := range ParseScopes(stored).List() {
		out = append(out, scopePermissions[word]...)
	}
	return out
}

// PermissionsFor is what a role and its optional selector amount to.
//
// A role with no selector is the whole role. A role with one holds exactly the
// permission that selector describes, and only if the role reached it in the
// first place — a selector can narrow a grant and must never widen one.
func PermissionsFor(role Role, selector string) PermissionSet {
	whole := role.Permissions()
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return whole
	}

	narrowed, err := PermissionFromSelector(selector)
	if err != nil {
		// A selector this binary cannot read is not a licence.
		return PermissionSet{}
	}
	// The role has to have reached it: `storage:read:avatars/*` on a reader is
	// a description of something the reader never had.
	if !whole.Permits(Resource{Domain: narrowed.Domain, Action: narrowed.Action}) {
		return PermissionSet{}
	}
	return PermissionSet{narrowed}
}

// PermissionFromSelector reads a stored selector as the permission it was
// trying to describe.
//
// The old grammar carried a key before the pattern — `topic=`, `name=`,
// `table=`, `key=` — because a domain had no other way to say which of its
// things it meant, and an action could only be appended to the pattern
// (`table=posts:read`). Both are structural now.
func PermissionFromSelector(selector string) (Permission, error) {
	parsed, err := ParseSelector(selector)
	if err != nil {
		return Permission{}, err
	}

	value := parsed.Value
	if key, rest, found := strings.Cut(value, "="); found && strings.EqualFold(key, selectorKeyFor(parsed.Domain)) {
		value = rest
	}

	action := ActionAny
	// `fn:name=checkout` was only ever about invoking one: managing functions
	// was part of `admin`, which a selector could not narrow.
	if parsed.Domain == DomainFn {
		action = ActionInvoke
	}
	if base, trailing, found := cutLastAction(value); found {
		value, action = base, trailing
	}
	if value == "" {
		value = PermissionWildcard
	}

	return Permission{Domain: parsed.Domain, Action: action, Resource: value}, nil
}

// Permissions is what a role amounts to.
//
// `developer` is expressible now. It was left out of the role vocabulary
// because every control-plane route required the single `admin` scope, so it
// would have resolved to exactly the same authority as `admin` — a label
// claiming a boundary that was not there.
func (r Role) Permissions() PermissionSet {
	switch r {
	case RoleOwner, RoleAdmin:
		return Everything()
	case RoleDeveloper:
		// Everything an application needs, plus building and running it, plus
		// managing its functions. Not who may do it: members, the namespace's
		// own settings and the cluster stay with the owner and the admin.
		return append(
			DataPlanePermissions(),
			append(
				wholeDomains(DomainDB, DomainDeploy, DomainSecrets),
				Permission{Domain: DomainFn, Action: ActionManage, Resource: PermissionWildcard},
			)...,
		)
	case RoleRuntime:
		return DataPlanePermissions()
	default:
		// RoleReader, and anything this binary does not recognise. A newer
		// gateway may have written a role this one has never heard of, and
		// granting nothing is the only safe reading of it.
		return PermissionSet{}
	}
}
