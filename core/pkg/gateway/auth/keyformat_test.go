package auth

import (
	"strings"
	"testing"
)

// The format is what somebody who finds a leaked string can act on. Three
// things have to hold: it is recognisably ours, a mistyped one is refused
// without a database, and it says nothing about which tenant it belongs to.

func TestNewKey_roundTrips(t *testing.T) {
	seen := map[string]bool{}
	for _, want := range []KeyType{KeyTypeService, KeyTypeRuntime} {
		for i := 0; i < 32; i++ {
			key, err := NewKey(want)
			if err != nil {
				t.Fatalf("NewKey(%s): %v", want, err)
			}
			got, err := ParseKey(key)
			if err != nil {
				t.Fatalf("a key we minted does not parse: %q: %v", key, err)
			}
			if got != want {
				t.Errorf("%q says it is a %s, want %s", key, got, want)
			}
			if !strings.HasPrefix(key, KeyPrefix+"_"+string(want)+"_") {
				t.Errorf("%q is not shaped like a key", key)
			}
			if seen[key] {
				t.Fatalf("%q came up twice", key)
			}
			seen[key] = true
		}
	}
}

// The namespace used to be in the key. A key pasted into an issue, a log line
// or a support ticket published which tenant it belonged to, before anybody had
// decided whether the key itself was still live.
func TestNewKey_carriesNoNamespace(t *testing.T) {
	key, err := NewKey(KeyTypeRuntime)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	if strings.Contains(key, ":") {
		t.Errorf("%q carries a colon; the namespace used to be after it", key)
	}
	if n := strings.Count(key, "_"); n != 3 {
		t.Errorf("%q has %d separators, want 3 — prefix, type, payload, checksum", key, n)
	}
}

// The checksum is what lets anything outside this codebase recognise a leaked
// key, and what turns a typo into an immediate refusal rather than a query.
func TestParseKey_refuses(t *testing.T) {
	good, err := NewKey(KeyTypeService)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	parts := strings.Split(good, "_")

	tests := []struct{ name, key string }{
		{"nothing", ""},
		{"a random string", "hunter2"},
		{"a legacy key", "ak_abc123:myns"},
		{"the wrong prefix", "stripe_" + parts[1] + "_" + parts[2] + "_" + parts[3]},
		// Well-formed and correctly checksummed: the type is the only thing
		// wrong with it, so this is what proves the type is checked at all.
		{"a type that does not exist", wellFormedWithType("xx")},
		{"a type that does not exist, uppercased", wellFormedWithType("SK")},
		{"an empty payload", KeyPrefix + "_sk__" + parts[3]},
		{"a payload that is not base62", KeyPrefix + "_sk_abc-def_" + parts[3]},
		{"a truncated key", good[:len(good)-3]},
		{"one character changed in the payload", flipLast(parts[2], good)},
		{"the wrong checksum", strings.Join(parts[:3], "_") + "_ZZZZ"},
		{"an extra segment", good + "_extra"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseKey(tc.key); err == nil {
				t.Errorf("accepted %q", tc.key)
			}
			if LooksLikeKey(tc.key) {
				t.Errorf("LooksLikeKey(%q) is true", tc.key)
			}
		})
	}

	// Surrounding whitespace is what a paste brings with it.
	if _, err := ParseKey("  " + good + "\n"); err != nil {
		t.Errorf("a pasted key was refused: %v", err)
	}
}

// wellFormedWithType builds a key with a valid checksum and the given type.
func wellFormedWithType(keyType string) string {
	body := KeyPrefix + "_" + keyType + "_2fJ8xQvMz9"
	return body + "_" + checksumOf(body)
}

// flipLast changes one character of the payload, leaving the checksum stale.
func flipLast(payload, key string) string {
	last := payload[len(payload)-1]
	replacement := byte('0')
	if last == '0' {
		replacement = '1'
	}
	return strings.Replace(key, payload, payload[:len(payload)-1]+string(replacement), 1)
}

// The label follows the grants rather than being chosen, so it cannot say
// "runtime" about a key that holds the control plane.
func TestKeyTypeFor(t *testing.T) {
	if got := KeyTypeFor("admin"); got != KeyTypeService {
		t.Errorf("an admin key is labelled %q", got)
	}
	if got := KeyTypeFor("invoke,storage"); got != KeyTypeRuntime {
		t.Errorf("a data-plane key is labelled %q", got)
	}
	if got := KeyTypeFor(""); got != KeyTypeRuntime {
		t.Errorf("a key with no grants is labelled %q; an empty scope set denies", got)
	}
}

func TestIsLegacyKey(t *testing.T) {
	if !IsLegacyKey("ak_abc:ns") || !IsLegacyKey("AK_ABC:NS") {
		t.Error("a legacy key was not recognised")
	}
	key, _ := NewKey(KeyTypeRuntime)
	if IsLegacyKey(key) {
		t.Error("a current key was called a legacy one")
	}
}

// This is the one that matters. hasWalletJWT is what stops an extracted runtime
// key acting as a logged-in user, and it is built on this: a key must never be
// read as a wallet.
func TestIsWalletSubject(t *testing.T) {
	key, err := NewKey(KeyTypeService)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	for _, sub := range []string{
		"0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB",
		"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM",
	} {
		if !IsWalletSubject(sub) {
			t.Errorf("%q is a wallet and was not recognised as one", sub)
		}
		if IsAPIKeySubject(sub) {
			t.Errorf("%q was read as an API key", sub)
		}
	}

	for _, sub := range []string{
		key,
		"ak_abc123:myns",
		"orama_rk_short_x",
		"did:ethr:not-an-address",
		"anything else at all",
	} {
		if IsWalletSubject(sub) {
			t.Errorf("%q was read as a wallet; a key read as a wallet is a logged-in user", sub)
		}
		if !IsAPIKeySubject(sub) {
			t.Errorf("%q is neither a wallet nor a key", sub)
		}
	}

	// An empty subject is neither.
	if IsWalletSubject("") || IsAPIKeySubject("") {
		t.Error("an empty subject was classified as something")
	}
}
