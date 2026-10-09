package systemd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLeftoverHostUnits_doNotIncludeNodeOrCoreDNS(t *testing.T) {
	for _, u := range LeftoverHostUnits {
		if u == "orama-node.service" || u == "coredns.service" {
			t.Errorf("leftover list must not disable %s", u)
		}
	}
	if LeftoverWireGuardUnit != "wg-quick@wg0.service" {
		t.Errorf("LeftoverWireGuardUnit = %s", LeftoverWireGuardUnit)
	}
	if LeftoverNameserverUnit != "coredns.service" {
		t.Errorf("LeftoverNameserverUnit = %s", LeftoverNameserverUnit)
	}
	for _, u := range LeftoverHostUnits {
		if u == LeftoverNameserverUnit {
			t.Error("coredns leftover belongs on LeftoverNameserverUnit, not LeftoverHostUnits")
		}
	}
}

func TestTemplateUnits_existOnDisk(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "systemd")
	for _, name := range TemplateUnits {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing template %s: %v", path, err)
		}
	}
}

func TestTemplateUnits_hostStackAdoptsExistingPaths(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "systemd")
	checks := map[string][]string{
		"orama-namespace-wireguard@.service": {"wg-quick up wg0", "wg show wg0"},
		"orama-namespace-ipfs@.service":      {"ipfs daemon", "IPFS_PATH"},
		"orama-namespace-ipfs-cluster@.service": {
			"ExecStart=/opt/orama/bin/orama serve-ipfs-cluster",
			"127.0.0.1:10107",
			"127.0.0.1:10110",
		},
		"orama-namespace-ipfs-gc@.service":    {"ExecStart=/opt/orama/bin/orama node ipfs-gc", "EnvironmentFile=/var/lib/orama-unit-env/%i/ipfs-gc.env"},
		"orama-namespace-vault@.service":      {"data/vault/vault.yaml"},
		"orama-namespace-caddy@.service":      {"/etc/caddy/Caddyfile", "XDG_CONFIG_HOME=/var/lib/caddy/config", "orama-namespace-coredns@nameserver.service"},
		"orama-namespace-tor@.service":        {"/etc/orama/tor/torrc", "StateDirectory=orama-tor"},
		"orama-namespace-sni-router@.service": {"Before=orama-namespace-caddy@%i.service"},
		"orama-namespace-coredns@.service":    {"/etc/coredns/Corefile", "orama-namespace-rqlite@index.service"},
	}
	for name, needles := range checks {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body := string(data)
		for _, n := range needles {
			if !strings.Contains(body, n) {
				t.Errorf("%s missing %q", name, n)
			}
		}
		if strings.Contains(body, "namespaces/%i/rqlite") && name != "orama-namespace-rqlite@.service" {
			t.Errorf("%s must not point rqlite at namespaces/%%i", name)
		}
	}
}

// rqlited rewrites datetime('now') into a literal in its own zone, so a host
// set to Europe/Berlin made every registry timestamp two hours ahead of UTC and
// the device-login prune delete live logins. The unit pins the process to UTC.
func TestNamespaceRqliteUnit_runsInUTC(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "systemd", "orama-namespace-rqlite@.service")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\nEnvironment=TZ=UTC\n") {
		t.Errorf("%s must set Environment=TZ=UTC: rqlited stamps datetime('now') in its local zone", path)
	}
}

// RLIMIT_NPROC is checked against every process and thread the unit's user owns
// on the host, not the unit's own. The units share their users (orama runs every
// namespace's processes), so a LimitNPROC in one of them fails that unit when the
// others grow: Caddy aborted that way and took the node's HTTPS with it. A unit
// bounds itself with TasksMax, which counts its own cgroup.
func TestTemplateUnits_noPerUserProcessLimit(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	units, err := filepath.Glob(filepath.Join(filepath.Dir(file), "..", "..", "systemd", "*.service"))
	if err != nil {
		t.Fatal(err)
	}
	if len(units) == 0 {
		t.Fatal("no unit templates found")
	}
	for _, path := range units {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "LimitNPROC") {
				t.Errorf("%s sets %s; bound the unit with TasksMax instead", filepath.Base(path), strings.TrimSpace(line))
			}
		}
	}
}
