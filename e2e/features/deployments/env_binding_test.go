//go:build e2e_fleet

package deployments

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// boundEnvelopePrefix is the stored form of an environment sealed to its own
// namespace and deployment id (core/pkg/secrets/encrypt.go).
const boundEnvelopePrefix = "enc:v2:"

// storedEnvironment reads a deployment's environment column from the registry.
func (tn *tenant) storedEnvironment(t testing.TB, name string) string {
	t.Helper()
	q := infra.IndexQuery(t, tn.f, tn.f.State.Nodes[0],
		"SELECT environment FROM deployments WHERE namespace = ? AND name = ?", tn.n.Name, name)
	if q.Error != "" || len(q.Values) != 1 || len(q.Values[0]) != 1 {
		t.Fatalf("the registry's row for %s/%s: %+v", tn.n.Name, name, q)
	}
	stored, _ := q.Values[0][0].(string)
	return stored
}

// TestDeployEnv_boundToItsRowAfterRotateSecrets: once the operator has run
// `orama maint operator rotate-secrets` (docs/whitepaper/technical-reference/appendices/d-cli-reference.md), a deployment's
// environment is stored sealed to its namespace and deployment id, still
// reaches the running app, and a later change to it is stored bound too
// (docs/whitepaper/technical-reference/vol1/11-app-deployments.md "Deployment environment").
func TestDeployEnv_boundToItsRowAfterRotateSecrets(t *testing.T) {
	tn := newTenant(t)
	secret := marker(t)
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "bound"), "bound", "--env", "SECRET="+secret)
	serving(t, tn.app(u), "/health", "")

	harness.CLI(t).MustOK(t, "maint", "operator", "rotate-secrets")

	if stored := tn.storedEnvironment(t, "bound"); !strings.HasPrefix(stored, boundEnvelopePrefix) {
		t.Fatalf("after rotate-secrets the environment is stored as %.12s..., want the %s envelope", stored, boundEnvelopePrefix)
	}

	// A change restarts the app, which reads the bound row back.
	after := marker(t)
	tn.api(t, http.MethodPost, pathEnvSet+"?name=bound", map[string]any{"set": map[string]string{"AFTER": after}}).Expect(t, http.StatusOK)
	tn.waitEnv(t, u, "AFTER", after, true)
	tn.waitEnv(t, u, "SECRET", secret, true)

	if stored := tn.storedEnvironment(t, "bound"); !strings.HasPrefix(stored, boundEnvelopePrefix) {
		t.Fatalf("after an environment change the row is stored as %.12s..., want the %s envelope", stored, boundEnvelopePrefix)
	}
}

// TestDeployHost_bareNameOfASubdomainedDeploymentIsNotServed: a deployment is
// served at its {name}-{random} address. The bare {name}.<base> host belongs
// only to a deployment from before subdomains existed, so deploying a name in
// two namespaces leaves the bare host answering neither
// (website/src/docs/developer/deployments.mdx "Deployment addresses").
func TestDeployHost_bareNameOfASubdomainedDeploymentIsNotServed(t *testing.T) {
	const name = "twin"
	tenants := newTenants(t, 2)
	first, second := tenants[0], tenants[1]
	urls := map[*tenant]string{}
	for _, tn := range []*tenant{first, second} {
		urls[tn] = tn.deploy(t, "go", tenancy.WriteProbeApp(t, name), name)
		serving(t, tn.app(urls[tn]), "/health", "")
	}

	parsed, err := url.Parse(urls[first])
	if err != nil {
		t.Fatal(err)
	}
	_, base, ok := strings.Cut(parsed.Host, ".")
	if !ok {
		t.Fatalf("the app address %s has no base domain", urls[first])
	}
	bare := fmt.Sprintf("%s://%s.%s", parsed.Scheme, name, base)
	r, err := first.app(bare).Send(t.Context(), gw.Req{Path: "/health"})
	if err != nil {
		t.Fatalf("request to %s: %v", bare, err)
	}
	if r.Status != http.StatusNotFound {
		t.Fatalf("%s answered %d %.120s, want 404: two namespaces share the name", bare, r.Status, r.Body)
	}
}
