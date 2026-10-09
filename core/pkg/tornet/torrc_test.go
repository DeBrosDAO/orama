package tornet

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func relayConfig() RelayConfig {
	return RelayConfig{
		Network:  testNetwork(),
		Home:     "/var/lib/orama-global/tor-relay",
		Nickname: NicknameFor("node-1"),
		Contact:  "ops@example.org 0xabc",
		Address:  "57.129.166.18",
		ORPort:   31020,
	}
}

func lines(s string) []string { return strings.Split(strings.TrimSpace(s), "\n") }

func has(t *testing.T, torrc string, want string) {
	t.Helper()
	for _, l := range lines(torrc) {
		if l == want {
			return
		}
	}
	t.Errorf("torrc has no line %q:\n%s", want, torrc)
}

func hasPrefix(torrc, prefix string) bool {
	for _, l := range lines(torrc) {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func TestRelayTorrc_nonExitAlwaysRejectsEverything(t *testing.T) {
	got, err := RelayTorrc(relayConfig())
	if err != nil {
		t.Fatal(err)
	}
	has(t, got, "ExitRelay 0")
	has(t, got, "ExitPolicy reject *:*")
	for _, l := range lines(got) {
		if strings.HasPrefix(l, "ExitPolicy accept") || l == "ExitRelay 1" {
			t.Errorf("a non-exit relay has %q", l)
		}
	}
	has(t, got, "ORPort 31020 IPv4Only")
	has(t, got, "DirPort 0")
	has(t, got, "Address 57.129.166.18")
	has(t, got, "UseDefaultFallbackDirs 0")
	if hasPrefix(got, "AuthoritativeDirectory") || hasPrefix(got, "AssumeReachable") {
		t.Errorf("a relay of a settled network is an authority or assumes reachability:\n%s", got)
	}
	for _, l := range testNetwork().DirAuthorityLines() {
		has(t, got, l)
	}
}

func TestRelayTorrc_exitPolicy(t *testing.T) {
	c := relayConfig()
	c.Network.AllowExit = true
	c.Exit = true
	c.ExitReject = []string{"203.0.113.9:*"}
	got, err := RelayTorrc(c)
	if err != nil {
		t.Fatal(err)
	}
	has(t, got, "ExitRelay 1")
	has(t, got, "IPv6Exit 0")
	has(t, got, "ExitPolicy reject 203.0.113.9:*")
	has(t, got, "ExitPolicy reject 198.18.0.0/15:*")
	has(t, got, "ExitPolicy reject 100.64.0.0/10:*")
	has(t, got, "ExitPolicy reject 10.0.0.0/8:*")
	has(t, got, "ExitPolicy reject *:25")
	has(t, got, "ExitPolicy accept *:*")
	if hasPrefix(got, "ExitPolicy reject *:*") {
		t.Error("an exit rejects everything")
	}
	var policy []string
	for _, l := range lines(got) {
		if strings.HasPrefix(l, "ExitPolicy ") {
			policy = append(policy, l)
		}
	}
	if policy[0] != "ExitPolicy reject 203.0.113.9:*" {
		t.Errorf("the operator's refusal is not first (Tor takes the first match): %v", policy[0])
	}
	if policy[len(policy)-1] != "ExitPolicy accept *:*" {
		t.Errorf("the policy does not end by accepting the rest: %v", policy[len(policy)-1])
	}
}

func TestRelayTorrc_exitNeedsTheNetworkToAllowIt(t *testing.T) {
	c := relayConfig()
	c.Exit = true
	if _, err := RelayTorrc(c); err == nil || !strings.Contains(err.Error(), "allow_exit") {
		t.Fatalf("an exit on a network that does not allow exits: %v", err)
	}
}

func TestRelayTorrc_rejectListNeedsExit(t *testing.T) {
	c := relayConfig()
	c.ExitReject = []string{"1.2.3.4"}
	if _, err := RelayTorrc(c); err == nil {
		t.Fatal("a reject list on a non-exit was accepted")
	}
}

func TestRelayTorrc_authority(t *testing.T) {
	c := relayConfig()
	c.Authority = true
	c.DirPort = 31021
	c.BandwidthFile = "/var/lib/orama-global/sbws/latest.v1"
	got, err := RelayTorrc(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"AuthoritativeDirectory 1", "V3AuthoritativeDirectory 1",
		"V3AuthVotingInterval 30 minutes", "V3AuthVoteDelay 300 seconds", "V3AuthDistDelay 300 seconds",
		"AuthDirMaxServersPerAddr 1", "DirPort 31021", "ExitPolicy reject *:*",
		"V3BandwidthsFile /var/lib/orama-global/sbws/latest.v1",
	} {
		has(t, got, want)
	}
	c.Exit, c.Network.AllowExit = true, true
	if _, err := RelayTorrc(c); err == nil {
		t.Fatal("a directory authority that exits was accepted")
	}
}

func TestRelayTorrc_bootstrapAssumesReachable(t *testing.T) {
	c := relayConfig()
	c.Network.Bootstrap = true
	got, err := RelayTorrc(c)
	if err != nil {
		t.Fatal(err)
	}
	has(t, got, "AssumeReachable 1")
}

func TestRelayTorrc_bandwidthAndFamily(t *testing.T) {
	c := relayConfig()
	c.BandwidthMbit = 100
	c.Family = []string{fmt.Sprintf("%040X", 7), fmt.Sprintf("%040X", 8)}
	got, err := RelayTorrc(c)
	if err != nil {
		t.Fatal(err)
	}
	has(t, got, "RelayBandwidthRate 100 Mbits")
	has(t, got, "RelayBandwidthBurst 100 Mbits")
	has(t, got, "MyFamily $"+fmt.Sprintf("%040X", 7)+",$"+fmt.Sprintf("%040X", 8))
	c.Family = []string{"not a fingerprint"}
	if _, err := RelayTorrc(c); err == nil {
		t.Fatal("a bad family member was accepted")
	}
}

// Every value written into a torrc line is checked: one that carries a newline
// would add a directive.
func TestRelayTorrc_refusesInjection(t *testing.T) {
	cases := map[string]func(*RelayConfig){
		"contact newline":   func(c *RelayConfig) { c.Contact = "me\nExitRelay 1" },
		"contact comment":   func(c *RelayConfig) { c.Contact = "me # x" },
		"contact empty":     func(c *RelayConfig) { c.Contact = "" },
		"contact long":      func(c *RelayConfig) { c.Contact = strings.Repeat("a", 201) },
		"contact non-ascii": func(c *RelayConfig) { c.Contact = "mé" },
		"nickname space":    func(c *RelayConfig) { c.Nickname = "a b" },
		"nickname long":     func(c *RelayConfig) { c.Nickname = strings.Repeat("a", 20) },
		"home space":        func(c *RelayConfig) { c.Home = "/var/lib/x y" },
		"home relative":     func(c *RelayConfig) { c.Home = "var/lib" },
		"home dotdot":       func(c *RelayConfig) { c.Home = "/var/../etc" },
		"address private":   func(c *RelayConfig) { c.Address = "192.168.1.5" },
		"address hostname":  func(c *RelayConfig) { c.Address = "relay.example.org" },
		"orport zero":       func(c *RelayConfig) { c.ORPort = 0 },
		"bandwidth path":    func(c *RelayConfig) { c.BandwidthFile = "/a\nExitRelay 1" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := relayConfig()
			c.Authority = name == "bandwidth path"
			c.DirPort = 31021
			mutate(&c)
			if _, err := RelayTorrc(c); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestNicknameFor(t *testing.T) {
	a, b := NicknameFor("node-1"), NicknameFor("node-2")
	if a == b || NicknameFor("node-1") != a {
		t.Fatal("nickname is not a stable function of the node id")
	}
	if len(a) != maxNicknameLen || !nicknamePattern.MatchString(a) {
		t.Fatalf("nickname %q is not 19 letters and digits", a)
	}
	if strings.Contains(a, "node") {
		t.Fatalf("nickname %q names the node id", a)
	}
}

func TestOnionTorrc(t *testing.T) {
	got, err := OnionTorrc(OnionConfig{Network: testNetwork(), Home: "/var/lib/orama-global/tor-onion", Target: "127.0.0.1:31022"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"SocksPort 0", "ORPort 0", "DirPort 0", "ClientOnly 1",
		"HiddenServiceDir /var/lib/orama-global/tor-onion/onion", "HiddenServiceVersion 3",
		"HiddenServicePort 80 127.0.0.1:31022", "HiddenServiceEnableIntroDoSDefense 1",
		"HiddenServiceMaxStreamsCloseCircuit 1",
	} {
		has(t, got, want)
	}
	if hasPrefix(got, "Nickname") || hasPrefix(got, "ExitPolicy accept") {
		t.Errorf("an onion host is a relay:\n%s", got)
	}
	for _, bad := range []string{"", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:99999", "a b:80", "127.0.0.1:80\nExitRelay 1"} {
		if _, err := OnionTorrc(OnionConfig{Network: testNetwork(), Home: "/h", Target: bad}); err == nil {
			t.Errorf("target %q accepted", bad)
		}
	}
}

func TestClientTorrc(t *testing.T) {
	got, err := ClientTorrc(ClientConfig{Network: testNetwork(), Home: "/var/lib/orama-tornet", SOCKSAddr: "127.0.0.1:9051", DNSAddr: "127.0.0.1:9153"})
	if err != nil {
		t.Fatal(err)
	}
	has(t, got, "SocksPort 127.0.0.1:9051 IsolateSOCKSAuth")
	has(t, got, "DNSPort 127.0.0.1:9153")
	has(t, got, "ControlPort 0")
	has(t, got, "ClientOnly 1")
	has(t, got, "ExitRelay 0")
	has(t, got, "UseBridges 0")
	has(t, got, "UseDefaultFallbackDirs 0")
	has(t, got, "ClientRejectInternalAddresses 1")
	for _, l := range testNetwork().DirAuthorityLines() {
		has(t, got, l)
	}
	if n := strings.Count(got, "DirAuthority "); n != 3 {
		t.Errorf("want exactly the network's three authorities, got %d:\n%s", n, got)
	}
	plain, err := ClientTorrc(ClientConfig{Network: testNetwork(), Home: "/h", SOCKSAddr: "127.0.0.1:9051"})
	if err != nil || strings.Contains(plain, "DNSPort") {
		t.Errorf("no DNS address means no DNSPort: %v\n%s", err, plain)
	}
}

func TestClientTorrc_refusesWhatCouldInjectOrExpose(t *testing.T) {
	bad := []ClientConfig{
		{Home: "relative/dir", SOCKSAddr: "127.0.0.1:9150"},
		{Home: "/tmp/a b", SOCKSAddr: "127.0.0.1:9150"},
		{Home: "/tmp/a\nSocksPort 0.0.0.0:1", SOCKSAddr: "127.0.0.1:9150"},
		{Home: "/tmp/a\x01b", SOCKSAddr: "127.0.0.1:9150"},
		{Home: "/h", SOCKSAddr: "0.0.0.0:9150"},
		{Home: "/h", SOCKSAddr: "example.com:9150"},
		{Home: "/h", SOCKSAddr: "127.0.0.1:0"},
		{Home: "/h", SOCKSAddr: "127.0.0.1:09150"},
		{Home: "/h", SOCKSAddr: ""},
		{Home: "/h", SOCKSAddr: "127.0.0.1:9150", DNSAddr: "10.0.0.1:53"},
		// netip accepts any text as an IPv6 zone and calls ::1%zone loopback; the
		// zone would add lines to the torrc.
		{Home: "/h", SOCKSAddr: "[::1%a\nClientTransportPlugin x exec /bin/sh]:9150"},
		{Home: "/h", SOCKSAddr: "127.0.0.1:9150", DNSAddr: "[::1%a\nControlPort 9051]:53"},
	}
	for _, c := range bad {
		c.Network = testNetwork()
		if _, err := ClientTorrc(c); err == nil {
			t.Errorf("%+v was accepted", c)
		}
	}
	if _, err := ClientTorrc(ClientConfig{Network: Network{}, Home: "/h", SOCKSAddr: "127.0.0.1:9051"}); err == nil {
		t.Fatal("an empty network was accepted")
	}
	public := testNetwork()
	public.Private = false
	if _, err := ClientTorrc(ClientConfig{Network: public, Home: "/h", SOCKSAddr: "127.0.0.1:9051"}); !errors.Is(err, ErrPublicNetwork) {
		t.Errorf("a torrc for a public network: %v", err)
	}
}

func TestExitPolicyLines_everyIPv4ReservedRangeBeforeTheAccept(t *testing.T) {
	policy := ExitPolicyLines(nil)
	index := map[string]int{}
	for i, l := range policy {
		index[l] = i
	}
	accept := index["ExitPolicy accept *:*"]
	for _, cidr := range []string{"10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "198.18.0.0/15", "0.0.0.0/8"} {
		i, ok := index["ExitPolicy reject "+cidr+":*"]
		if !ok || i > accept {
			t.Errorf("%s is not refused before the accept", cidr)
		}
	}
	for _, port := range []string{"25", "465", "587", "6881-6999"} {
		if _, ok := index["ExitPolicy reject *:"+port]; !ok {
			t.Errorf("port %s is not refused", port)
		}
	}
	for _, l := range policy {
		if strings.Contains(l, ":") && strings.Count(l, ":") > 1 && !strings.HasSuffix(l, ":*") {
			t.Errorf("unexpected IPv6-looking rule %q", l)
		}
	}
}

func TestParseExitRejectList(t *testing.T) {
	got, err := ParseExitRejectList("# abuse desk\n\n203.0.113.9\n198.51.100.0/24:443  # a complaint\n192.0.2.7:6000-6100\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"203.0.113.9:*", "198.51.100.0/24:443", "192.0.2.7:6000-6100"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if out, err := ParseExitRejectList(""); err != nil || len(out) != 0 {
		t.Fatalf("empty list = %v %v", out, err)
	}
}

func TestParseExitRejectList_refusesAnythingButADestination(t *testing.T) {
	for _, bad := range []string{
		"ExitRelay 1", "example.org", "1.2.3.4:0", "1.2.3.4:70000", "1.2.3.4:80-70", "1.2.3.4:x",
		"2001:db8::1", "1.2.3.4/33", "1.2.3.4 SocksPort 1", "1.2.3.4:080",
	} {
		if _, err := ParseExitRejectList(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTorrc_smallNetworkOptions(t *testing.T) {
	c := relayConfig()
	if got, _ := RelayTorrc(c); hasPrefix(got, "EnforceDistinctSubnets") {
		t.Error("a network that did not ask for shared subnets disables the subnet rule")
	}
	c.Network.AllowSharedSubnets = true
	got, err := RelayTorrc(c)
	if err != nil {
		t.Fatal(err)
	}
	has(t, got, "EnforceDistinctSubnets 0")
	client, _ := ClientTorrc(ClientConfig{Network: c.Network, Home: "/h", SOCKSAddr: "127.0.0.1:9052"})
	has(t, client, "EnforceDistinctSubnets 0")
	onion, _ := OnionTorrc(OnionConfig{Network: c.Network, Home: "/h", Target: "127.0.0.1:1"})
	has(t, onion, "EnforceDistinctSubnets 0")

	auth := relayConfig()
	auth.Authority, auth.DirPort = true, 31021
	if got, _ := RelayTorrc(auth); hasPrefix(got, "MinUptimeHidServDirectoryV2") {
		t.Error("an authority overrides Tor's HSDir uptime without being asked")
	}
	auth.Network.HSDirMinUptimeHours = 2
	got, err = RelayTorrc(auth)
	if err != nil {
		t.Fatal(err)
	}
	has(t, got, "MinUptimeHidServDirectoryV2 2 hours")
	// Only authorities decide the HSDir flag.
	plain := relayConfig()
	plain.Network.HSDirMinUptimeHours = 2
	if got, _ := RelayTorrc(plain); hasPrefix(got, "MinUptimeHidServDirectoryV2") {
		t.Error("a relay that is not an authority carries the authority option")
	}
}
