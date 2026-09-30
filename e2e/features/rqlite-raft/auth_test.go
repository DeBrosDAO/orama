//go:build e2e_fleet

package rqliteraft

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// unauthStatus is the HTTP status of an rqlite call made with the given
// curl credential options from one node to another's WireGuard address.
func unauthStatus(t testing.TB, f *fleet.Fleet, from fleet.Node, toWG, method, path, cred string) string {
	t.Helper()
	cmd := fmt.Sprintf("curl -s -o /dev/null -w '%%{http_code}' --max-time 10 -X %s %s -H 'Content-Type: application/json' "+
		"--data-binary '[[\"SELECT 1\"]]' http://%s:%d%s", method, cred, toWG, infra.IndexRQLiteHTTP, path)
	return strings.TrimSpace(f.MustExec(t, from, cmd).Stdout)
}

// TestRQLite_authRequiredOnTheMesh: the index rqlite always runs with -auth.
// An unauthenticated or wrongly authenticated call from any node of the
// mesh, to its own rqlite or a peer's, is 401 on every data and cluster
// endpoint; the node's own credentials work (docs/SECURITY.md "RQLite
// Authentication": unauthenticated POST /db/execute from the mesh is 401).
func TestRQLite_authRequiredOnTheMesh(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, from := range f.State.Nodes {
		for _, to := range f.State.Nodes {
			wg := wgOf(t, f, to)
			for _, call := range []struct{ method, path string }{
				{"POST", "/db/execute"}, {"POST", "/db/query"}, {"GET", "/status"}, {"GET", "/nodes"},
				{"GET", "/db/backup"}, {"POST", "/remove"},
			} {
				for cred, label := range map[string]string{"": "no credential", "-u orama:wrong-password": "a wrong password", "-u admin:admin": "a guessed user"} {
					if got := unauthStatus(t, f, from, wg, call.method, call.path, cred); got != "401" {
						t.Errorf("%s -> %s %s %s with %s: HTTP %s, want 401", from.Name, to.Name, call.method, call.path, label, got)
					}
				}
			}
		}
		if q := infra.IndexQuery(t, f, from, "SELECT 1"); len(q.Values) != 1 {
			t.Errorf("%s: an authenticated query returned %v", from.Name, q.Values)
		}
	}
}

// TestRQLite_startedWithAuth: every index rqlited was started with -auth
// pointing at its copy of the credentials, which is 0600 orama and not
// readable through the unit's view of the secrets directory.
func TestRQLite_startedWithAuth(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		pid := strings.TrimSpace(f.MustExec(t, n, "systemctl show -p MainPID --value "+infra.IndexRQLiteUnit).Stdout)
		args := strings.Split(strings.TrimSpace(f.MustExec(t, n, "tr '\\0' '\\n' < /proc/"+pid+"/cmdline").Stdout), "\n")
		authFile := ""
		for i, a := range args {
			if a == "-auth" && i+1 < len(args) {
				authFile = args[i+1]
			}
		}
		if authFile == "" {
			t.Errorf("%s: rqlited runs without -auth: %v", n.Name, args)
			continue
		}
		infra.RequireStat(t, f, n, authFile, "orama", "orama", "600")
		for _, a := range args {
			if strings.Contains(a, "password") {
				t.Errorf("%s: rqlited's command line carries a credential-like argument", n.Name)
			}
		}
	}
}

// TestRQLite_bindsTheWireGuardAddress: the index rqlite's HTTP and raft
// ports listen on the node's WireGuard address only, never on every
// interface (docs/SECURITY.md "Network isolation").
func TestRQLite_bindsTheWireGuardAddress(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		wg := wgOf(t, f, n)
		seen := map[int]bool{}
		for _, l := range f.Listeners(t, n) {
			if l.Proto != "tcp" || (l.Port != infra.IndexRQLiteHTTP && l.Port != infra.IndexRQLiteRaft) {
				continue
			}
			seen[l.Port] = true
			if l.Addr != wg {
				t.Errorf("%s: rqlite port %d listens on %s, want only the WireGuard address %s", n.Name, l.Port, l.Addr, wg)
			}
		}
		for _, p := range []int{infra.IndexRQLiteHTTP, infra.IndexRQLiteRaft} {
			if !seen[p] {
				t.Errorf("%s: nothing listens on the rqlite port %d", n.Name, p)
			}
		}
	}
}

func wgOf(t testing.TB, f *fleet.Fleet, n fleet.Node) string {
	t.Helper()
	return strings.TrimSpace(f.MustExec(t, n, "ip -4 -o addr show dev wg0 | awk '{print $4}' | cut -d/ -f1").Stdout)
}
