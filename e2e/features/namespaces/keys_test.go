//go:build e2e_fleet

package namespaces

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// What `orama namespace keys create|rotate` print (core/cmd/orama/internal
// namespace_commands.go): "  id:        7", "  new id:    8", and the key on
// its own line.
var (
	keyIDLine    = regexp.MustCompile(`(?m)^\s*(?:new )?id:\s+(\d+)\s*$`)
	keyValueLine = regexp.MustCompile(`orama_[A-Za-z0-9_]+`)
)

const (
	// keyReloadBudget: a revoked key is refused everywhere within the
	// revocation reload (docs/AUTH.md#revoking), plus the round trip.
	keyReloadBudget = 30 * time.Second
	pathCacheHealth = "/v1/cache/health"
)

type cliKey struct{ ID, Key string }

func cliKeyCreate(t testing.TB, cli *oramacli.Runner, args ...string) cliKey {
	t.Helper()
	out := cli.MustOK(t, append([]string{"namespace", "keys", "create"}, args...)...).Stdout
	id, key := keyIDLine.FindStringSubmatch(out), keyValueLine.FindString(out)
	if id == nil || key == "" {
		t.Fatalf("orama namespace keys create printed no id or key")
	}
	k := cliKey{ID: id[1], Key: key}
	t.Cleanup(func() { cliRevoke(t, cli, k.ID) })
	return k
}

// cliRevoke revokes id at cleanup; already revoked is fine.
func cliRevoke(t testing.TB, cli *oramacli.Runner, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	res, err := cli.Run(ctx, "namespace", "keys", "revoke", "--id", id)
	if err != nil || res.Exit != 0 {
		t.Errorf("cleanup: revoking key %s: exit %d %v %s", id, res.Exit, err, res.Stderr)
	}
}

// keyServes is an eventually probe: does key reach the cache route?
func keyServes(t testing.TB, n *ns.Namespace, key string, want bool) func() (bool, error) {
	return func() (bool, error) {
		r := tenancy.Get(t, n.Client, pathCacheHealth, tenancy.Cred{APIKey: key})
		if (r.Status == http.StatusOK) != want {
			return false, fmt.Errorf("HTTP %d %s", r.Status, r.ErrorCode())
		}
		return true, nil
	}
}

// TestNamespaceKeys_cliCreateListRotateRevoke drives the five key commands:
// a key works, list shows it, rotate mints a successor while the original
// keeps working for the overlap, revoke stops a key, revoke-legacy finds none
// on a namespace that never had one (docs/CLI_REFERENCE.md "orama namespace
// keys").
func TestNamespaceKeys_cliCreateListRotateRevoke(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	cli := n.CLI
	k := cliKeyCreate(t, cli, "--scope", "cache", "--label", "e2e-cli", "--expires-in-days", "30")
	eventually.Require(t, pollEvery, keyReloadBudget, "the new key to work", keyServes(t, n, k.Key, true))
	list := cli.MustOK(t, "namespace", "keys", "list").Stdout
	if !strings.Contains(list, "#"+k.ID) || !strings.Contains(list, "cache") || !strings.Contains(list, "e2e-cli") {
		t.Fatalf("keys list does not show #%s cache e2e-cli:\n%s", k.ID, list)
	}
	rot := cli.MustOK(t, "namespace", "keys", "rotate", "--id", k.ID, "--overlap-days", "1").Stdout
	id, key := keyIDLine.FindStringSubmatch(rot), keyValueLine.FindString(rot)
	if id == nil || key == "" || id[1] == k.ID {
		t.Fatalf("rotate printed no new id or key")
	}
	revoked := false
	t.Cleanup(func() {
		if !revoked {
			cliRevoke(t, cli, id[1])
		}
	})
	for _, kk := range []string{k.Key, key} {
		eventually.Require(t, pollEvery, keyReloadBudget, "both keys to work in the overlap", keyServes(t, n, kk, true))
	}
	if list := cli.MustOK(t, "namespace", "keys", "ls").Stdout; !strings.Contains(list, "rotated from #"+k.ID) {
		t.Fatalf("keys list does not show the rotation:\n%s", list)
	}
	cli.MustOK(t, "namespace", "keys", "revoke", "--id", id[1])
	revoked = true
	eventually.Require(t, pollEvery, keyReloadBudget, "the revoked key to stop working", keyServes(t, n, key, false))
	if out := cli.MustOK(t, "namespace", "keys", "revoke-legacy", "--force").Stdout; !strings.Contains(out, "Revoked 0") {
		t.Fatalf("revoke-legacy on a fresh namespace printed %q", out)
	}
	eventually.Require(t, pollEvery, keyReloadBudget, "a scoped key to survive revoke-legacy", keyServes(t, n, k.Key, true))
}

func TestNamespaceKeys_cliRefusals(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	for _, args := range [][]string{
		{"create"}, {"create", "--scope", "everything"}, {"create", "--scope", "cache", "--expires-in-days", "366"},
		{"create", "--scope", "cache", "--expires-in-days", "-1"}, {"revoke", "--id", "999999"}, {"revoke"},
		{"rotate", "--id", "999999"}, {"rotate", "--id", "1", "--overlap-days", "31"}, {"revoke-legacy"},
	} {
		res, err := n.CLI.Run(t.Context(), append([]string{"namespace", "keys"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		if res.Exit == 0 && !strings.Contains(res.Stdout, "Revoked 0") {
			t.Errorf("orama namespace keys %s succeeded: %s", strings.Join(args, " "), res.Stdout)
		}
	}
	out := n.CLI.MustOK(t, "namespace", "keys", "--help").Stdout
	for _, sub := range []string{"create", "list", "revoke", "revoke-legacy", "rotate"} {
		if !strings.Contains(out, sub) {
			t.Errorf("orama namespace keys --help does not list %s", sub)
		}
	}
}

// TestNamespaceKeys_namespaceFlagIsHonoured: --namespace names the namespace
// the key is for (docs/CLI_REFERENCE.md). A CLI signed in to A asking for a key
// in B must not quietly mint one in A.
func TestNamespaceKeys_namespaceFlagIsHonoured(t *testing.T) {
	t.Parallel()
	pair := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{Via: ns.ViaOperator})
	a, b := pair[0], pair[1]
	res, err := a.CLI.Run(t.Context(), "namespace", "keys", "create", "--namespace", b.Name, "--scope", "cache", "--label", "for-b")
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != 0 {
		return
	}
	// Only a key that landed in A is A's to revoke; one in B goes with B.
	if strings.Contains(a.CLI.MustOK(t, "namespace", "keys", "list").Stdout, "for-b") {
		if id := keyIDLine.FindStringSubmatch(res.Stdout); id != nil {
			t.Cleanup(func() { cliRevoke(t, a.CLI, id[1]) })
		}
		t.Fatalf("`keys create --namespace %s` minted the key in %s", b.Name, a.Name)
	}
}
