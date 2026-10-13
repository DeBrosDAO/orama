package auth

import (
	"strings"
	"testing"
)

func TestParseSelector(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		domain SelectorDomain
		value  string
	}{
		{"storage:avatars/*", SelectorStorage, "avatars/*"},
		{"db:table=posts:read", SelectorDB, "table=posts:read"},
		{"pubsub:topic=chat.*", SelectorPubsub, "topic=chat.*"},
		{"fn:name=checkout", SelectorFn, "name=checkout"},
		{"STORAGE:avatars/*", SelectorStorage, "avatars/*"},
	} {
		got, err := ParseSelector(tc.raw)
		if err != nil {
			t.Errorf("ParseSelector(%q): %v", tc.raw, err)
			continue
		}
		if got.Domain != tc.domain || got.Value != tc.value {
			t.Errorf("ParseSelector(%q) = %+v", tc.raw, got)
		}
		// The value is the domain's business and is not interpreted here, so
		// the colon inside `table=posts:read` must survive.
		if got.String() != strings.ToLower(string(tc.domain))+":"+tc.value {
			t.Errorf("%q did not round-trip: %q", tc.raw, got.String())
		}
	}
}

func TestParseSelector_refuses(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"nothing", ""},
		{"only whitespace", "   "},
		{"no domain separator", "avatars/*"},
		{"an empty pattern", "storage:"},
		{"an unknown domain", "filesystem:/etc/passwd"},
		{"a space, which a stored selector compared for equality must not have", "storage: avatars/*"},
		{"a newline inside the pattern", "storage:av\natars/*"},
		{"a non-ASCII lookalike, which displays the same and does not match", "storage:аvatars/*"},
		{"something far too long to be a pattern", "storage:" + strings.Repeat("a", maxSelectorLength)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := ParseSelector(tc.raw); err == nil {
				t.Errorf("accepted %q as %+v", tc.raw, got)
			}
		})
	}
}

// Whitespace around the value is trimmed rather than refused: a selector typed
// on a command line or pasted into JSON picks it up, and refusing would be a
// puzzle rather than a protection. What is refused is whitespace *inside* it,
// which is what makes two selectors that display the same stop matching.
func TestParseSelector_trimsSurroundingWhitespace(t *testing.T) {
	got, err := ParseSelector("  storage:avatars/*\n")
	if err != nil {
		t.Fatalf("a pasted selector was refused: %v", err)
	}
	if got.String() != "storage:avatars/*" {
		t.Errorf("got %q", got.String())
	}
}

// A selector names the grant it narrows, so a grant whose role does not hold
// that grant cannot carry it — the check that stops a reader being given
// `storage:avatars/*` and reading as though the selector meant something.
func TestSelector_requiredScope(t *testing.T) {
	for raw, want := range map[string]string{
		"storage:avatars/*":   ScopeStorage,
		"pubsub:topic=chat.*": ScopePubsub,
		"fn:name=checkout":    ScopeInvoke,
		"cache:session:*":     ScopeCache,
		"push:topic=alerts":   ScopePush,
		"db:table=posts:read": ScopeAdmin,
	} {
		selector, err := ParseSelector(raw)
		if err != nil {
			t.Fatalf("ParseSelector(%q): %v", raw, err)
		}
		if got := selector.RequiredScope(); got != want {
			t.Errorf("%q narrows %q, want %q", raw, got, want)
		}
	}
}

// The domains listed in an error message have to be the ones the parser
// accepts, or the message sends someone to a value that is refused.
func TestSelectorDomains_matchWhatIsAccepted(t *testing.T) {
	for _, domain := range SelectorDomains() {
		if _, err := ParseSelector(domain + ":anything"); err != nil {
			t.Errorf("%q is advertised and refused: %v", domain, err)
		}
	}
	if len(SelectorDomains()) != len(selectorDomains) {
		t.Error("a domain is missing from the list clients are shown")
	}
}
