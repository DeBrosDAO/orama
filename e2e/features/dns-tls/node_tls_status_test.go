//go:build e2e_fleet

package dnstls

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// tlsStatus is `orama node tls status --json`
// (core/cmd/orama/internal/production/tlsstatus Report).
type tlsStatus struct {
	Unit  string `json:"unit"`
	CA    string `json:"ca"`
	Names []struct {
		Name       string    `json:"name"`
		Held       bool      `json:"held"`
		Issuer     string    `json:"issuer"`
		NotAfter   time.Time `json:"not_after"`
		ServeError string    `json:"serve_error"`
		LastError  *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"last_error"`
	} `json:"names"`
}

// TestNodeTLSStatus_reportsTheHeldCertificates: on every node `orama node tls
// status --json` names the CA the cluster issues from and lists the base domain
// and its wildcard, each held, issued by the CA the run uses (staging on a
// fleet the run provisioned, production on stagenet) and far from expiry; the
// text form lists the same names (website/src/docs/operator/tls-certificates.mdx
// "Reading a node's certificate status").
func TestNodeTLSStatus_reportsTheHeldCertificates(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	base := f.State.BaseDomain
	wantCA := constants.LetsEncryptProductionACME
	if f.State.StagingCerts() {
		wantCA = constants.LetsEncryptStagingACME
	}
	for _, n := range f.State.Nodes {
		out := infra.OnNode(t, f, n, "node", "tls", "status", "--json")
		infra.ExpectNodeExit(t, n.Name+": orama node tls status --json", out, infra.ExitOK)
		var st tlsStatus
		if err := json.Unmarshal([]byte(out.Stdout), &st); err != nil {
			t.Fatalf("%s: %v\n%s", n.Name, err, out.Stdout)
		}
		if st.CA != wantCA {
			t.Errorf("%s: the CA is %q, want %q", n.Name, st.CA, wantCA)
		}
		if !strings.HasPrefix(st.Unit, "orama-namespace-caddy@") {
			t.Errorf("%s: the unit is %q", n.Name, st.Unit)
		}
		clusterNames := []string{base, "*." + base}
		var names []string
		for _, c := range st.Names {
			names = append(names, c.Name)
			if !slices.Contains(clusterNames, c.Name) {
				continue
			}
			if !c.Held || c.ServeError != "" {
				t.Errorf("%s: %s is not held: %+v", n.Name, c.Name, c)
			}
			if staging := strings.HasPrefix(c.Issuer, stagingIssuerPrefix); staging != f.State.StagingCerts() {
				t.Errorf("%s: %s issued by %q; want a staging intermediate: %v", n.Name, c.Name, c.Issuer, f.State.StagingCerts())
			}
			if left := time.Until(c.NotAfter); left < certMinLeft(t) {
				t.Errorf("%s: %s expires in %s, want more than %s", n.Name, c.Name, left.Round(time.Hour), certMinLeft(t))
			}
		}
		for _, want := range clusterNames {
			if !slices.Contains(names, want) {
				t.Errorf("%s: the status lists %v, want %s", n.Name, names, want)
			}
		}
		text := infra.OnNode(t, f, n, "node", "tls", "status")
		infra.ExpectNodeExit(t, n.Name+": orama node tls status", text, infra.ExitOK, "Certificate authority: "+wantCA, base, "held")
	}
}

// TestNodeTLSStatus_needsRoot: the journal of Caddy is root's to read, so the
// command refuses anyone else instead of reporting a log it could not see.
func TestNodeTLSStatus_needsRoot(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	out := infra.OnNodeUnprivileged(t, f, n, "node", "tls", "status")
	infra.ExpectNodeExit(t, n.Name+": orama node tls status as "+infra.Unprivileged, out, infra.ExitUsage, infra.MustBeRoot)
}
