package globalnetns

import (
	"strings"
	"testing"
)

func testLayout() Layout {
	return Layout{
		Ports: []Port{{"tcp", 31000}, {"udp", 31000}, {"tcp", 31013}},
		Tools: Tools{IP: "/usr/sbin/ip", Nft: "/usr/sbin/nft", Sysctl: "/usr/sbin/sysctl"},
	}
}

func TestRenderUnit_buildsAndTearsDownTheNamespace(t *testing.T) {
	unit := testLayout().RenderUnit()
	for _, want := range []string{
		"Type=oneshot", "RemainAfterExit=yes",
		"ExecStart=/usr/sbin/ip netns add orama-global",
		"ExecStart=/usr/sbin/ip link add ogl-host type veth peer name ogl-ns",
		"ExecStart=/usr/sbin/ip link set ogl-ns netns orama-global",
		"ExecStart=/usr/sbin/ip addr add 198.18.0.1/30 dev ogl-host",
		"ExecStart=/usr/sbin/ip -n orama-global addr add 198.18.0.2/30 dev ogl-ns",
		"ExecStart=/usr/sbin/ip -n orama-global route add default via 198.18.0.1",
		"ExecStart=/usr/sbin/nft -f /etc/orama-global/netns-host.nft",
		"ExecStart=/usr/sbin/ip netns exec orama-global /usr/sbin/nft -f /etc/orama-global/netns.nft",
		"ExecStop=-/usr/sbin/ip netns del orama-global",
		"ExecStartPre=-/usr/sbin/ip netns del orama-global",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "ProtectSystem") || strings.Contains(unit, "PrivateMounts") {
		t.Errorf("the namespace unit must not sandbox its mounts: ip netns add binds into the host mount namespace")
	}
}

func TestRenderHostRules_publishesOnlyTheListedPorts(t *testing.T) {
	rules := testLayout().RenderHostRules()
	for _, want := range []string{
		`tcp dport { 31000, 31013 } dnat to 198.18.0.2`,
		`udp dport { 31000 } dnat to 198.18.0.2`,
		`masquerade`,
		`iifname "ogl-host" ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 100.64.0.0/10 } drop`,
		`oifname "ogl-host" ct status dnat accept`,
		`oifname "ogl-host" drop`,
		`iifname "ogl-host" drop`,
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("host rules lack %q:\n%s", want, rules)
		}
	}
}

func TestRenderRules_noPortsMeansNoDNATAndNoAccept(t *testing.T) {
	l := testLayout()
	l.Ports = nil
	if strings.Contains(l.RenderHostRules(), "dnat to") {
		t.Errorf("host rules DNAT with no published port:\n%s", l.RenderHostRules())
	}
	if strings.Contains(l.RenderNSRules(), "dport") {
		t.Errorf("namespace rules accept a port with none published:\n%s", l.RenderNSRules())
	}
}

func TestRenderNSRules_dropsByDefault(t *testing.T) {
	rules := testLayout().RenderNSRules()
	for _, want := range []string{
		"type filter hook input priority 0; policy drop;",
		"type filter hook forward priority 0; policy drop;",
		`iifname "lo" accept`,
		"tcp dport { 31000, 31013 } accept",
		"udp dport { 31000 } accept",
		"ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 100.64.0.0/10 } drop",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("namespace rules lack %q:\n%s", want, rules)
		}
	}
}

func TestRenderRules_duplicatePortsAppearOnce(t *testing.T) {
	l := testLayout()
	l.Ports = []Port{{"tcp", 5}, {"tcp", 5}}
	if got := l.portSet("tcp"); got != "{ 5 }" {
		t.Errorf("portSet = %q", got)
	}
}

func TestLayoutValidate(t *testing.T) {
	bad := []Layout{
		{Tools: Tools{IP: "ip", Nft: "/n", Sysctl: "/s"}},
		{Tools: Tools{IP: "/a b", Nft: "/n", Sysctl: "/s"}},
		{Tools: Tools{IP: "/i", Nft: "/n;rm", Sysctl: "/s"}},
		{Tools: Tools{IP: "/i", Nft: "/n", Sysctl: "/s"}, Ports: []Port{{"sctp", 1}}},
		{Tools: Tools{IP: "/i", Nft: "/n", Sysctl: "/s"}, Ports: []Port{{"tcp", 0}}},
		{Tools: Tools{IP: "/i", Nft: "/n", Sysctl: "/s"}, Ports: []Port{{"tcp", 70000}}},
	}
	for i, l := range bad {
		if l.Validate() == nil {
			t.Errorf("layout %d validated: %+v", i, l)
		}
	}
	if err := testLayout().Validate(); err != nil {
		t.Errorf("good layout refused: %v", err)
	}
}

func TestApplyToUnit_joinsTheNamespace(t *testing.T) {
	unit := "[Unit]\nDescription=x\nAfter=network-online.target\nStartLimitIntervalSec=0\n\n[Service]\nType=simple\nUser=a\n"
	got, err := ApplyToUnit(unit)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"BindsTo=orama-global-netns.service\n", "After=orama-global-netns.service\n",
		"NetworkNamespacePath=/run/netns/orama-global\n",
		"BindReadOnlyPaths=/etc/orama-global/resolv.conf:/etc/resolv.conf\n",
	} {
		if strings.Count(got, want) != 1 {
			t.Errorf("unit has %q %d times:\n%s", want, strings.Count(got, want), got)
		}
	}
	if !strings.Contains(got, "User=a") {
		t.Errorf("the rest of the unit was lost:\n%s", got)
	}
}

func TestApplyToUnit_refusesWhatItCannotAnchor(t *testing.T) {
	if _, err := ApplyToUnit("[Unit]\nDescription=x\n[Service]\nExecStart=/bin/true\n"); err == nil {
		t.Error("a unit with no anchors was patched")
	}
	once, err := ApplyToUnit("[Unit]\nStartLimitIntervalSec=0\n[Service]\nType=simple\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyToUnit(once); err == nil {
		t.Error("a unit already in the namespace was patched again")
	}
}

func TestRenderResolvConfAndSysctl(t *testing.T) {
	if got := RenderResolvConf(); got != "nameserver 9.9.9.9\nnameserver 1.1.1.1\n" {
		t.Errorf("resolv.conf = %q", got)
	}
	if !strings.Contains(RenderSysctl(), "net.ipv4.ip_forward = 1") {
		t.Error("sysctl does not enable forwarding")
	}
}

func TestApplyToUnit_joinsTheNamespaceFromAOneshot(t *testing.T) {
	got, err := ApplyToUnit("[Unit]\nStartLimitIntervalSec=0\n\n[Service]\nType=oneshot\nExecStart=/bin/true\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"BindsTo=" + UnitName + "\n", "NetworkNamespacePath=" + Path + "\n", "Type=oneshot\nNetworkNamespacePath="} {
		if !strings.Contains(got, want) {
			t.Errorf("unit lacks %q:\n%s", want, got)
		}
	}
}

// The rulesets are IPv4 only, so IPv6 is switched off instead of being left to route around them:
// on the host end of the veth and everywhere in the namespace, before the ends are brought up.
func TestRenderUnit_switchesIPv6OffBeforeAnEndComesUp(t *testing.T) {
	unit := testLayout().RenderUnit()
	hostOff := "ExecStart=-/usr/sbin/sysctl -q -w net.ipv6.conf.ogl-host.disable_ipv6=1"
	nsOff := "ExecStart=-/usr/sbin/ip netns exec orama-global /usr/sbin/sysctl -q -w net.ipv6.conf.all.disable_ipv6=1 net.ipv6.conf.default.disable_ipv6=1"
	for _, want := range []string{hostOff, nsOff} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit lacks %q:\n%s", want, unit)
		}
	}
	hostUp := strings.Index(unit, "ExecStart=/usr/sbin/ip link set ogl-host up")
	nsUp := strings.Index(unit, "ExecStart=/usr/sbin/ip -n orama-global link set ogl-ns up")
	if hostUp < 0 || nsUp < 0 {
		t.Fatalf("unit does not bring the veth ends up:\n%s", unit)
	}
	for _, off := range []string{hostOff, nsOff} {
		if at := strings.Index(unit, off); at > hostUp || at > nsUp {
			t.Errorf("%q comes after an end is up, so a link-local address is already assigned", off)
		}
	}
}

// The comment on PrivateRanges must not claim what the rulesets do not do: they are IPv4 only, and
// no IPv6 private range appears in either.
func TestRenderRules_areIPv4Only(t *testing.T) {
	l := testLayout()
	for name, rules := range map[string]string{"host": l.RenderHostRules(), "namespace": l.RenderNSRules()} {
		if strings.Contains(rules, "ip6") || strings.Contains(rules, "table inet") {
			t.Errorf("%s rules mention IPv6:\n%s", name, rules)
		}
		if !strings.HasPrefix(rules, "table ip ") {
			t.Errorf("%s rules are not `table ip`:\n%s", name, rules)
		}
	}
}
