package domain

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/cli/shared"
)

// The gateway lowercases the domain, strips a scheme and a trailing slash
// before storing it. Doing the same here means the name printed back is the
// name the gateway knows it by, and that `remove https://Example.com/` finds
// what `add example.com` created.
func TestNormalizeDomain(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"example.com", "example.com"},
		{"EXAMPLE.com", "example.com"},
		{"  example.com  ", "example.com"},
		{"https://example.com", "example.com"},
		{"http://example.com", "example.com"},
		{"https://Example.com/", "example.com"},
		{"app.example.co.uk", "app.example.co.uk"},
	} {
		if got := normalizeDomain(tc.in); got != tc.want {
			t.Errorf("normalizeDomain(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Only "the record is not there yet" is worth waiting on. Retrying a 404 or a
// 401 turns a clear error into a long silence ending in the same error.
func TestRetryVerify(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		timeLeft bool
		want     bool
	}{
		{"400 with time left", &shared.StatusError{Status: 400}, true, true},
		{"400 out of time", &shared.StatusError{Status: 400}, false, false},
		{"404 never retried", &shared.StatusError{Status: 404}, true, false},
		{"401 never retried", &shared.StatusError{Status: 401}, true, false},
		{"500 never retried", &shared.StatusError{Status: 500}, true, false},
		{"transport error never retried", fmt.Errorf("connection refused"), true, false},
	} {
		if got := retryVerify(tc.err, tc.timeLeft); got != tc.want {
			t.Errorf("%s: retryVerify = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Each subcommand owns its flags. Package-level flag variables shared between
// them would leave `add --wait` with whichever default cobra registered last.
func TestSubcommandsDoNotShareFlagStorage(t *testing.T) {
	var add, verify string
	for _, c := range Cmd.Commands() {
		switch c.Name() {
		case "add":
			add = c.Flags().Lookup("wait").DefValue
		case "verify":
			verify = c.Flags().Lookup("wait").DefValue
		}
	}
	if add == "" || verify == "" {
		t.Fatal("both add and verify must define --wait")
	}
	if add == verify {
		t.Errorf("--wait defaults are both %q; add waits by default, verify does not", add)
	}
}

func TestEverySubcommandTakesJSON(t *testing.T) {
	names := map[string]bool{}
	for _, c := range Cmd.Commands() {
		names[c.Name()] = true
		if c.Flags().Lookup("json") == nil {
			t.Errorf("orama domain %s has no --json", c.Name())
		}
	}
	for _, want := range []string{"add", "verify", "list", "remove"} {
		if !names[want] {
			t.Errorf("orama domain %s is not registered", want)
		}
	}
}

// list works without --app; add cannot guess which deployment a domain belongs
// to, so it must say so rather than fail at the gateway.
func TestAddRequiresAppAndListDoesNot(t *testing.T) {
	for _, c := range Cmd.Commands() {
		flag := c.Flags().Lookup("app")
		switch c.Name() {
		case "add":
			if flag == nil {
				t.Fatal("orama domain add has no --app")
			}
			if flag.Annotations["cobra_annotation_bash_completion_one_required_flag"] == nil {
				t.Error("--app must be required on add")
			}
		case "list":
			if flag == nil {
				t.Fatal("orama domain list has no --app")
			}
			if flag.Annotations["cobra_annotation_bash_completion_one_required_flag"] != nil {
				t.Error("--app must be optional on list; without it the whole namespace is listed")
			}
		}
	}
}
