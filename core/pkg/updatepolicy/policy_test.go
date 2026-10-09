package updatepolicy

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

func TestValidate_acceptsTheDocumentedValuesAndRefusesTheRest(t *testing.T) {
	good := map[string][]string{
		KeyMode:    {"off", "notify", "auto"},
		KeyChannel: {"stable", "nightly", "beta-2"},
		KeyWindow:  {"", "1-5", "22-4", "0-23", "5-5"},
		KeyRepo:    {"", "https://releases.example.org/tuf", "https://93.184.216.34:8443/tuf"},
	}
	bad := map[string][]string{
		KeyMode:    {"", "yes", "AUTO", "auto "},
		KeyChannel: {"", "Stable", "a/b", "../x", "this-channel-name-is-far-longer-than-allowed"},
		KeyWindow:  {"night", "1-24", "-1-3", "1", "1-", "-5", "a-b", "1-5-7"},
		KeyRepo:    {"releases.example.org", "http://releases.example.org", "ftp://x", "https://u:p@x", "http://127.0.0.1:8080", "https://localhost/tuf", "https://10.0.0.7/tuf", "https://169.254.169.254/"},
	}
	for key, values := range good {
		for _, v := range values {
			if err := Validate(key, v); err != nil {
				t.Errorf("%s=%q: %v", key, v, err)
			}
		}
	}
	for key, values := range bad {
		for _, v := range values {
			if err := Validate(key, v); err == nil {
				t.Errorf("%s=%q was accepted", key, v)
			}
		}
	}
	if err := Validate("max_parallel", "2"); err == nil {
		t.Error("an unknown key was accepted")
	}
}

func TestParseWindow(t *testing.T) {
	w, err := ParseWindow("22-4")
	if err != nil || w != (Window{Start: 22, End: 4}) {
		t.Fatalf("22-4 -> %+v, %v", w, err)
	}
	if w, err := ParseWindow(""); err != nil || w != (Window{}) {
		t.Fatalf("empty -> %+v, %v", w, err)
	}
}

// The top-level roles of the release repository are not channels: the verifier
// refuses them as delegated role names, so a policy naming one would pass here
// and fail at every node on every tick.
func TestValidChannel_refusesTheTopLevelRolesAndAgreesWithTheVerifier(t *testing.T) {
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		if err := Validate(KeyChannel, role); err == nil {
			t.Errorf("update_channel %q was accepted", role)
		}
	}
	for _, name := range []string{"", "Stable", "a/b", "../x", "a.b", "stable ", strings.Repeat("a", 33), "root", "stable", "nightly", "beta-2", "0"} {
		policy := ValidChannel(name)
		verifier := releaseverify.ValidRoleName(name)
		if (policy == nil) != (verifier == nil) {
			t.Errorf("channel %q: policy says %v, the verifier says %v", name, policy, verifier)
		}
	}
}
