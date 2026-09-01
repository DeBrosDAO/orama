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
		"orama-namespace-wireguard@.service":     {"wg-quick up wg0", "wg show wg0"},
		"orama-namespace-ipfs@.service":          {"ipfs daemon", "IPFS_PATH"},
		"orama-namespace-ipfs-cluster@.service":  {"ipfs-cluster-service daemon", "127.0.0.1:10107"},
		"orama-namespace-vault@.service":         {"data/vault/vault.yaml"},
		"orama-namespace-caddy@.service":         {"/etc/caddy/Caddyfile", "localhost:10104/health", "orama-namespace-coredns@nameserver.service"},
		"orama-namespace-anyone-client@.service": {"/etc/anon/anonrc"},
		"orama-namespace-sni-router@.service":    {"Before=orama-namespace-caddy@%i.service"},
		"orama-namespace-coredns@.service":       {"/etc/coredns/Corefile", "orama-namespace-rqlite@index.service"},
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
