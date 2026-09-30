//go:build e2e_fleet

package dnstls

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// stampWindowSec steps outside the ±60s window a stamp is valid in
// (core/pkg/auth coordinationMaxSkew).
const stampWindowSec = 120

// TestACME_badStampsRefusedOnTheNode: a process on the node itself — a
// tenant's deployment is one — is refused without the right MAC: no header,
// a key no node holds, a stamp outside the window either way, and a stamp
// captured for one body replayed onto another (docs/SECURITY.md "ACME
// DNS-01": the MAC covers the body's hash). Loopback grants nothing.
func TestACME_badStampsRefusedOnTheNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	base := f.State.BaseDomain
	good := edge.ACMEBody(t, "_acme-challenge."+under(t, base)+".", edge.ChallengeValue(t))
	other := edge.ACMEBody(t, "_acme-challenge."+base+".", edge.ChallengeValue(t))
	cases := map[string]edge.ACMECall{
		"no MAC":        {Body: good, Key: edge.KeyNone},
		"foreign key":   {Body: good, Key: edge.KeyRandom},
		"stamp too old": {Body: good, Key: edge.KeyReal, SkewSec: -stampWindowSec},
		"stamp ahead":   {Body: good, Key: edge.KeyReal, SkewSec: stampWindowSec},
		"replayed body": {Body: other, SignedBody: good, Key: edge.KeyReal},
	}
	for name, call := range cases {
		for _, path := range []string{edge.ACMEPresent, edge.ACMECleanup} {
			call.Path = path
			if p := call.Run(t, f, n); p.Status != http.StatusNotFound {
				t.Errorf("%s on %s: %d, want 404: %.200s", name, path, p.Status, p.Body)
			}
		}
	}
}

// TestACME_badStampsRefusedOverTheOverlay: another node on the mesh is no
// more trusted than loopback: the source address is not consulted.
func TestACME_badStampsRefusedOverTheOverlay(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	from, to := f.State.Nodes[0], f.State.Nodes[1]
	body := edge.ACMEBody(t, "_acme-challenge."+under(t, f.State.BaseDomain)+".", edge.ChallengeValue(t))
	for _, key := range []string{edge.KeyNone, edge.KeyRandom} {
		call := edge.ACMECall{Path: edge.ACMEPresent, Body: body, Key: key, URL: edge.OverlayGateway(to, "")}
		if p := call.Run(t, f, from); p.Status != http.StatusNotFound {
			t.Errorf("%s -> %s over the overlay with key %s: %d, want 404: %.200s", from.Name, to.Name, key, p.Status, p.Body)
		}
	}
}

// TestACME_signedButOutOfScopeRefused: even a correctly signed call writes
// only an _acme-challenge record for the base domain or a name under it,
// whose value is a DNS-01 answer; anything else is 400 and nothing is written
// (docs/SECURITY.md "ACME DNS-01"; core/pkg/gateway/acme_auth.go).
func TestACME_signedButOutOfScopeRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	base := f.State.BaseDomain
	v := edge.ChallengeValue(t)
	bodies := map[string][]byte{
		"not a challenge name":     edge.ACMEBody(t, "www."+base+".", v),
		"the apex itself":          edge.ACMEBody(t, base+".", v),
		"another zone":             edge.ACMEBody(t, "_acme-challenge.example.com.", v),
		"suffix without a dot":     edge.ACMEBody(t, "_acme-challenge.evil"+base+".", v),
		"parent of the base":       edge.ACMEBody(t, "_acme-challenge."+parentOf(base)+".", v),
		"underscore label":         edge.ACMEBody(t, "_acme-challenge.a_b."+base+".", v),
		"empty label":              edge.ACMEBody(t, "_acme-challenge.."+base+".", v),
		"unicode label":            edge.ACMEBody(t, "_acme-challenge.ünï."+base+".", v),
		"NUL in the name":          edge.ACMEBody(t, "_acme-challenge.a\x00b."+base+".", v),
		"value too short":          edge.ACMEBody(t, "_acme-challenge."+base+".", v[:42]),
		"value too long":           edge.ACMEBody(t, "_acme-challenge."+base+".", v+"A"),
		"value not base64url":      edge.ACMEBody(t, "_acme-challenge."+base+".", "+"+v[1:]),
		"value with a quote":       edge.ACMEBody(t, "_acme-challenge."+base+".", `"`+v[1:]),
		"malformed JSON":           []byte(`{"fqdn":`),
		"JSON of the wrong type":   []byte(`["_acme-challenge.` + base + `."]`),
		"empty body":               {},
		"duplicate keys, last bad": []byte(`{"fqdn":"_acme-challenge.` + base + `.","value":"` + v + `","fqdn":"www.example.com."}`),
	}
	for name, body := range bodies {
		call := edge.ACMECall{Path: edge.ACMEPresent, Body: body, Key: edge.KeyReal}
		if p := call.Run(t, f, n); p.Status != http.StatusBadRequest {
			t.Errorf("signed present, %s: %d, want 400: %.200s", name, p.Status, p.Body)
			if p.Status == http.StatusOK {
				call.Path = edge.ACMECleanup
				call.Run(t, f, n)
			}
		}
	}
}

// TestACME_wrongMethod: GET is 405 before anything else is read.
func TestACME_wrongMethod(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	for _, path := range []string{edge.ACMEPresent, edge.ACMECleanup} {
		p := edge.NodeCurl{Method: http.MethodGet, URL: edge.LocalGateway(path)}.Run(t, f, n)
		if p.Status != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: %d, want 405: %.200s", path, p.Status, p.Body)
		}
	}
}

func parentOf(domain string) string {
	_, parent, _ := strings.Cut(domain, ".")
	return parent
}
