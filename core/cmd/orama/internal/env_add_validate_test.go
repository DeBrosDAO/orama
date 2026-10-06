package cli

import (
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// An environment no command could use was stored as given (stagenet e2e,
// 2026-09-30). It is a usage error now, before anything is written.
func TestValidateNewEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, url string
		ok        bool
	}{
		{"stagenet", "https://stagenet.example", true},
		{"local", "http://localhost:6001", true},
		{"loopback", "http://127.0.0.1:6001", true},
		{"loopback6", "http://[::1]:6001", true},
		{"remote-http", "http://gw.example", false},
		{"", "https://g.example", false},
		{"   ", "https://g.example", false},
		{"x", "", false},
		{"x", "https://", false},
		{"x", "not a url", false},
		{"x", "ftp://g.example", false},
		{"x", "g.example", false},
	} {
		err := validateNewEnvironment(tc.name, tc.url)
		if (err == nil) != tc.ok {
			t.Errorf("(%q, %q): err %v, want ok=%v", tc.name, tc.url, err, tc.ok)
		}
		if err != nil && clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("(%q, %q): code %d, want usage", tc.name, tc.url, clierr.CodeOf(err))
		}
	}
}

// A negative lifetime was dropped before the request and the key minted with
// the default (stagenet e2e, 2026-09-30); it is a usage mistake.
func TestNamespaceKeys_negativeDaysAreUsage(t *testing.T) {
	if err := NamespaceKeysCreate("acme", "cache", "", -1); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("create --expires-in-days -1: %v (code %d)", err, clierr.CodeOf(err))
	}
	if err := NamespaceKeysRotate("acme", 1, -1, 0); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("rotate --overlap-days -1: %v", err)
	}
	if err := NamespaceKeysRotate("acme", 1, 0, -2); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("rotate --expires-in-days -2: %v", err)
	}
}
