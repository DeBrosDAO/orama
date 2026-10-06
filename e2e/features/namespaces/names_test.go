//go:build e2e_fleet

package namespaces

import (
	"crypto/rand"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/nsledger"
)

// reservedNames are the platform's (create_handler.go reservedNamespaces;
// docs/SECURITY.md "platform names are reserved").
var reservedNames = []string{"default", "index", "nameserver", "system", "orama", "admin", "internal",
	"api", "www", "mail", "cdn", "docs", "status", "push", "turn", "ns1", "ns2", "ns3", "ns4"}

// invalidNames break the rule a name is held to: 2-40 of [a-z0-9-], not
// starting or ending with a hyphen, because it becomes a DNS label, a systemd
// instance name and a directory (create_handler.go namespaceName).
var invalidNames = []string{"", "a", "-ab", "ab-", "a_b", "a.b", "a/b", "../etc", "a b", "a\u0000b", "ñame",
	"a\u202eb", "a@b", "a\nb", strings.Repeat("a", 41), "--", "a%2fb", "ns-a.b", "é"}

func TestNamespaceName_invalidRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	owner := tenancy.Creator(t, f)
	for _, name := range invalidNames {
		c, resp := create(t, owner, name)
		adoptIfCreated(t, f, owner, c, resp)
		if resp.Status != http.StatusBadRequest || resp.ErrorCode() != codeName {
			t.Errorf("name %q: want 400 %s, got %d %.200s", name, codeName, resp.Status, resp.Body)
		}
	}
}

func TestNamespaceName_reservedRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	owner := tenancy.Creator(t, f)
	for _, name := range reservedNames {
		for _, spelled := range []string{name, strings.ToUpper(name), " " + name + " "} {
			c, resp := create(t, owner, spelled)
			adoptIfCreated(t, f, owner, c, resp)
			if resp.Status != http.StatusBadRequest || resp.ErrorCode() != codeName {
				t.Errorf("reserved %q: want 400 %s, got %d %.200s", spelled, codeName, resp.Status, resp.Body)
			}
		}
	}
}

// TestNamespaceName_boundariesAccepted: 40 characters and 2 characters are
// legal, and serve.
func TestNamespaceName_boundariesAccepted(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	long := ns.UniqueName(t.Name())
	long += strings.Repeat("x", 40-len(long))
	// The padded name is the one created, so it is the one the ledger must hold.
	if err := nsledger.RecordFromEnv(os.LookupEnv, long, time.Now()); err != nil {
		t.Fatalf("failed to record namespace %s in the ledger: %v", long, err)
	}
	short := strings.ToLower(rand.Text()[:2])
	tenancy.Reserve(t, f, 2)
	for _, name := range []string{long, short} {
		n := ns.New(t, f, ns.Options{Name: name})
		tenancy.Get(t, n.Client, "/health", tenancy.Cred{}).Expect(t, http.StatusOK)
	}
}

// TestNamespaceName_caseAndSpaceNormalised: the name is lowercased and trimmed
// before it is checked (create_handler.go), so "  E2E-X " creates "e2e-x".
func TestNamespaceName_caseAndSpaceNormalised(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	tenancy.Reserve(t, f, 1)
	owner := tenancy.Creator(t, f)
	name := ns.UniqueName(t.Name())
	c, resp := create(t, owner, "  "+strings.ToUpper(name)+" ")
	resp.Expect(t, http.StatusAccepted)
	if c.Name != name {
		t.Fatalf("created %q, want the normalised %q", c.Name, name)
	}
	n := tenancy.Adopt(t, f, owner, c)
	tenancy.Get(t, n.Client, "/health", tenancy.Cred{}).Expect(t, http.StatusOK)
	other := tenancy.Creator(t, f)
	c2, again := create(t, other, strings.ToUpper(name))
	adoptIfCreated(t, f, other, c2, again)
	if again.Status != http.StatusConflict || again.ErrorCode() != codeTaken {
		t.Fatalf("the upper-case spelling of an existing name: want 409 %s, got %d", codeTaken, again.Status)
	}
}
