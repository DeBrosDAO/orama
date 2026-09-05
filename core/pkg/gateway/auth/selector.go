package auth

import (
	"fmt"
	"sort"
	"strings"
)

// A resource selector narrows a grant to part of a namespace: `storage:avatars/*`
// rather than all of storage, `pubsub:topic=chat.*` rather than every topic.
//
// Nothing enforces one yet. The data plane checks a grant (see scopes.go) and
// has no notion of which object within it is being touched, so a selector is
// recorded intent and not a boundary. That is why a grant carrying one
// authorises nothing at all until the data-plane work lands (Phase 2): the
// alternative is to ignore the selector and hand over the whole role, which
// would turn "may write to storage:avatars/*" into "may write to all storage"
// silently — a narrower-looking grant that is in fact the wide one.
//
// So this file exists to validate what is stored, so that when enforcement
// arrives it is reading selectors that mean something rather than whatever
// anyone typed.

// SelectorDomain is the part of a namespace a selector narrows.
type SelectorDomain string

const (
	SelectorStorage SelectorDomain = "storage"
	SelectorDB      SelectorDomain = "db"
	SelectorPubsub  SelectorDomain = "pubsub"
	SelectorFn      SelectorDomain = "fn"
	SelectorCache   SelectorDomain = "cache"
	SelectorPush    SelectorDomain = "push"
)

// selectorDomains are the domains a selector may name, each tied to the grant
// it narrows. A selector for a domain a role does not reach is meaningless, so
// the domain is checked against the role's scope set when a grant is written.
var selectorDomains = map[SelectorDomain]string{
	SelectorStorage: ScopeStorage,
	SelectorDB:      ScopeAdmin,
	SelectorPubsub:  ScopePubsub,
	SelectorFn:      ScopeInvoke,
	SelectorCache:   ScopeCache,
	SelectorPush:    ScopePush,
}

// enforcedDomains are the domains whose data path can name the resource a
// request is about, and therefore apply a selector to it.
//
// A grant may only carry a selector in one of these. The alternative is to let
// somebody record `storage:avatars/*` and have it silently authorise nothing —
// which is what a stored-but-unenforced selector amounts to, and it reads as a
// working restriction in `orama members list`. Refusing at the point of writing
// is the honest version: a selector you can create is a selector that is
// applied.
//
// `db` is not here and cannot be until the control-plane vocabulary is split.
// It narrows `admin`, and `admin` is the whole control plane — so a grant
// narrowed to `db:table=posts:read` would hold `admin` everywhere except the
// database routes that narrow it, which is a wider grant wearing a narrower
// name. The same is true of deployments. Both wait on the scope split that
// feat-367's `developer` note describes.
//
// `push` is not here for a different reason: the push API has no topic. A send
// names a user and optionally a channel, so `push:topic=` names a field that
// does not exist, and what a push actually touches is somebody's device.
//
// `db` also needs the statement parsed for the tables it touches and in which
// direction, which is a bigger job than the denylist in sqlguard.go.
var enforcedDomains = map[SelectorDomain]bool{
	SelectorPubsub:  true,
	SelectorFn:      true,
	SelectorStorage: true,
	SelectorCache:   true,
}

// SelectorEnforced reports whether a selector in this domain is applied by the
// data path.
func SelectorEnforced(domain SelectorDomain) bool { return enforcedDomains[domain] }

// EnforcedSelectorDomains returns the domains a grant may be narrowed to today.
func EnforcedSelectorDomains() []string {
	out := make([]string, 0, len(enforcedDomains))
	for d := range enforcedDomains {
		out = append(out, string(d))
	}
	sort.Strings(out)
	return out
}

// maxSelectorLength bounds what goes in the column. A selector is a short
// pattern; anything longer is a mistake or an attempt to put something else in
// the row.
const maxSelectorLength = 256

// Selector is a parsed resource selector.
type Selector struct {
	Domain SelectorDomain
	// Value is everything after the domain, verbatim: `avatars/*`,
	// `table=posts:read`, `topic=chat.*`. Its shape is the domain's business
	// and is not interpreted here.
	Value string
}

func (s Selector) String() string { return string(s.Domain) + ":" + s.Value }

// RequiredScope is the grant a selector narrows. A grant whose role does not
// hold it cannot carry this selector.
func (s Selector) RequiredScope() string { return selectorDomains[s.Domain] }

// ParseSelector reads a selector, or says why it is not one.
func ParseSelector(raw string) (Selector, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Selector{}, fmt.Errorf("a resource selector cannot be empty; leave it unset for the whole role")
	}
	if len(raw) > maxSelectorLength {
		return Selector{}, fmt.Errorf("a resource selector is at most %d characters", maxSelectorLength)
	}
	for _, c := range raw {
		// Printable ASCII only. A selector goes into a row that is read back
		// and compared; control characters and non-ASCII lookalikes are how two
		// selectors that display identically stop matching each other.
		if c < 0x21 || c > 0x7e {
			return Selector{}, fmt.Errorf("a resource selector holds only printable characters and no spaces")
		}
	}

	domain, value, ok := strings.Cut(raw, ":")
	if !ok || value == "" {
		return Selector{}, fmt.Errorf("a resource selector is <domain>:<pattern>, e.g. storage:avatars/*")
	}
	d := SelectorDomain(strings.ToLower(domain))
	if _, known := selectorDomains[d]; !known {
		return Selector{}, fmt.Errorf("unknown selector domain %q (valid: %s)", domain, strings.Join(SelectorDomains(), ", "))
	}
	return Selector{Domain: d, Value: value}, nil
}

// SelectorDomains returns every valid domain, sorted, for an error message.
func SelectorDomains() []string {
	out := make([]string, 0, len(selectorDomains))
	for d := range selectorDomains {
		out = append(out, string(d))
	}
	sort.Strings(out)
	return out
}

// NormalizeStoragePath is the name a storage selector matches against.
//
// The name comes from the client — a multipart filename, or the `name` field of
// a JSON upload — and it is the only thing a `storage:avatars/*` selector has
// to compare against. So it has to be one canonical string: `/avatars/me.png`,
// `avatars//me.png` and `avatars/me.png` are the same object, and a selector
// that matched one and not the others would be a boundary that depends on how
// the caller typed the request.
//
// `..` is refused rather than resolved. Resolving it is how `avatars/../keys/x`
// matches `avatars/*` while naming something else entirely; refusing it costs a
// caller nothing, because a storage name is a label and not a filesystem path.
func NormalizeStoragePath(name string) (string, error) {
	cleaned := strings.TrimSpace(name)
	if cleaned == "" {
		return "", nil
	}
	if len(cleaned) > maxStoragePathLength {
		return "", fmt.Errorf("a storage name is at most %d characters", maxStoragePathLength)
	}

	segments := strings.Split(cleaned, "/")
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		switch segment {
		case "":
			// A repeated separator. Two names that differ only by one are the
			// same object.
			continue
		case ".":
			continue
		case "..":
			return "", fmt.Errorf("a storage name cannot contain \"..\": it is a label, not a path, " +
				"and resolving one would let a name match a prefix it is not under")
		}
		out = append(out, segment)
	}
	return strings.Join(out, "/"), nil
}

// maxStoragePathLength bounds the name column and the selector comparison.
const maxStoragePathLength = 1024
