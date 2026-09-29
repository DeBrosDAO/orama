package globalnetns

import (
	"os"
	"os/exec"
	"path/filepath"
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
		"ExecStart=/usr/sbin/ip link add ogl-host type veth peer name ogl-ns netns orama-global",
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
	// The namespace's default is set before its end of the veth exists, so that end is created with
	// IPv6 already off instead of being switched off after the fact.
	create := strings.Index(unit, "ExecStart=/usr/sbin/ip link add ogl-host type veth peer name ogl-ns netns orama-global")
	if create < 0 || strings.Index(unit, nsOff) > create {
		t.Errorf("the namespace's IPv6 default is not set before the veth pair is created:\n%s", unit)
	}
	if strings.Index(unit, hostOff) < create {
		t.Errorf("the host end is switched off before it exists")
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

func hostOnlyLayout() Layout {
	l := testLayout()
	l.HostPorts = []int{31015, 31001, 31003, 31001}
	l.HostClientUIDs = []int{998}
	return l
}

func TestRenderNSRules_hostOnlyPortsComeFromTheHostVethAddressAlone(t *testing.T) {
	rules := hostOnlyLayout().RenderNSRules()
	want := `iifname "ogl-ns" ip saddr 198.18.0.1 tcp dport { 31001, 31003, 31015 } accept`
	if !strings.Contains(rules, want) {
		t.Fatalf("namespace rules lack %q:\n%s", want, rules)
	}
	// The accept sits in the input chain, whose policy is drop: every other source is refused.
	if !strings.Contains(rules, "type filter hook input priority 0; policy drop;") {
		t.Errorf("the input chain does not drop by default:\n%s", rules)
	}
	// Not a published port: nothing else names them.
	if strings.Count(rules, "31001") != 1 {
		t.Errorf("a host-only port is accepted by more than one rule:\n%s", rules)
	}
}

func TestRenderHostRules_doNotPublishHostOnlyPorts(t *testing.T) {
	rules := hostOnlyLayout().RenderHostRules()
	prerouting := rules[strings.Index(rules, "chain prerouting"):strings.Index(rules, "chain postrouting")]
	for _, port := range []string{"31001", "31003", "31015"} {
		if strings.Contains(prerouting, port) {
			t.Errorf("host rules DNAT host-only port %s (it must not be published):\n%s", port, rules)
		}
	}
}

// Every host process shares the veth's route to the namespace address, so only root and the account
// the cluster node runs as may open a connection to the host-only ports: a tenant process (a
// systemd dynamic user) is dropped by the host ruleset's output chain.
func TestRenderHostRules_onlyRootAndTheClusterAccountReachTheHostOnlyPorts(t *testing.T) {
	rules := hostOnlyLayout().RenderHostRules()
	want := "ip daddr 198.18.0.2 tcp dport { 31001, 31003, 31015 } meta skuid != { 0, 998 } drop"
	if !strings.Contains(rules, want) {
		t.Fatalf("host rules lack %q:\n%s", want, rules)
	}
	if !strings.Contains(rules, "chain output {\n\t\ttype filter hook output priority -1; policy accept;") {
		t.Errorf("the rule is not in an output filter chain:\n%s", rules)
	}
}

func TestRenderHostRules_noHostOnlyPortsMeansNoOutputChain(t *testing.T) {
	if rules := testLayout().RenderHostRules(); strings.Contains(rules, "chain output") {
		t.Errorf("a layout with no host-only port renders an output chain:\n%s", rules)
	}
}

func TestValidate_hostOnlyPortsNeedAClientAccount(t *testing.T) {
	l := testLayout()
	l.HostPorts = []int{31001}
	if err := l.Validate(); err == nil {
		t.Error("host-only ports with no account allowed to reach them were accepted")
	}
	l.HostClientUIDs = []int{0}
	if err := l.Validate(); err == nil {
		t.Error("root named as an extra client account was accepted")
	}
	l.HostClientUIDs = []int{998}
	if err := l.Validate(); err != nil {
		t.Errorf("a good layout was refused: %v", err)
	}
}

func TestRenderNSRules_noHostOnlyPortsMeansNoHostRule(t *testing.T) {
	if strings.Contains(testLayout().RenderNSRules(), "saddr") {
		t.Errorf("a layout with no host-only port renders a source rule:\n%s", testLayout().RenderNSRules())
	}
}

func TestValidate_refusesAHostOnlyPortOutOfRange(t *testing.T) {
	for _, n := range []int{0, -1, 65536} {
		l := testLayout()
		l.HostPorts = []int{n}
		l.HostClientUIDs = []int{998}
		if err := l.Validate(); err == nil {
			t.Errorf("host-only port %d was accepted", n)
		}
	}
}

func TestInstalled_isTheNamespaceUnitOnDisk(t *testing.T) {
	seen := ""
	got := Installed("/etc/systemd/system", func(p string) bool { seen = p; return true })
	if !got || seen != "/etc/systemd/system/orama-global-netns.service" {
		t.Errorf("Installed = %v, probed %q", got, seen)
	}
	if Installed("/etc/systemd/system", func(string) bool { return false }) {
		t.Errorf("a machine without the unit reports co-located")
	}
}

func TestChainHost(t *testing.T) {
	if ChainHost(true) != "198.18.0.2" || ChainHost(false) != "127.0.0.1" {
		t.Errorf("ChainHost = %q / %q", ChainHost(true), ChainHost(false))
	}
}

// The sysctls are written with ExecStart=-, so a write that failed is ignored. ExecStartPost reads them
// back and fails the unit unless IPv6 is off on the host end and everywhere in the namespace; a kernel
// with no IPv6 at all has nothing to check.
func TestRenderUnit_failsWhenIPv6CouldNotBeSwitchedOff(t *testing.T) {
	unit := testLayout().RenderUnit()
	var hostCheck, nsCheck string
	for _, line := range strings.Split(unit, "\n") {
		switch {
		case strings.HasPrefix(line, "ExecStartPost=/bin/sh -c"):
			hostCheck = strings.TrimPrefix(line, "ExecStartPost=")
		case strings.HasPrefix(line, "ExecStartPost=/usr/sbin/ip netns exec orama-global /bin/sh -c"):
			nsCheck = strings.TrimPrefix(line, "ExecStartPost=/usr/sbin/ip netns exec orama-global ")
		}
	}
	if hostCheck == "" || nsCheck == "" {
		t.Fatalf("the unit has no ExecStartPost read-back of the IPv6 sysctls:\n%s", unit)
	}
	if strings.Contains(unit, "ExecStartPost=-") {
		t.Errorf("an ExecStartPost that ignores failure would let a namespace with IPv6 start:\n%s", unit)
	}
	postAt, startAt := strings.Index(unit, "ExecStartPost="), strings.LastIndex(unit, "ExecStart=")
	if postAt < startAt {
		t.Error("the read-back must come after the last ExecStart")
	}

	// Run each check against a stand-in for /proc/sys/net/ipv6.
	run := func(command, procDir string) (string, error) {
		script := strings.TrimSuffix(strings.TrimPrefix(command, "/bin/sh -c '"), "'")
		script = strings.ReplaceAll(script, "/proc/sys/net/ipv6", procDir)
		out, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
		return string(out), err
	}
	write := func(dir string, values map[string]string) {
		for iface, v := range values {
			path := filepath.Join(dir, "conf", iface, "disable_ipv6")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(v+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	off := t.TempDir()
	write(off, map[string]string{"ogl-host": "1", "all": "1", "default": "1", "ogl-ns": "1"})
	for name, cmd := range map[string]string{"host": hostCheck, "namespace": nsCheck} {
		if out, err := run(cmd, off); err != nil {
			t.Errorf("%s check failed with IPv6 off: %v %s", name, err, out)
		}
	}

	on := t.TempDir()
	write(on, map[string]string{"ogl-host": "0", "all": "1", "default": "1", "ogl-ns": "1"})
	if out, err := run(hostCheck, on); err == nil || !strings.Contains(out, "IPv6 is still enabled") {
		t.Errorf("the host check passed with IPv6 on the veth (%v): %s", err, out)
	}
	on = t.TempDir()
	write(on, map[string]string{"ogl-host": "1", "all": "1", "default": "1", "ogl-ns": "0"})
	if out, err := run(nsCheck, on); err == nil || !strings.Contains(out, "IPv6 is still enabled") {
		t.Errorf("the namespace check passed with IPv6 on the namespace veth end (%v): %s", err, out)
	}
	missing := t.TempDir()
	write(missing, map[string]string{"all": "1"})
	if _, err := run(nsCheck, missing); err == nil {
		t.Error("the namespace check passed with a sysctl file missing on a kernel that has IPv6")
	}

	if out, err := run(hostCheck, filepath.Join(t.TempDir(), "no-ipv6")); err != nil {
		t.Errorf("a kernel with no IPv6 must pass: %v %s", err, out)
	}
	if out, err := run(nsCheck, filepath.Join(t.TempDir(), "no-ipv6")); err != nil {
		t.Errorf("a kernel with no IPv6 must pass: %v %s", err, out)
	}
}
