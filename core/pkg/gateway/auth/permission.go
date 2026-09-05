package auth

import (
	"fmt"
	"sort"
	"strings"
)

// One model for what a credential may do.
//
// There were two, bolted together. **Scopes** were a flat set of eight words
// stored comma-separated on a key: seven data-plane ones, and `admin`, which
// covered the entire control plane. **Selectors** were `domain:pattern` strings
// on a grant, narrowing a role to part of a namespace. Neither could express
// the other, so a translation sat between them — a grant with a selector held
// exactly the scope that selector narrowed, which the data path then narrowed
// again — and three separate cases where a selector authorised nothing at all.
//
// It cost more than complexity. `developer` could not be a role, because every
// control-plane route required the one `admin` word, so it would have been a
// label claiming a boundary that was not there. A grant could not be narrowed
// to a table or a deployment for the same reason: `db:table=posts:read` would
// have held `admin` everywhere except the database routes that narrowed it — a
// wider grant wearing a narrower name.
//
// A permission is one sentence: **in this domain, this action, on this
// resource.** A scope is the case where the action and the resource are both
// `*`; `admin` is the case where all three are. There is nothing left to
// translate, and a route says what it does rather than which word it needs.

// PermissionWildcard matches anything in its position.
const PermissionWildcard = "*"

// Domain is the part of the platform a permission is about.
type Domain string

const (
	// --- the data plane: safe in a client that ships to users ------------
	DomainStorage Domain = "storage" // objects in IPFS
	DomainPubsub  Domain = "pubsub"  // topics
	DomainCache   Domain = "cache"   // keys in a distributed map
	DomainPush    Domain = "push"    // devices and notifications
	DomainWebRTC  Domain = "webrtc"  // TURN credentials and signalling
	DomainProxy   Domain = "proxy"   // the anonymity tunnel
	DomainFn      Domain = "fn"      // functions: invoking and managing

	// --- the control plane: what `admin` used to be, in parts ------------
	DomainDB        Domain = "db"        // the tenant's SQL databases and raw RQLite
	DomainDeploy    Domain = "deploy"    // deployments, their domains and their logs
	DomainSecrets   Domain = "secrets"   // function secrets, env, push credentials
	DomainMembers   Domain = "members"   // grants, keys, ownership
	DomainNamespace Domain = "namespace" // a namespace's own settings and its deletion
	DomainAudit     Domain = "audit"     // the record
	DomainOperator  Domain = "operator"  // the cluster itself
)

// domains is every domain a permission may name, and what it covers. A domain
// nothing recognises is refused when a permission is parsed, so a typo is a
// refusal rather than a permission that never matches.
var domains = map[Domain]string{
	DomainStorage:   "objects in storage",
	DomainPubsub:    "pub/sub topics",
	DomainCache:     "cache keys",
	DomainPush:      "push devices and sends",
	DomainWebRTC:    "TURN credentials and signalling",
	DomainProxy:     "the anonymity tunnel",
	DomainFn:        "functions",
	DomainDB:        "databases",
	DomainDeploy:    "deployments",
	DomainSecrets:   "secrets and environment",
	DomainMembers:   "members, keys and ownership",
	DomainNamespace: "a namespace's settings",
	DomainAudit:     "the audit trail",
	DomainOperator:  "cluster operations",
}

// Action is what is being done.
//
// `read` and `write` are the two most domains need. `invoke` and `manage`
// belong to functions, where running one and changing one are different
// authorities and neither is a read or a write of the same thing.
const (
	ActionInvoke Action = "invoke"
	ActionManage Action = "manage"
	ActionAny    Action = PermissionWildcard
)

// actions is every action a permission may name.
var actions = map[Action]struct{}{
	ActionRead:   {},
	ActionWrite:  {},
	ActionInvoke: {},
	ActionManage: {},
	ActionAny:    {},
}

// Permission is one thing a credential may do.
type Permission struct {
	Domain Domain
	Action Action
	// Resource is the object, or `*` for every object in the domain. Its
	// shape is the domain's: a storage path, a topic, a table, a deployment
	// name. `*` inside it matches any run of characters.
	Resource string
}

func (p Permission) String() string {
	return string(p.Domain) + ":" + string(p.Action) + ":" + p.Resource
}

// Permits reports whether this permission covers a request.
//
// A wildcard in any position matches anything in that position, and the
// resource is matched as a glob — so `storage:*:avatars/*` covers reading and
// writing anything under `avatars/`, and `*:*:*` covers everything.
func (p Permission) Permits(r Resource) bool {
	if !p.permitsDomain(r.Domain, r.Action) {
		return false
	}
	// A request that names no object is one nothing could name — a CID this
	// namespace recorded no name for, an upload with no name. A narrowed
	// permission does not cover it: "I could not work out what you are
	// touching" is not a reason to allow it. Only an unrestricted resource
	// does.
	//
	// This is not the gate's question. The gate asks PermitsDomain, before the
	// handler knows which object; the two are different questions and used to
	// be the same one, which is how an unnamed object slipped through.
	if r.Name == "" {
		return p.Resource == PermissionWildcard
	}
	return matchGlob(p.Resource, r.Name)
}

// ParsePermission reads `<domain>:<action>:<resource>`.
//
// The two-part form `<domain>:<resource>` is accepted and means every action —
// it is what a selector looked like, and what somebody will type.
func ParsePermission(raw string) (Permission, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Permission{}, fmt.Errorf("a permission cannot be empty")
	}
	if len(raw) > maxSelectorLength {
		return Permission{}, fmt.Errorf("a permission is at most %d characters", maxSelectorLength)
	}
	for _, c := range raw {
		if c < 0x21 || c > 0x7e {
			return Permission{}, fmt.Errorf("a permission holds only printable characters and no spaces")
		}
	}

	parts := strings.Split(raw, ":")
	var p Permission
	switch len(parts) {
	case 1:
		// A bare domain: everything in it. `storage` is `storage:*:*`.
		p = Permission{Domain: Domain(parts[0]), Action: ActionAny, Resource: PermissionWildcard}
	case 2:
		p = Permission{Domain: Domain(parts[0]), Action: ActionAny, Resource: parts[1]}
	case 3:
		p = Permission{Domain: Domain(parts[0]), Action: Action(parts[1]), Resource: parts[2]}
	default:
		return Permission{}, fmt.Errorf("a permission is <domain>:<action>:<resource>, e.g. storage:read:avatars/*")
	}

	p.Domain = Domain(strings.ToLower(string(p.Domain)))
	p.Action = Action(strings.ToLower(string(p.Action)))
	if p.Resource == "" {
		p.Resource = PermissionWildcard
	}

	if p.Domain != Domain(PermissionWildcard) {
		if _, known := domains[p.Domain]; !known {
			return Permission{}, fmt.Errorf("unknown permission domain %q (valid: %s)",
				parts[0], strings.Join(AllDomains(), ", "))
		}
	}
	if _, known := actions[p.Action]; !known {
		return Permission{}, fmt.Errorf("unknown action %q in %q (valid: read, write, invoke, manage, *)",
			p.Action, raw)
	}
	return p, nil
}

// AllDomains returns every domain, sorted, for an error message.
func AllDomains() []string {
	out := make([]string, 0, len(domains))
	for d := range domains {
		out = append(out, string(d))
	}
	sort.Strings(out)
	return out
}

// PermissionSet is everything a credential may do.
type PermissionSet []Permission

// Permits reports whether anything in the set covers a request.
func (ps PermissionSet) Permits(r Resource) bool {
	for _, p := range ps {
		if p.Permits(r) {
			return true
		}
	}
	return false
}

// PermitsDomain reports whether the set reaches a domain and action at all,
// whatever the object.
//
// This is the question the scope gate asks: it runs before the handler, which
// is the only thing that knows which object a request is about. The handler
// asks Permits with the object once it has one, and a set that reaches the
// domain but not that object is refused there.
func (ps PermissionSet) PermitsDomain(domain Domain, action Action) bool {
	for _, p := range ps {
		if p.permitsDomain(domain, action) {
			return true
		}
	}
	return false
}

// permitsDomain is Permits without the resource: does this permission apply to
// this domain and action at all, whatever object the request turns out to be
// about.
func (p Permission) permitsDomain(domain Domain, action Action) bool {
	if p.Domain != Domain(PermissionWildcard) && !strings.EqualFold(string(p.Domain), string(domain)) {
		return false
	}
	if p.Action != ActionAny && action != "" && !strings.EqualFold(string(p.Action), string(action)) {
		return false
	}
	return true
}

// IsAdmin reports whether the set is the unrestricted one — what the single
// `admin` scope used to be.
func (ps PermissionSet) IsAdmin() bool {
	for _, p := range ps {
		if p.Domain == Domain(PermissionWildcard) && p.Action == ActionAny && p.Resource == PermissionWildcard {
			return true
		}
	}
	return false
}

// List renders the set as stable, sorted strings.
func (ps PermissionSet) List() []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	sort.Strings(out)
	return out
}

func (ps PermissionSet) String() string { return strings.Join(ps.List(), ",") }

// Everything returns the unrestricted set.
func Everything() PermissionSet {
	return PermissionSet{{Domain: Domain(PermissionWildcard), Action: ActionAny, Resource: PermissionWildcard}}
}

// wholeDomains returns one permission per domain, unrestricted within it.
func wholeDomains(list ...Domain) PermissionSet {
	out := make(PermissionSet, 0, len(list))
	for _, d := range list {
		out = append(out, Permission{Domain: d, Action: ActionAny, Resource: PermissionWildcard})
	}
	return out
}

// DataPlanePermissions is what a logged-in user, or a runtime credential,
// holds: everything an application does, and nothing an operator does.
func DataPlanePermissions() PermissionSet {
	return append(
		wholeDomains(DomainStorage, DomainPubsub, DomainCache, DomainPush, DomainWebRTC, DomainProxy),
		Permission{Domain: DomainFn, Action: ActionInvoke, Resource: PermissionWildcard},
	)
}
