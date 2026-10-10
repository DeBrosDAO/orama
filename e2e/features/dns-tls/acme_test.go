//go:build e2e_fleet

package dnstls

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// propagationSlack is how long past a cache TTL a change may take to reach
// every nameserver: each CoreDNS reads its own node's rqlite.
const propagationSlack = 20 * time.Second

// ACME TXT records are written with TTL 60 (core/pkg/gateway/acme_handler.go).
const acmeTXTTTL = 60

// txtEverywhere reports whether every nameserver answers name/TXT with value
// (want) or answers it with no TXT record, NODATA (!want).
func txtEverywhere(ctx context.Context, servers []fleet.Node, name, value string, want bool) (bool, error) {
	for _, n := range servers {
		a, err := edge.Query(ctx, "udp", n.PublicIP, name, dnsmessage.TypeTXT)
		if err != nil {
			return false, err
		}
		has := slices.Contains(a.Values(dnsmessage.TypeTXT), value)
		if want && !has {
			return false, nil
		}
		if !want && (has || a.RCode != dnsmessage.RCodeSuccess || len(a.Answers) != 0) {
			return false, nil
		}
	}
	return true, nil
}

// TestACME_presentCleanupThroughTheNegativeCache: a challenge name has no TXT
// record, and is NODATA with the zone's SOA in the authority section (the
// base wildcard covers the name, so it exists); a signed present
// makes it resolve on every nameserver within the 30s negative-cache TTL (a
// negative answer is never served stale), and a signed cleanup removes it
// within the plugin's 30s cache (website/src/docs/operator/nameserver.mdx "A negative
// answer"; core/pkg/coredns/rqlite/cache.go NegativeTTL; docs/whitepaper/technical-reference/vol1/24-dns-and-nameservers.md
// "ACME DNS-01").
func TestACME_presentCleanupThroughTheNegativeCache(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	servers := edge.Nameservers(f)
	name := "_acme-challenge." + under(t, f.State.BaseDomain)
	value := edge.ChallengeValue(t)
	for _, n := range servers {
		requireNegative(t, n.Name, name, f.State.BaseDomain, ask(t, n.PublicIP, name, dnsmessage.TypeTXT))
	}
	cached := time.Now()
	caller := f.State.Nodes[0]
	body := edge.ACMEBody(t, name+".", value)
	t.Cleanup(func() { cleanupChallenge(t, f, caller, body) })
	if p := (edge.ACMECall{Path: edge.ACMEPresent, Body: body, Key: edge.KeyReal}).Run(t, f, caller); p.Status != http.StatusOK {
		t.Fatalf("a signed present answered %d: %s", p.Status, p.Body)
	}
	eventually.Require(t, time.Second, edge.NegativeTTL+propagationSlack, "the TXT record on every nameserver", func() (bool, error) {
		return txtEverywhere(t.Context(), servers, name, value, true)
	})
	if waited := time.Since(cached); waited > edge.NegativeTTL+propagationSlack {
		t.Errorf("the record took %s to replace a cached negative answer, want at most %s", waited, edge.NegativeTTL+propagationSlack)
	}
	a := ask(t, servers[0].PublicIP, name, dnsmessage.TypeTXT)
	requireTTL(t, servers[0].Name, name, a, dnsmessage.TypeTXT, acmeTXTTTL)
	if p := (edge.ACMECall{Path: edge.ACMECleanup, Body: body, Key: edge.KeyReal}).Run(t, f, caller); p.Status != http.StatusOK {
		t.Fatalf("a signed cleanup answered %d: %s", p.Status, p.Body)
	}
	eventually.Require(t, time.Second, edge.PluginCacheTTL+propagationSlack, "the TXT record gone from every nameserver", func() (bool, error) {
		return txtEverywhere(t.Context(), servers, name, value, false)
	})
}

func cleanupChallenge(t *testing.T, f *fleet.Fleet, n fleet.Node, body []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), fleet.CleanupBudget)
	defer cancel()
	call := edge.ACMECall{Path: edge.ACMECleanup, Body: body, Key: edge.KeyReal}
	out, err := f.SSH(ctx, n).Run(ctx, strings.TrimSpace(call.Command()))
	if err != nil || out.Exit != 0 || !strings.HasPrefix(out.Stdout, "STATUS 200") {
		t.Errorf("cleanup: removing the test's challenge record failed: %v %s %s", err, out.Stdout, f.Redact(out.Stderr))
	}
}

// TestACME_unsignedRefusedFromTheInternet: through Caddy the endpoints are
// reachable, and without a valid MAC they are 404 — they do not confirm they
// exist (core/pkg/gateway/acme_auth.go; docs/whitepaper/technical-reference/appendices/i-api-surface.md "Internal").
func TestACME_unsignedRefusedFromTheInternet(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	body := edge.ACMEBody(t, "_acme-challenge."+f.State.BaseDomain+".", edge.ChallengeValue(t))
	macs := []string{"", "1.00", "garbage", time.Now().Format("20060102") + "." + strings.Repeat("ab", 32)}
	for _, path := range []string{edge.ACMEPresent, edge.ACMECleanup} {
		for _, mac := range macs {
			h := http.Header{"Content-Type": {"application/json"}}
			if mac != "" {
				h.Set(edge.MACHeader, mac)
			}
			resp := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: path, Header: h, Body: body})
			if resp.Status != http.StatusNotFound {
				t.Errorf("%s with MAC %q from the internet: %d, want 404: %.200s", path, mac, resp.Status, resp.Body)
			}
		}
	}
}
