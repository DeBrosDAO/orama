package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// A grant says what a principal may do; a selector says which part of it. This
// is where the second half is applied.
//
// Until now a selector was recorded and nothing read it, so a grant carrying
// one authorised nothing at all — the only safe reading, since handing over the
// whole role would turn "may publish to chat.*" into "may publish to
// everything". This makes the narrow grant mean what it says on the paths that
// can name the resource being touched.
//
// The rule is one sentence: a grant with no selector is the whole role, and a
// grant with one permits exactly what the selector matches. A request whose
// resource this code cannot name is refused for a grant that has a selector,
// because "I could not work out what you are touching" is not a reason to allow
// it.

// Action is what is being done to a resource. It is only meaningful for domains
// that distinguish reading from writing.
type Action string

const (
	ActionRead  Action = "read"
	ActionWrite Action = "write"
)

// Resource is the thing a request is about: which domain, and which object
// within it.
type Resource struct {
	Domain SelectorDomain
	// Name is the object: a topic, a function name, a storage path, a table.
	Name string
	// Action is read or write where the domain distinguishes them, and empty
	// where it does not.
	Action Action
}

func (r Resource) String() string {
	if r.Action == "" {
		return string(r.Domain) + " " + r.Name
	}
	return string(r.Domain) + " " + r.Name + " (" + string(r.Action) + ")"
}

// ErrResourceNotPermitted is returned when a grant's selector does not cover
// the resource a request is about.
type ErrResourceNotPermitted struct {
	Resource Resource
	Selector string
}

func (e *ErrResourceNotPermitted) Error() string {
	return fmt.Sprintf("this credential is granted %q, which does not cover %s", e.Selector, e.Resource)
}

// Permits reports whether this grant covers a resource.
//
// A grant with no selector is the whole role and covers everything the role's
// scopes reach; the scope gate has already decided that. A grant with a
// selector covers only what the selector matches, and only in its own domain —
// a `pubsub:topic=chat.*` grant says nothing about storage, so it permits no
// storage at all.
func (g Grant) Permits(r Resource) error {
	selector := strings.TrimSpace(g.Resource)
	if selector == "" {
		return nil
	}
	parsed, err := ParseSelector(selector)
	if err != nil {
		// A selector this binary cannot read is not a licence. It was written
		// by something that understood it; this process does not, so it
		// permits nothing.
		return &ErrResourceNotPermitted{Resource: r, Selector: selector}
	}
	if parsed.Domain != r.Domain || !matchesSelectorValue(parsed.Value, r) {
		return &ErrResourceNotPermitted{Resource: r, Selector: selector}
	}
	return nil
}

// AuthorizeResource refuses a request whose grant does not cover the resource
// it is about.
//
// A request with no grant in its context is not narrowed: the caller reached
// here through the scope gate, which is what decides whether they may touch
// this class of thing at all. This only ever takes access away.
func AuthorizeResource(ctx context.Context, r Resource) error {
	grant, _ := ctx.Value(ctxkeys.Grant).(*Grant)
	if grant == nil {
		return nil
	}
	return grant.Permits(r)
}

// matchesSelectorValue applies a selector's value to a resource.
//
// The shapes are the domain's: `avatars/*` is a path, `topic=chat.*` is a keyed
// pattern, `table=posts:read` is a keyed pattern with an action. They are
// parsed here rather than in ParseSelector because ParseSelector's job is to
// say whether a selector is well-formed at all, and a new domain should not
// have to change it.
func matchesSelectorValue(value string, r Resource) bool {
	pattern := value
	if key, rest, ok := strings.Cut(value, "="); ok {
		if !strings.EqualFold(key, selectorKeyFor(r.Domain)) {
			return false
		}
		pattern = rest
	}

	// A trailing `:read` or `:write` narrows the pattern to one action.
	if base, action, ok := cutLastAction(pattern); ok {
		if r.Action != action {
			return false
		}
		pattern = base
	}

	return matchGlob(pattern, r.Name)
}

// selectorKeyFor is the word a domain's selector uses before the `=`.
//
// `storage:avatars/*` has no key because a storage selector is a path and
// nothing else; the others name what they are matching so that a second kind of
// selector can be added to a domain later without the old ones becoming
// ambiguous.
func selectorKeyFor(domain SelectorDomain) string {
	switch domain {
	case SelectorPubsub:
		return "topic"
	case SelectorFn:
		return "name"
	case SelectorDB:
		return "table"
	case SelectorPush:
		return "topic"
	case SelectorCache:
		return "key"
	default:
		return ""
	}
}

// cutLastAction splits a trailing `:read` or `:write` off a pattern.
func cutLastAction(pattern string) (string, Action, bool) {
	for _, action := range []Action{ActionRead, ActionWrite} {
		if base, ok := strings.CutSuffix(pattern, ":"+string(action)); ok {
			return base, action, true
		}
	}
	return pattern, "", false
}

// matchGlob matches a pattern against a name, where `*` stands for any run of
// characters including none.
//
// `*` deliberately crosses `/`: a storage selector of `avatars/*` is meant to
// cover `avatars/2026/03/me.png`, and a rule that stopped at the separator
// would quietly grant less than it appears to — which is worse than granting
// more, because nobody notices until something fails in production.
func matchGlob(pattern, name string) bool {
	if pattern == "" {
		return name == ""
	}
	parts := strings.Split(pattern, "*")

	// No wildcard: exact match.
	if len(parts) == 1 {
		return pattern == name
	}

	if !strings.HasPrefix(name, parts[0]) {
		return false
	}
	name = name[len(parts[0]):]

	last := parts[len(parts)-1]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(name, part)
		if i < 0 {
			return false
		}
		name = name[i+len(part):]
	}
	return strings.HasSuffix(name, last)
}
