//go:build e2e_fleet

package authclusteradmin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	pathJWKS = "/v1/auth/jwks"
	// keyReloadBudget covers the 30-second signing-key reload on every
	// gateway (core/pkg/gateway/auth/signing_keys.go) plus a round trip.
	keyReloadBudget = 45 * time.Second
	// overlapText is how long the outgoing key keeps verifying: one access
	// token lifetime (docs/whitepaper/technical-reference/vol1/13-identity.md#key-rotation).
	overlapText = "15m0s"
	// freshPollEvery paces the wait for a gateway to sign with the new key:
	// each poll signs in once per gateway (two credential operations), so a
	// poll every 10s spends 12 a minute of each gateway's 30-a-minute bucket.
	freshPollEvery = 10 * time.Second
	// freshBudget covers the 30-second reload plus a slow node, at that pace.
	freshBudget = 90 * time.Second
)

var (
	newKeyLine  = regexp.MustCompile(`New key:\s+(\S+)`)
	prevKeyLine = regexp.MustCompile(`Previous key:\s+(\S+)`)
	edKid       = regexp.MustCompile(`^ed_[0-9a-f]{16}$`)
)

// kidOf reads the kid a token's header names.
func kidOf(t testing.TB, token string) string {
	t.Helper()
	head, _, ok := strings.Cut(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(head)
	if !ok || err != nil {
		t.Fatalf("not a JWT: %v", err)
	}
	var h struct{ Kid string }
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	return h.Kid
}

func publishedKids(t testing.TB, c *gw.Client) map[string]bool {
	t.Helper()
	var set struct{ Keys []struct{ Kid string } }
	if err := c.MustSend(t, gw.Req{Path: pathJWKS}).Expect(t, http.StatusOK).Decode(&set); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, k := range set.Keys {
		out[k.Kid] = true
	}
	return out
}

// TestRotateSigningKey_oneLifetimeOverlap: rotating publishes a new key and
// signs with it, while the outgoing key keeps verifying what it signed: two
// kids in flight, nobody signed out (docs/whitepaper/technical-reference/vol1/13-identity.md#upkeep).
// A key cannot be un-rotated; the new key is simply the cluster's key from
// here on, which is what an operator's rotation leaves too.
func TestRotateSigningKey_oneLifetimeOverlap(t *testing.T) {
	f := harness.Fleet(t)
	nodes := perNode(t, f, harness.GW(t))
	held := map[string]string{} // token -> kid
	for _, nc := range nodes {
		tok := lobbyToken(t, nc.Client, newWallet(t))
		held[tok] = kidOf(t, tok)
	}
	out := harness.CLI(t).MustOK(t, "operator", "rotate-signing-key").Stdout
	nm, pm := newKeyLine.FindStringSubmatch(out), prevKeyLine.FindStringSubmatch(out)
	if nm == nil || pm == nil || !strings.Contains(out, overlapText) {
		t.Fatalf("rotate-signing-key output lacks the new key, the previous key or the %s overlap:\n%s", overlapText, out)
	}
	newKid, prevKid := nm[1], pm[1]
	if newKid == prevKid || !edKid.MatchString(newKid) || !edKid.MatchString(prevKid) {
		t.Fatalf("rotation reported new %q previous %q", newKid, prevKid)
	}
	// The rotation may land on another gateway than the JWKS read: every
	// gateway publishes the new kid within one signing-key reload.
	for _, nc := range nodes {
		eventually.Require(t, pollEvery, keyReloadBudget, nc.Node.Name+" to publish both kids", func() (bool, error) {
			kids := publishedKids(t, nc.Client)
			if !kids[newKid] || !kids[prevKid] {
				return false, fmt.Errorf("JWKS carries new %v previous %v", kids[newKid], kids[prevKid])
			}
			return true, nil
		})
	}
	found := false
	for _, kid := range held {
		found = found || kid == prevKid
	}
	if !found {
		t.Errorf("the previous key %s signed none of the tokens held from each gateway (%v)", prevKid, held)
	}
	for tok := range held {
		for _, nc := range nodes {
			if resp := nc.Client.MustSend(t, gw.Req{Path: gw.PathWhoami, Bearer: tok}); resp.Status != http.StatusOK {
				t.Errorf("%s refused a token signed before the rotation (kid %s): %d", nc.Node.Name, held[tok], resp.Status)
			}
		}
	}
	freshVerifiesEverywhere(t, nodes, newKid)
}

// freshVerifiesEverywhere waits until some gateway signs with newKid and
// every gateway accepts that token.
func freshVerifiesEverywhere(t testing.TB, nodes []nodeClient, newKid string) {
	t.Helper()
	var fresh string
	eventually.Require(t, freshPollEvery, freshBudget, "a gateway to sign with the new key", func() (bool, error) {
		for _, nc := range nodes {
			tok := lobbyToken(t, nc.Client, newWallet(t))
			if kidOf(t, tok) == newKid {
				fresh = tok
				return true, nil
			}
		}
		return false, fmt.Errorf("no gateway signs with %s yet", newKid)
	})
	for _, nc := range nodes {
		eventually.Require(t, pollEvery, keyReloadBudget, nc.Node.Name+" to accept the new key", func() (bool, error) {
			resp := nc.Client.MustSend(t, gw.Req{Path: gw.PathWhoami, Bearer: fresh})
			if resp.Status == http.StatusOK {
				return true, nil
			}
			return false, fmt.Errorf("HTTP %d", resp.Status)
		})
	}
}
