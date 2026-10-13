package releasepub

import (
	"strings"
	"testing"
	"time"
)

func TestParseChannel(t *testing.T) {
	cases := map[string]string{
		"nightly": "nightly", "main": "main", " main ": "main",
		"dev/my-branch": "dev/my-branch", "dev/Feat/Join_One": "dev/feat-join-one", "dev/--x--": "dev/x", "dev/a.b": "dev/a-b",
	}
	for in, want := range cases {
		got, err := ParseChannel(in)
		if err != nil || got.Name != want {
			t.Errorf("ParseChannel(%q) = %q, %v; want %q", in, got.Name, err, want)
		}
	}
	for _, bad := range []string{"", "stable", "dev", "dev/", "dev/---", "dev/" + strings.Repeat("a", 33), "Nightly", "nightly/x"} {
		if got, err := ParseChannel(bad); err == nil {
			t.Errorf("ParseChannel(%q) = %q, want a refusal", bad, got.Name)
		}
	}
}

func TestChannel_timestampValidityIsLongerForMain(t *testing.T) {
	for name, want := range map[string]time.Duration{"nightly": 7 * 24 * time.Hour, "dev/x": 7 * 24 * time.Hour, "main": 30 * 24 * time.Hour} {
		if got := (Channel{Name: name}).TimestampValidity(); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}

func TestChannel_tagNeverCollidesAcrossSeparators(t *testing.T) {
	a := Channel{Name: "dev/x"}.Tag("0.3.1")
	b := Channel{Name: "dev-x"}.Tag("0.3.1")
	if a == b || a != "release-dev.x-0.3.1" || b != "release-dev-x-0.3.1" {
		t.Fatalf("tags %q and %q", a, b)
	}
}
