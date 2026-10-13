//go:build e2e_fleet

package tornetwork

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// TestAuthorityKeys_refusesBeforeItInstallsOrMakesAnything: `orama maint global tor
// authority-keys` (what `orama setup --create-network` runs on each authority's machine)
// checks its arguments before it installs the Tor package or makes a key: a missing flag, a
// private address, a name instead of an address and a bad nickname are usage errors, and
// the keys home is not made. The keys it makes are checked end to end by
// join-one-command's TestSetup_createANetworkWithItsOwnTorNetwork, on a fresh server,
// because the command installs the Tor Project's package, which this fleet's nodes must not
// have changed under the other tests.
func TestAuthorityKeys_refusesBeforeItInstallsOrMakesAnything(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no flags", nil, "--nickname and --address are required"},
		{"no address", []string{"--nickname", "OramaAuth01"}, "--nickname and --address are required"},
		{"a private address", []string{"--nickname", "OramaAuth01", "--address", "10.0.0.5"}, "not a public address"},
		{"a name instead of an address", []string{"--nickname", "OramaAuth01", "--address", "ns1.example.org"}, "not an IPv4 address"},
		{"a nickname with a space", []string{"--nickname", "Orama Auth", "--address", "57.129.166.16"}, "nickname"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := infra.OnNode(t, f, n, append([]string{"maint", "global", "tor", "authority-keys"}, c.args...)...)
			infra.ExpectNodeExit(t, "authority-keys "+c.name, out, infra.ExitUsage, c.want)
		})
	}
	if _, ok := infra.StatFile(t, f, n, constants.GlobalTorAuthorityKeysDir); ok {
		t.Errorf("%s exists on %s after only refused calls", constants.GlobalTorAuthorityKeysDir, n.Name)
	}
}
