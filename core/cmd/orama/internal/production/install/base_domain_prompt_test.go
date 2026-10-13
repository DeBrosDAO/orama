package install

import (
	"strings"
	"testing"
)

func TestPromptForBaseDomain_choices(t *testing.T) {
	for in, want := range map[string]string{
		"example.com\n":      "example.com",
		"https://ex.org/\n":  "ex.org",
		"stagenet.example\n": "stagenet.example",
	} {
		got, err := promptForBaseDomain(strings.NewReader(in))
		if err != nil || got != want {
			t.Errorf("input %q: got %q, %v; want %q", in, got, err, want)
		}
	}
}

// Enter, a single word, and a menu number used to install into devnet.
func TestPromptForBaseDomain_refusesWhatItCannotUse(t *testing.T) {
	for _, in := range []string{"\n", "1\n", "devnet\n", "https://\n", "-x.example\n"} {
		if got, err := promptForBaseDomain(strings.NewReader(in)); err == nil {
			t.Errorf("input %q: chose %q instead of refusing", in, got)
		}
	}
}
