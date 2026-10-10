package globalnetns

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeHost struct {
	calls     []string
	systemd   string
	failOn    string
	routes    string
	missing   map[string]bool
	noNetProc bool
	goos      string
}

func (f *fakeHost) host() Host {
	goos := "linux"
	if f.goos != "" {
		goos = f.goos
	}
	return Host{
		GOOS: goos,
		Run: func(name string, args ...string) ([]byte, error) {
			call := name + " " + strings.Join(args, " ")
			f.calls = append(f.calls, call)
			if f.failOn != "" && strings.Contains(call, f.failOn) {
				return []byte("Operation not permitted"), errors.New("exit status 2")
			}
			switch {
			case name == "systemctl":
				return []byte(f.systemd), nil
			case strings.HasSuffix(name, "ip") && len(args) > 1 && args[1] == "route":
				return []byte(f.routes), nil
			}
			return nil, nil
		},
		LookPath: func(n string) (string, error) {
			if f.missing[n] {
				return "", errors.New("not found")
			}
			return "/usr/sbin/" + n, nil
		},
		Exists: func(string) bool { return !f.noNetProc },
	}
}

func goodHost() *fakeHost {
	return &fakeHost{systemd: "systemd 252 (252.22-1)\n+PAM\n", routes: "default via 1.2.3.1 dev eth0\n"}
}

func TestPreflight_acceptsACapableMachine(t *testing.T) {
	f := goodHost()
	tools, err := Preflight(f.host())
	if err != nil {
		t.Fatal(err)
	}
	if tools.IP != "/usr/sbin/ip" || tools.Nft != "/usr/sbin/nft" || tools.Sysctl != "/usr/sbin/sysctl" {
		t.Errorf("tools = %+v", tools)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{"netns add orama-netns-probe", "netns del orama-netns-probe", "link del oglprobe0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("probe never ran %q:\n%s", want, joined)
		}
	}
}

func TestPreflight_refusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fakeHost)
		want  string
	}{
		{"not linux", func(f *fakeHost) { f.goos = "darwin" }, "Linux feature"},
		{"kernel without netns", func(f *fakeHost) { f.noNetProc = true }, "no network namespaces"},
		{"no ip", func(f *fakeHost) { f.missing = map[string]bool{"ip": true} }, "apt-get install -y iproute2"},
		{"no nft", func(f *fakeHost) { f.missing = map[string]bool{"nft": true} }, "apt-get install -y nftables"},
		{"old systemd", func(f *fakeHost) { f.systemd = "systemd 241 (241)\n" }, "too old"},
		{"garbled systemd", func(f *fakeHost) { f.systemd = "what\n" }, "cannot read the systemd version"},
		{"netns forbidden", func(f *fakeHost) { f.failOn = "netns add" }, "cannot create a network namespace"},
		{"no veth", func(f *fakeHost) { f.failOn = "type veth" }, "modprobe veth"},
		{"range taken", func(f *fakeHost) { f.routes = "198.18.0.0/24 dev vpn0 scope link\n" }, "already routed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := goodHost()
			tc.setup(f)
			_, err := Preflight(f.host())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestPreflight_ownRouteIsNotAConflict(t *testing.T) {
	f := goodHost()
	f.routes = "198.18.0.0/30 dev ogl-host proto kernel scope link src 198.18.0.1\n"
	if _, err := Preflight(f.host()); err != nil {
		t.Fatalf("a re-run refused its own veth route: %v", err)
	}
}

func TestPreflight_reportsAProbeThatCannotBeRemoved(t *testing.T) {
	f := goodHost()
	f.failOn = "netns del"
	_, err := Preflight(f.host())
	if err == nil || !strings.Contains(err.Error(), "ip netns del orama-netns-probe") {
		t.Fatalf("err = %v, want the leftover probe named", err)
	}
}

func TestVerify(t *testing.T) {
	p := DefaultPaths("/etc/systemd/system")
	all := func(string) bool { return true }
	if err := Verify(Name, p, all); err != nil {
		t.Fatalf("complete layout refused: %v", err)
	}
	if err := Verify("", p, all); err == nil {
		t.Error("an unrecorded layout was accepted")
	}
	if err := Verify("other", p, all); err == nil {
		t.Error("a different namespace name was accepted")
	}
	for _, missing := range []string{p.Unit, p.HostFile, p.NSFile, p.Resolv} {
		err := Verify(Name, p, func(f string) bool { return f != missing })
		if err == nil || !strings.Contains(err.Error(), fmt.Sprint(missing)) {
			t.Errorf("missing %s: err = %v", missing, err)
		}
	}
}

// A Debian image may ship without nftables (stagenet athena, 2026-09-30); the
// co-located install provisions the layout's tools like it does WireGuard.
func TestInstallTools_installsOnlyTheMissingPackages(t *testing.T) {
	f := goodHost()
	f.missing = map[string]bool{"nft": true, "ip": true}
	if err := InstallTools(f.host()); err != nil {
		t.Fatal(err)
	}
	want := "apt-get install -y --no-install-recommends iproute2 nftables"
	if len(f.calls) != 1 || f.calls[0] != want {
		t.Fatalf("calls = %q, want [%q]", f.calls, want)
	}
}

func TestInstallTools_leavesACompleteMachineAlone(t *testing.T) {
	f := goodHost()
	if err := InstallTools(f.host()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("a machine with every tool ran %q", f.calls)
	}
	f.goos = "darwin"
	f.missing = map[string]bool{"nft": true}
	if err := InstallTools(f.host()); err != nil || len(f.calls) != 0 {
		t.Fatalf("non-linux: err %v, calls %q; Preflight refuses it, InstallTools must not act", err, f.calls)
	}
}

func TestInstallTools_refusals(t *testing.T) {
	noApt := goodHost()
	noApt.missing = map[string]bool{"nft": true, "apt-get": true}
	if err := InstallTools(noApt.host()); err == nil || !strings.Contains(err.Error(), "no apt-get") || !strings.Contains(err.Error(), "nftables") {
		t.Errorf("no apt-get: err = %v", err)
	}
	failing := goodHost()
	failing.missing = map[string]bool{"nft": true}
	failing.failOn = "apt-get install"
	if err := InstallTools(failing.host()); err == nil || !strings.Contains(err.Error(), "apt-get install -y nftables") {
		t.Errorf("apt-get failure: err = %v", err)
	}
}
