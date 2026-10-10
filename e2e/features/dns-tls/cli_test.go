//go:build e2e_fleet

package dnstls

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const tlsCheckPath = "/v1/internal/tls/check"

// delegation is `orama node dns delegation --json`
// (core/cmd/orama/internal/production/dnsdelegation Delegation).
type delegation struct {
	Domain      string `json:"domain"`
	Nameservers []struct {
		Hostname string `json:"hostname"`
		IP       string `json:"ip"`
	} `json:"nameservers"`
}

// TestDelegation_jsonMatchesTheLiveZone: the delegation the CLI reads over
// SSH lists one slot per nameserver, and each slot's address is exactly what
// the zone's own glue answers — the records the parent zone must carry
// (website/src/docs/operator/nameserver.mdx "Seeing which address holds which slot").
func TestDelegation_jsonMatchesTheLiveZone(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	res := harness.CLI(t).MustOK(t, "node", "dns", "delegation", "--env", f.State.Env, "--json")
	var ds []delegation
	if err := oramacli.DecodeJSON(res, &ds); err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].Domain != f.State.BaseDomain {
		t.Fatalf("delegation lists %+v, want exactly the base domain %s", ds, f.State.BaseDomain)
	}
	srv := edge.Nameservers(f)[0]
	var ips []string
	for i, n := range ds[0].Nameservers {
		if want := fmt.Sprintf("ns%d", i+1); n.Hostname != want {
			t.Errorf("slot %d is %q, want %s (slots in numeric order)", i, n.Hostname, want)
		}
		glue := ask(t, srv.PublicIP, n.Hostname+"."+f.State.BaseDomain, dnsmessage.TypeA).Values(dnsmessage.TypeA)
		if !slices.Equal(glue, []string{n.IP}) {
			t.Errorf("%s: delegation says %s, the zone's glue says %v", n.Hostname, n.IP, glue)
		}
		ips = append(ips, n.IP)
	}
	if !slices.Equal(sorted(ips), publicIPs(edge.Nameservers(f))) {
		t.Errorf("delegation names %v, want every nameserver %v", ips, publicIPs(edge.Nameservers(f)))
	}
}

// TestDelegation_jsonCarriesTheDNSVerdict: after the records the command asks
// DNS about them. The verdict is delegated, or a list of the records that are
// missing or point elsewhere, or a check error when the resolver could not
// answer; never an empty verdict that is not delegated (website/src/docs/operator/nameserver.mdx
// "Seeing which address holds which slot").
func TestDelegation_jsonCarriesTheDNSVerdict(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	res := harness.CLI(t).MustOK(t, "node", "dns", "delegation", "--env", f.State.Env, "--json")
	var reports []struct {
		Domain     string `json:"domain"`
		Delegated  bool   `json:"delegated"`
		CheckError string `json:"check_error"`
		Findings   []struct {
			Kind   string `json:"kind"`
			Record string `json:"record"`
			Want   string `json:"want"`
		} `json:"findings"`
	}
	if err := oramacli.DecodeJSON(res, &reports); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("%d reports, want one for the base domain", len(reports))
	}
	r := reports[0]
	switch {
	case r.Delegated && (len(r.Findings) != 0 || r.CheckError != ""):
		t.Errorf("delegated with findings %v and check error %q", r.Findings, r.CheckError)
	case !r.Delegated && r.CheckError == "" && len(r.Findings) == 0:
		t.Errorf("%s is neither delegated nor explained", r.Domain)
	}
	for _, fd := range r.Findings {
		if fd.Kind == "" || fd.Record == "" || fd.Want == "" {
			t.Errorf("finding %+v lacks its kind, record or expected value", fd)
		}
	}
}

// TestDelegation_textIsZoneFileRecords: without --json the command prints a
// comment naming the parent zone, then one NS line per slot and one glue A
// line per slot, in zone-file form (website/src/docs/operator/nameserver.mdx).
func TestDelegation_textIsZoneFileRecords(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	out := harness.CLI(t).MustOK(t, "node", "dns", "delegation", "--env", f.State.Env).Stdout
	apex := edge.Fqdn(f.State.BaseDomain)
	if !strings.HasPrefix(out, "; "+f.State.BaseDomain+" — create these in the parent zone "+parentOf(f.State.BaseDomain)) {
		t.Errorf("the output does not start with the parent-zone comment:\n%s", out)
	}
	ns, a := 0, 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 4 && fields[0] == apex && fields[1] == "IN" && fields[2] == "NS" && strings.HasSuffix(fields[3], "."+apex):
			ns++
		case len(fields) == 4 && fields[1] == "IN" && fields[2] == "A" && strings.HasSuffix(fields[0], "."+apex):
			a++
		}
	}
	if want := len(edge.Nameservers(f)); ns != want || a != want {
		t.Errorf("%d NS and %d glue lines, want %d of each:\n%s", ns, a, want, out)
	}
}

// TestDelegation_refusals: --env is required (usage, exit 2), and an
// environment the CLI does not know is an error, not an empty delegation.
func TestDelegation_refusals(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	infra.ExpectExit(t, infra.Run(t, cli, "node", "dns", "delegation"), infra.ExitUsage, "--env is required")
	res := infra.Run(t, cli, "node", "dns", "delegation", "--env", "e2e-no-such-env")
	if res.Exit == infra.ExitOK || strings.Contains(res.Stdout, " IN ") {
		t.Fatalf("an unknown environment printed a delegation (exit %d):\n%s", res.Exit, res.Stdout)
	}
	infra.ExpectExit(t, infra.Run(t, cli, "node", "dns"), infra.ExitOK, "delegation")
}

// TestTLSCheck_onlyBaseAndItsSubdomains: the check Caddy asks before it
// would issue on demand allows the base domain and names under it and
// nothing else — no custom domain gets a certificate (website/src/docs/contributor/architecture-reference.mdx
// "TLS/HTTPS"; core/pkg/gateway/status_handlers.go tlsCheckHandler). Asked
// from a node shell, where Caddy asks it.
func TestTLSCheck_onlyBaseAndItsSubdomains(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	base := f.State.BaseDomain
	cases := map[string]int{
		base: http.StatusOK, "app." + base: http.StatusOK, "a.b.c." + base: http.StatusOK,
		"example.com": http.StatusForbidden, "evil" + base: http.StatusForbidden, base + ".evil.com": http.StatusForbidden,
		parentOf(base): http.StatusForbidden, "": http.StatusBadRequest,
	}
	for domain, want := range cases {
		p := edge.NodeCurl{URL: edge.LocalGateway(tlsCheckPath + "?domain=" + url.QueryEscape(domain))}.Run(t, f, n)
		if p.Status != want {
			t.Errorf("tls/check?domain=%q: %d, want %d: %.200s", domain, p.Status, want, p.Body)
		}
	}
}

// TestTLSCheck_reachableFromTheInternet records a documentation gap: the
// route is policyOpen and has no source check, so it answers through Caddy
// although docs/whitepaper/technical-reference/appendices/i-api-surface.md calls it "Never reachable by a client". What
// it leaks is only whether a name is under the base domain; the answer is
// the same as on the node.
func TestTLSCheck_reachableFromTheInternet(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	yes := c.MustSend(t, gw.Req{Path: tlsCheckPath, Query: url.Values{"domain": {"app." + f.State.BaseDomain}}})
	no := c.MustSend(t, gw.Req{Path: tlsCheckPath, Query: url.Values{"domain": {"example.com"}}})
	if yes.Status != http.StatusOK || no.Status != http.StatusForbidden {
		t.Fatalf("from the internet: own name %d, foreign %d; want 200 and 403 (or update this test with the doc)", yes.Status, no.Status)
	}
}

// TestCustomDomain_baseDomainRefused: a tenant cannot claim the base domain
// or a name under it as a custom domain, in any spelling, before any
// deployment lookup (website/src/docs/operator/monitoring.mdx "Public status page": a custom domain
// equal to the base domain is refused; core/pkg/gateway/handlers/deployments
// domain_handler.go).
func TestCustomDomain_baseDomainRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	base := f.State.BaseDomain
	for _, domain := range []string{base, "app." + base, strings.ToUpper(base), "https://" + base + "/", " www." + base + " "} {
		resp := tenancy.Post(t, n.Client, "/v1/deployments/domains/add", tenancy.Owner(n),
			map[string]string{"deployment_name": "e2e-none", "domain": domain})
		if resp.Status != http.StatusBadRequest || !strings.Contains(string(resp.Body), "Cannot use ."+base) {
			t.Errorf("custom domain %q: %d %.200s, want 400 naming the base domain", domain, resp.Status, resp.Body)
		}
	}
}
