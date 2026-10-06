package auth

import "testing"

// The name a storage selector matches is client-supplied, and it is the only
// thing `storage:avatars/*` has to compare against. Two names that mean the
// same object have to normalise to one string, and a name that is not under a
// prefix must not be able to spell itself as though it were.
func TestNormalizeStoragePath(t *testing.T) {
	for name, want := range map[string]string{
		"avatars/me.png":   "avatars/me.png",
		"/avatars/me.png":  "avatars/me.png",
		"avatars//me.png":  "avatars/me.png",
		"avatars/me.png/":  "avatars/me.png",
		"  avatars/me.png": "avatars/me.png",
		"./avatars/me.png": "avatars/me.png",
		"me.png":           "me.png",
		"":                 "",
		"   ":              "",
		"/":                "",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := NormalizeStoragePath(name)
			if err != nil {
				t.Fatalf("NormalizeStoragePath(%q): %v", name, err)
			}
			if got != want {
				t.Errorf("NormalizeStoragePath(%q) = %q, want %q", name, got, want)
			}
		})
	}
}

// `avatars/../keys/x` is not under `avatars/`, and resolving it would let a
// selector be walked out of. Refusing costs a caller nothing: a storage name is
// a label, not a filesystem path.
func TestNormalizeStoragePath_refusesDotDot(t *testing.T) {
	for _, name := range []string{
		"avatars/../keys/x",
		"../keys/x",
		"avatars/..",
		"..",
		"a/b/../../../etc/passwd",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := NormalizeStoragePath(name); err == nil {
				t.Errorf("NormalizeStoragePath(%q) = %q, want a refusal", name, got)
			}
		})
	}
}

func TestNormalizeStoragePath_boundsTheLength(t *testing.T) {
	long := make([]byte, maxStoragePathLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := NormalizeStoragePath(string(long)); err == nil {
		t.Error("a name longer than the column was accepted")
	}
}

// The normalisation exists to make the selector comparison mean one thing, so
// the two have to be tested together.
func TestStorageSelector_matchesTheNormalizedName(t *testing.T) {
	grant := Grant{Role: RoleRuntime, Resource: "storage:avatars/*"}

	for name, permitted := range map[string]bool{
		"avatars/me.png":            true,
		"/avatars/me.png":           true,
		"avatars/2026/03/me.png":    true,
		"avatars":                   false,
		"keys/private.pem":          false,
		"avatars-elsewhere/me.png":  false,
		"not-avatars/avatars/x.png": false,
	} {
		t.Run(name, func(t *testing.T) {
			normalized, err := NormalizeStoragePath(name)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			err = grant.Permits(Resource{Domain: SelectorStorage, Name: normalized, Action: ActionRead})
			if permitted && err != nil {
				t.Errorf("%q was refused: %v", name, err)
			}
			if !permitted && err == nil {
				t.Errorf("%q was permitted by storage:avatars/*", name)
			}
		})
	}

	// The wildcard crosses `/` on purpose: `avatars/*` is meant to cover
	// everything under it, and a rule that stopped at the separator would grant
	// less than it appears to.
	t.Run("a whole-role grant is not narrowed", func(t *testing.T) {
		if err := (Grant{Role: RoleRuntime}).Permits(
			Resource{Domain: SelectorStorage, Name: "keys/private.pem"}); err != nil {
			t.Errorf("an unnarrowed grant was refused: %v", err)
		}
	})

	// An object this code cannot name is refused for a narrowed grant: "I could
	// not work out what you are touching" is not a reason to allow it.
	t.Run("an unnamed object is refused", func(t *testing.T) {
		if err := grant.Permits(Resource{Domain: SelectorStorage, Name: ""}); err == nil {
			t.Error("an object with no recorded name was permitted by a narrowed grant")
		}
	})

	// A selector in another domain says nothing about storage.
	t.Run("another domain permits no storage", func(t *testing.T) {
		other := Grant{Role: RoleRuntime, Resource: "pubsub:topic=chat.*"}
		if err := other.Permits(Resource{Domain: SelectorStorage, Name: "avatars/me.png"}); err == nil {
			t.Error("a pubsub selector permitted storage")
		}
	})
}

// A cache selector matches the map and the key together, so a map-wide
// narrowing is expressible.
func TestCacheSelector_matchesTheMapAndTheKey(t *testing.T) {
	grant := Grant{Role: RoleRuntime, Resource: "cache:key=sessions/*"}

	for name, permitted := range map[string]bool{
		"sessions/user:1": true,
		"sessions/":       true,
		"sessions":        false,
		"tokens/user:1":   false,
		"other/sessions":  false,
	} {
		t.Run(name, func(t *testing.T) {
			err := grant.Permits(Resource{Domain: SelectorCache, Name: name, Action: ActionRead})
			if permitted && err != nil {
				t.Errorf("%q was refused: %v", name, err)
			}
			if !permitted && err == nil {
				t.Errorf("%q was permitted by cache:key=sessions/*", name)
			}
		})
	}

	// The key is compared, so a selector naming the wrong one is not a match.
	t.Run("the wrong selector key is not a match", func(t *testing.T) {
		wrong := Grant{Role: RoleRuntime, Resource: "cache:topic=sessions/*"}
		if err := wrong.Permits(Resource{Domain: SelectorCache, Name: "sessions/user:1"}); err == nil {
			t.Error("a selector keyed on `topic` matched a cache key")
		}
	})
}
