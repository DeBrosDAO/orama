//go:build e2e_fleet

package gatewaymiddleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// queryResult is /v1/rqlite/query's answer (core/pkg/rqlite/gateway.go).
type queryResult struct {
	Items []map[string]any `json:"items"`
	Count int              `json:"count"`
}

// TestProxy_everyNodeServesTheNamespace: the namespace host reached through
// each node's own Caddy and cluster gateway lands on the same namespace: a
// row written through one node is read back through every other (the
// cluster gateway proxies ns-<ns> to the namespace's gateways over the mesh;
// website/src/docs/contributor/architecture-reference.mdx "HTTP Request Flow"). A credential of another
// namespace is refused on every node.
func TestProxy_everyNodeServesTheNamespace(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	nss := tenancy.Namespaces(t, f, 2, ns.Options{})
	a, b := nss[0], nss[1]
	nodes := f.State.Nodes
	first := a.Client.PinTo(nodes[0].PublicIP)
	for _, sql := range []string{"CREATE TABLE IF NOT EXISTS e2e_proxy (v TEXT)", "INSERT INTO e2e_proxy (v) VALUES ('" + a.Name + "')"} {
		tenancy.Post(t, first, "/v1/rqlite/exec", tenancy.Owner(a), map[string]any{"sql": sql, "args": []any{}}).Expect(t, http.StatusOK)
	}
	for _, n := range nodes {
		c := a.Client.PinTo(n.PublicIP)
		resp := tenancy.Post(t, c, "/v1/rqlite/query", tenancy.Owner(a), map[string]any{"sql": "SELECT v FROM e2e_proxy", "args": []any{}})
		resp.Expect(t, http.StatusOK)
		var r queryResult
		if err := json.Unmarshal(resp.Body, &r); err != nil {
			t.Fatal(err)
		}
		if r.Count != 1 || len(r.Items) != 1 || fmt.Sprint(r.Items[0]["v"]) != a.Name {
			t.Errorf("via %s: rows %v, want the one written via %s", n.Name, r.Items, nodes[0].Name)
		}
		other := tenancy.Post(t, c, "/v1/rqlite/query", tenancy.Owner(b), map[string]any{"sql": "SELECT 1"})
		tenancy.ExpectDenied(t, other, "via "+n.Name+", "+b.Name+"'s session on "+a.Name)
	}
}
