package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Every spelling below reaches a private, loopback or otherwise non-public address through a
// resolver or an HTTP client, and used to pass as a hostname.
func TestValidateEndpoints_refusesObfuscatedAndNonPublicForms(t *testing.T) {
	refused := []string{
		// Integer, hex, octal and short IPv4 forms, in URL, host:port, bare and multiaddr shapes.
		"https://2130706433/", "https://2130706433:443", "2130706433:443", "2130706433",
		"https://0x7f.1/", "0x7f.1", "https://0x7f000001/", "https://127.1/", "127.1:80",
		"https://010.0.0.1/", "010.0.0.1", "https://01.2.3.4/", "01.2.3.4:443",
		"/dns4/2130706433/tcp/443", "/dns/127.1/tcp/443", "/ip4/2130706433/tcp/443", "/ip4/0x7f.1/tcp/1",
		"/ip4/example.com/tcp/443", "/ip6/1.2.3.4/tcp/443",
		// A zone id.
		"https://[fe80::1%25eth0]/", "[fe80::1%25eth0]:443", "fe80::1%eth0", "/ip6/fe80::1%25eth0/tcp/1",
		// A schemeless endpoint carrying a path, query or fragment.
		"10.0.0.1/x", "1.2.3.4/x", "node.example/x", "node.example:443/x", "node.example?x", "node.example#x",
		// Special ranges.
		"https://100.64.0.1/", "100.64.0.1:443", "https://198.18.0.1/", "https://192.0.0.9/",
		"https://240.0.0.1/", "https://255.255.255.255/", "https://198.51.100.1/", "https://203.0.113.1/",
		"https://192.0.2.1/", "https://[64:ff9b::a00:1]/", "https://[64:ff9b::7f00:1]/", "https://[fc00::1]/",
		"https://[fe80::1]/", "https://[ff02::1]/", "https://[2001:db8::1]/", "https://[::ffff:10.0.0.1]/",
		"/ip4/100.64.0.1/tcp/4001", "/ip6/fc00::1/tcp/4001", "/ip4/224.0.0.1/tcp/4001",
	}
	for _, ep := range refused {
		require.Errorf(t, ValidateEndpoints([]string{ep}, 1, 8), "%q must be refused", ep)
	}
	require.Error(t, ValidateMetadataURI("https://2130706433/meta.json"))
	require.Error(t, ValidateMetadataURI("https://[fe80::1%25eth0]/meta.json"))
	require.Error(t, ValidateBaseDomain("2130706433"))
}

func TestValidateEndpoints_keepsAcceptingPublicForms(t *testing.T) {
	accepted := []string{
		"https://node.example:443", "node.example:443", "node.example", "8.8.8.8:443", "https://8.8.8.8/",
		"https://[2606:4700:4700::1111]:443", "[2606:4700:4700::1111]:443", "2606:4700:4700::1111",
		"/dns4/node.example/tcp/443", "/ip4/8.8.8.8/tcp/4001", "/ip6/2606:4700:4700::1111/tcp/4001",
		"/ip4/8.8.8.8/tcp/4001/p2p/12D3KooWJ",
	}
	for _, ep := range accepted {
		require.NoErrorf(t, ValidateEndpoints([]string{ep}, 1, 8), "%q must be accepted", ep)
	}
}

func TestLiteralIPsAndNetworkOf_readOnlyStrictLiterals(t *testing.T) {
	require.Equal(t, []string{"8.8.8.8", "2606:4700:4700::1111"},
		LiteralIPs([]string{"https://8.8.8.8/", "/ip4/8.8.8.8/tcp/1", "[2606:4700:4700::1111]:443", "node.example:443"}))
	require.Equal(t, []string{"8.8.8.8"}, LiteralIPs([]string{"[::ffff:8.8.8.8]:443", "https://8.8.8.8/"}),
		"an IPv4-mapped spelling is the same address")
	require.Empty(t, LiteralIPs([]string{"https://2130706433/", "0x7f.1", "fe80::1%eth0", "8.8.8.8/x"}),
		"an obfuscated or malformed host is not read as an address")

	require.Equal(t, "8.8.0.0/16", NetworkOf([]string{"node.example", "https://8.8.8.8:443"}))
	require.Equal(t, "2606:4700::/32", NetworkOf([]string{"[2606:4700:4700::1111]:443"}))
	require.Empty(t, NetworkOf([]string{"https://2130706433/", "node.example", "0x7f.1"}))
	require.Empty(t, NetworkOf(nil))
}
