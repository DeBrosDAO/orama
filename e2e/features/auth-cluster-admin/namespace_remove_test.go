//go:build e2e_fleet

package authclusteradmin

import (
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	pathOperatorRemove = "/v1/operator/namespaces/remove"
	exitUsage          = infra.ExitUsage
)

// TestNamespaceRemove_operatorRemovesAnOrphan: a namespace whose owner's
// wallet is gone could never be deleted; an operator removes it with a reason
// and it is torn down on every node. Its owner cannot use the operator route
// (docs/whitepaper/technical-reference/appendices/i-api-surface.md "/v1/operator/namespaces/remove",
// docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama cluster namespace remove").
func TestNamespaceRemove_operatorRemovesAnOrphan(t *testing.T) {
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})

	// The owner holds the namespace but is not an operator.
	resp := postJSON(t, n.Owner.Client, pathOperatorRemove, n.Owner.Token(),
		map[string]string{"namespace": n.Name, "reason": "e2e: not an operator"})
	if resp.Status != http.StatusForbidden {
		t.Fatalf("the owner reached the operator removal: HTTP %d %s", resp.Status, resp.Body)
	}

	cli := harness.CLI(t)
	if res := cli.MustOK(t, "cluster", "namespace", "remove", n.Name, "--reason", "e2e: orphan removal", "--force"); res.Exit != 0 {
		t.Fatalf("remove exited %d", res.Exit)
	}
	n.MarkRemoved(t)
}

// TestNamespaceRemove_refusals: no reason, the lobby and a reserved name are
// refused before anything is touched; the CLI requires --reason.
func TestNamespaceRemove_refusals(t *testing.T) {
	cli := harness.CLI(t)
	res, err := cli.For(t).Run(t.Context(), "cluster", "namespace", "remove", "default", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != exitUsage {
		t.Errorf("remove without --reason: exit %d, want %d", res.Exit, exitUsage)
	}
	for _, name := range []string{"default", "index"} {
		res, err := cli.For(t).Run(t.Context(), "cluster", "namespace", "remove", name, "--reason", "e2e", "--force")
		if err != nil {
			t.Fatal(err)
		}
		if res.Exit == 0 {
			t.Errorf("removing %s succeeded", name)
		}
	}
}
