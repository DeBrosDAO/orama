package install

import (
	"strings"
	"testing"
)

func TestPromptForBaseDomain_choices(t *testing.T) {
	for in, want := range map[string]string{
		"\n":                   "orama-devnet.network",
		"1\n":                  "orama-devnet.network",
		"2\n":                  "orama-testnet.network",
		"3\n":                  "orama-mainnet.network",
		"4\nhttps://ex.org/\n": "ex.org",
	} {
		got, err := promptForBaseDomain(strings.NewReader(in))
		if err != nil || got != want {
			t.Errorf("input %q: got %q, %v; want %q", in, got, err, want)
		}
	}
}

// A typo or an empty custom domain used to install the node into devnet's
// zone without saying so.
func TestPromptForBaseDomain_refusesWhatItCannotUse(t *testing.T) {
	for _, in := range []string{"5\n", "devnet\n", "4\n\n"} {
		if got, err := promptForBaseDomain(strings.NewReader(in)); err == nil {
			t.Errorf("input %q: chose %q instead of refusing", in, got)
		}
	}
}
