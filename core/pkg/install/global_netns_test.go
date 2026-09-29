package install

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// colocatedFixture is a global fixture on a machine that already runs a
// cluster node and can hold the namespace layout.
type colocatedFixture struct {
	*globalFixture
	oramaDir string
	etc      string
	missing  map[string]bool
	probeErr string
}

func newColocatedFixture(t *testing.T) *colocatedFixture {
	t.Helper()
	f := &colocatedFixture{globalFixture: newGlobalFixture(t), missing: map[string]bool{}}
	tmp := t.TempDir()
	f.oramaDir = filepath.Join(tmp, "opt", "orama", ".orama")
	f.etc = filepath.Join(tmp, "etc")
	for _, dir := range []string{f.oramaDir, f.etc} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.writePrefs(t, "branch: main\nnameserver: true\n")
	run := func(name string, args ...string) ([]byte, error) {
		call := name + " " + strings.Join(args, " ")
		switch {
		case name == "systemctl" && len(args) == 1 && args[0] == "--version":
			return []byte("systemd 252 (252.22-1)\n"), nil
		case f.probeErr != "" && strings.Contains(call, f.probeErr):
			return []byte("not permitted"), errors.New("exit status 1")
		case strings.HasSuffix(name, "/ip"):
			f.node.calls = append(f.node.calls, append([]string{name}, args...))
			return nil, nil
		}
		return f.node.run(name, args...)
	}
	f.host.Run = run
	f.host.Netns = NetnsHost{
		Probe: globalnetns.Host{
			GOOS: "linux", Run: run,
			LookPath: func(n string) (string, error) {
				if f.missing[n] {
					return "", errors.New("not found")
				}
				return "/usr/sbin/" + n, nil
			},
			Exists: func(string) bool { return true },
		},
		Root:       rootfs.At(f.etc),
		ConfigDir:  filepath.Join(f.etc, "orama-global"),
		SysctlFile: filepath.Join(f.etc, "sysctl.d", "60-orama-global-netns.conf"),
		OramaDir:   f.oramaDir,
	}
	return f
}

func (f *colocatedFixture) writePrefs(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.oramaDir, "preferences.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *colocatedFixture) prefs(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.oramaDir, "preferences.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *colocatedFixture) options(services ...GlobalService) GlobalInstallOptions {
	o := f.globalFixture.options(services...)
	o.Colocated = true
	return o
}

func TestInstallGlobal_colocatedWritesTheNamespaceLayout(t *testing.T) {
	f := newColocatedFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain, GlobalServiceProvider), f.host); err != nil {
		t.Fatal(err)
	}
	cfg := f.host.Netns.ConfigDir
	hostRules, err := os.ReadFile(filepath.Join(cfg, "netns-host.nft"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hostRules), "tcp dport { 31000, 31013 } dnat to 198.18.0.2") ||
		!strings.Contains(string(hostRules), "udp dport { 31000 } dnat to 198.18.0.2") {
		t.Errorf("host rules do not publish the chain and provider ports:\n%s", hostRules)
	}
	for _, name := range []string{"netns.nft", "resolv.conf"} {
		if _, err := os.Stat(filepath.Join(cfg, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
	if _, err := os.Stat(f.host.Netns.SysctlFile); err != nil {
		t.Errorf("the forwarding sysctl was not written: %v", err)
	}
	netnsUnit, err := os.ReadFile(filepath.Join(f.host.UnitDir, globalnetns.UnitName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(netnsUnit), "ip netns add orama-global") {
		t.Errorf("namespace unit:\n%s", netnsUnit)
	}
	for _, unit := range []string{constants.ChainServiceUnit, constants.GlobalProviderUnit} {
		body, err := os.ReadFile(filepath.Join(f.host.UnitDir, unit))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"NetworkNamespacePath=/run/netns/orama-global", "BindsTo=orama-global-netns.service"} {
			if !strings.Contains(string(body), want) {
				t.Errorf("%s lacks %s", unit, want)
			}
		}
	}
	if got := f.prefs(t); !strings.Contains(got, "role: both") || !strings.Contains(got, "global_netns: orama-global") ||
		!strings.Contains(got, "nameserver: true") {
		t.Errorf("preferences = %q, want role both, the namespace, and the cluster's own settings kept", got)
	}
	wantSystemctl := []string{"daemon-reload", "enable " + constants.ChainServiceUnit, "enable " + constants.GlobalProviderUnit, "enable " + globalnetns.UnitName}
	if got := f.node.named("systemctl"); !slices.Equal(got, wantSystemctl) {
		t.Errorf("systemctl calls = %v, want %v", got, wantSystemctl)
	}
}

func TestInstallGlobal_colocatedFirewallUsesRouteRules(t *testing.T) {
	f := newColocatedFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain, GlobalServiceProvider), f.host); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"status",
		"route delete allow in on ogl-host",
		"route allow in on ogl-host from 198.18.0.2 comment orama-global",
		"route allow proto tcp to 198.18.0.2 port 31000 comment orama-global",
		"route allow proto udp to 198.18.0.2 port 31000 comment orama-global",
		"route allow proto tcp to 198.18.0.2 port 31013 comment orama-global",
	}
	if got := f.node.named("ufw"); !slices.Equal(got, want) {
		t.Errorf("ufw calls = %v, want %v", got, want)
	}
}

func TestInstallGlobal_colocatedSecondRunChangesNothing(t *testing.T) {
	f := newColocatedFixture(t)
	opts := f.options(GlobalServiceChain)
	if err := InstallGlobal(opts, f.host); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(f.host.Netns.ConfigDir, "netns.nft"))
	prefs := f.prefs(t)
	f.node.calls = nil
	if err := InstallGlobal(opts, f.host); err != nil {
		t.Fatalf("second install: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(f.host.Netns.ConfigDir, "netns.nft"))
	if string(first) != string(second) || len(second) == 0 {
		t.Error("the namespace rules changed between two identical installs")
	}
	if f.prefs(t) != prefs {
		t.Errorf("preferences changed on the second install:\n%s\n%s", prefs, f.prefs(t))
	}
	if changes := f.node.changes(); len(changes) != 0 {
		t.Errorf("second install changed accounts: %v", changes)
	}
}

func TestInstallGlobal_colocatedRefusalsChangeNothing(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *colocatedFixture)
		want  string
	}{
		{"no ip binary", func(_ *testing.T, f *colocatedFixture) { f.missing["ip"] = true }, "apt-get install -y iproute2"},
		{"no nft binary", func(_ *testing.T, f *colocatedFixture) { f.missing["nft"] = true }, "apt-get install -y nftables"},
		{"kernel refuses a namespace", func(_ *testing.T, f *colocatedFixture) { f.probeErr = "netns add" }, "cannot create a network namespace"},
		{"no cluster node", func(t *testing.T, f *colocatedFixture) {
			if err := os.Remove(filepath.Join(f.oramaDir, "preferences.yaml")); err != nil {
				t.Fatal(err)
			}
		}, "no cluster node is installed here"},
		{"global-only machine", func(t *testing.T, f *colocatedFixture) { f.writePrefs(t, "role: global\n") }, "no cluster node to share with"},
		{"unreadable preferences", func(t *testing.T, f *colocatedFixture) { f.writePrefs(t, "role: [\n") }, "parse"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newColocatedFixture(t)
			tc.setup(t, f)
			before := f.prefsOrEmpty()
			err := InstallGlobal(f.options(GlobalServiceChain), f.host)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if len(f.node.changes()) != 0 || f.node.named("systemctl") != nil || f.node.named("ufw") != nil {
				t.Errorf("the host changed before the refusal: %v", f.node.calls)
			}
			if _, err := os.Stat(f.host.Netns.ConfigDir); !os.IsNotExist(err) {
				t.Errorf("the namespace config directory exists after a refusal")
			}
			if f.prefsOrEmpty() != before {
				t.Errorf("preferences changed by a refused install")
			}
		})
	}
}

func (f *colocatedFixture) prefsOrEmpty() string {
	data, _ := os.ReadFile(filepath.Join(f.oramaDir, "preferences.yaml"))
	return string(data)
}

func TestInstallGlobal_globalOnlyInstallRefusedOnAColocatedMachine(t *testing.T) {
	f := newColocatedFixture(t)
	f.writePrefs(t, "role: both\nglobal_netns: orama-global\n")
	err := InstallGlobal(f.globalFixture.options(GlobalServiceChain), f.host)
	if err == nil || !strings.Contains(err.Error(), "--colocated") {
		t.Fatalf("err = %v, want a refusal naming --colocated", err)
	}
	if _, statErr := os.Stat(filepath.Join(f.host.UnitDir, constants.ChainServiceUnit)); !os.IsNotExist(statErr) {
		t.Error("a chain unit in the root namespace was written on a co-located machine")
	}
}

func TestInstallGlobal_globalOnlyInstallIgnoresAMachineWithNoPreferences(t *testing.T) {
	f := newColocatedFixture(t)
	if err := os.Remove(filepath.Join(f.oramaDir, "preferences.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(f.globalFixture.options(GlobalServiceChain), f.host); err != nil {
		t.Fatalf("a global-only machine was refused: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(f.host.UnitDir, constants.ChainServiceUnit))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "NetworkNamespacePath") {
		t.Error("a global-only unit joined the namespace")
	}
}

func TestGlobalFirewallPorts(t *testing.T) {
	got := GlobalFirewall{ChainP2P: true, Provider: true}.Ports()
	want := []globalnetns.Port{{Proto: "tcp", Number: 31000}, {Proto: "udp", Number: 31000}, {Proto: "tcp", Number: 31013}}
	if !slices.Equal(got, want) {
		t.Errorf("Ports() = %v, want %v", got, want)
	}
	if got := (GlobalFirewall{}).Ports(); len(got) != 0 {
		t.Errorf("no service published ports %v", got)
	}
}

func TestInstallGlobal_colocatedPutsTheKuboIndexerAndCosmovisorUnitsInTheNamespace(t *testing.T) {
	f := newColocatedFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain, GlobalServiceIPFS, GlobalServiceProvider, GlobalServiceIndexer), f.host); err != nil {
		t.Fatal(err)
	}
	hostRules, err := os.ReadFile(filepath.Join(f.host.Netns.ConfigDir, "netns-host.nft"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hostRules), "tcp dport { 31000, 31010, 31013 } dnat to 198.18.0.2") ||
		!strings.Contains(string(hostRules), "udp dport { 31000, 31010 } dnat to 198.18.0.2") {
		t.Errorf("host rules do not publish the Kubo swarm beside the chain and provider ports:\n%s", hostRules)
	}
	if strings.Contains(string(hostRules), "31011") || strings.Contains(string(hostRules), "31015") {
		t.Errorf("a loopback port (Kubo RPC, indexer) is published:\n%s", hostRules)
	}
	inNamespace := []string{constants.ChainServiceUnit, constants.GlobalIPFSUnit, globalIPFSGCUnit, constants.GlobalProviderUnit, constants.GlobalIndexerUnit}
	for _, unit := range inNamespace {
		body, err := os.ReadFile(filepath.Join(f.host.UnitDir, unit))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"NetworkNamespacePath=/run/netns/orama-global", "BindsTo=orama-global-netns.service"} {
			if !strings.Contains(string(body), want) {
				t.Errorf("%s lacks %s", unit, want)
			}
		}
	}
	chain, _ := os.ReadFile(filepath.Join(f.host.UnitDir, constants.ChainServiceUnit))
	if !strings.Contains(string(chain), "/cosmovisor run start") || !strings.Contains(string(chain), "check-sign-floor") {
		t.Errorf("the co-located chain unit is not the cosmovisor unit:\n%s", chain)
	}
	timer, err := os.ReadFile(filepath.Join(f.host.UnitDir, globalIPFSGCTimer))
	if err != nil || strings.Contains(string(timer), "NetworkNamespacePath=") || string(timer) != RenderGlobalIPFSGCTimer() {
		t.Errorf("the GC timer must be written unchanged, outside the namespace (%v)", err)
	}
	// The provider and Kubo share the namespace, so the provider's dial of 127.0.0.1:31011 stays local.
	provider, _ := os.ReadFile(filepath.Join(f.host.UnitDir, constants.GlobalProviderUnit))
	if !strings.Contains(string(provider), "--ipfs-api http://127.0.0.1:31011") {
		t.Errorf("provider does not use Kubo's loopback RPC:\n%s", provider)
	}
	wantRoutes := []string{
		"route delete allow in on ogl-host",
		"route allow in on ogl-host from 198.18.0.2 comment orama-global",
		"route allow proto tcp to 198.18.0.2 port 31000 comment orama-global",
		"route allow proto udp to 198.18.0.2 port 31000 comment orama-global",
		"route allow proto tcp to 198.18.0.2 port 31010 comment orama-global",
		"route allow proto udp to 198.18.0.2 port 31010 comment orama-global",
		"route allow proto tcp to 198.18.0.2 port 31013 comment orama-global",
	}
	want := append([]string{"status"}, wantRoutes...)
	if got := f.node.named("ufw"); !slices.Equal(got, want) {
		t.Errorf("ufw calls = %v, want %v", got, want)
	}
	systemctl := f.node.named("systemctl")
	for _, unit := range []string{globalIPFSGCTimer, constants.GlobalIndexerUnit, globalnetns.UnitName} {
		if !slices.Contains(systemctl, "enable "+unit) {
			t.Errorf("%s was not enabled: %v", unit, systemctl)
		}
	}
	if slices.Contains(systemctl, "enable "+globalIPFSGCUnit) {
		t.Error("the GC oneshot was enabled")
	}
}

func TestGlobalFirewallPorts_publicKuboSwarm(t *testing.T) {
	got := GlobalFirewall{PublicStorage: true}.Ports()
	want := []globalnetns.Port{{Proto: "tcp", Number: 31010}, {Proto: "udp", Number: 31010}}
	if !slices.Equal(got, want) {
		t.Errorf("Ports() = %v, want %v", got, want)
	}
}

// The forward rule for the namespace's own traffic names its source: it used to allow anything
// arriving on ogl-host, so a source other than the namespace's address was forwarded too. The
// broad rule an earlier release added is deleted first.
func TestNetnsRouteArgs_forwardOnlyFromTheNamespaceAddress(t *testing.T) {
	args := netnsRouteArgs([]string{"31000/tcp"})
	got := make([]string, len(args))
	for i, a := range args {
		got[i] = strings.Join(a, " ")
	}
	want := []string{
		"route delete allow in on ogl-host",
		"route allow in on ogl-host from 198.18.0.2 comment orama-global",
		"route allow proto tcp to 198.18.0.2 port 31000 comment orama-global",
	}
	if !slices.Equal(got, want) {
		t.Errorf("netnsRouteArgs = %v, want %v", got, want)
	}
	for _, line := range got[1:] {
		if strings.HasPrefix(line, "route allow in on ogl-host ") && !strings.Contains(line, "from 198.18.0.2") {
			t.Errorf("%q forwards traffic from any source", line)
		}
	}
}
