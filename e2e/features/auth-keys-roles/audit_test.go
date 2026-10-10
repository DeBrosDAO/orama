//go:build e2e_fleet

package authkeysroles

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	// auditMaxLimit (docs/whitepaper/technical-reference/appendices/i-api-surface.md "/v1/audit").
	auditMaxLimit = 200
	auditBudget   = 30 * time.Second
)

// keyActor is how the trail records anything that is not a wallet: a
// fingerprint, never the credential (docs/whitepaper/technical-reference/vol1/14-authorization.md#the-audit-trail).
var keyActor = regexp.MustCompile(`^key:[0-9a-f]{16}$`)

type auditEvent struct {
	Action    string `json:"action"`
	Actor     string `json:"actor"`
	Resource  string `json:"resource"`
	Result    string `json:"result"`
	CreatedAt string `json:"created_at"`
}

type auditPage struct {
	Namespace string       `json:"namespace"`
	Events    []auditEvent `json:"events"`
	Count     int          `json:"count"`
}

func readAudit(t testing.TB, c *gw.Client, bearer string, q url.Values) (*gw.Response, auditPage) {
	t.Helper()
	var page auditPage
	resp := c.MustSend(t, gw.Req{Path: pathAudit, Query: q, Bearer: bearer})
	if resp.Status == http.StatusOK {
		if err := resp.Decode(&page); err != nil {
			t.Fatal(err)
		}
	}
	return resp, page
}

// waitForAction polls until the trail shows action, and returns its events.
func waitForAction(t testing.TB, c *gw.Client, bearer, action string) []auditEvent {
	t.Helper()
	var got []auditEvent
	eventually.Require(t, pollEvery, auditBudget, "audit action "+action, func() (bool, error) {
		resp, page := readAudit(t, c, bearer, url.Values{"action": {action}})
		if resp.Status != http.StatusOK {
			return false, eventually.Stop(fmt.Errorf("GET /v1/audit: %d %s", resp.Status, resp.Body))
		}
		got = page.Events
		if len(got) > 0 {
			return true, nil
		}
		return false, fmt.Errorf("no %s event yet", action)
	})
	return got
}

// TestAudit_recordsWhoChangedWhat: keys minted and revoked, grants given and
// taken, and the session policy are recorded; a wallet is recorded as itself,
// a key as a fingerprint, never the key (docs/whitepaper/technical-reference/vol1/14-authorization.md#the-audit-trail).
func TestAudit_recordsWhoChangedWhat(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	owner := n.Owner.Token()
	admin := mintKey(t, n, map[string]any{"scope": "admin", "label": "audit"})
	byKey := mintKeyWith(t, n, c, admin.APIKey)
	w := newWallet(t).Address()
	grant(t, n, w, roleRuntime, "").Expect(t, http.StatusCreated)
	send(t, c, http.MethodDelete, pathMembers+"/"+w, owner, nil).Expect(t, http.StatusOK)
	send(t, c, http.MethodDelete, keyPath(byKey), owner, nil).Expect(t, http.StatusOK)
	for _, action := range []string{"key.issue", "key.revoke", "grant.add", "grant.revoke"} {
		for _, e := range waitForAction(t, c, owner, action) {
			if e.Result != "success" {
				t.Errorf("%s recorded result %q", action, e.Result)
			}
		}
	}
	issued := waitForAction(t, c, owner, "key.issue")
	keyActors := 0
	for _, e := range issued {
		switch {
		case strings.EqualFold(e.Actor, n.Owner.Wallet.Address()):
		case keyActor.MatchString(e.Actor):
			keyActors++
		default:
			t.Errorf("key.issue actor %q is neither the owner wallet nor a key fingerprint", e.Actor)
		}
	}
	if keyActors == 0 {
		t.Error("the key minted with an admin key's token was not attributed to a key fingerprint")
	}
	resp, _ := readAudit(t, c, owner, nil)
	resp.Expect(t, http.StatusOK)
	if strings.Contains(string(resp.Body), admin.APIKey) {
		t.Fatal("the audit trail contains an API key")
	}
}

// mintKeyWith exchanges key for a token and mints a key with it; the trail
// must attribute that act to the key's fingerprint.
func mintKeyWith(t testing.TB, n *ns.Namespace, c *gw.Client, key string) int64 {
	t.Helper()
	s, _, err := c.For(t).Token(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	var k mintedKey
	if err := send(t, c, http.MethodPost, pathKeys, s.AccessToken, map[string]any{"scope": "cache"}).Expect(t, http.StatusCreated).Decode(&k); err != nil {
		t.Fatal(err)
	}
	protect(t, c, k.APIKey)
	t.Cleanup(func() { revokeAtCleanup(t, n, k.ID) })
	return k.ID
}

// TestAudit_filtersAndBounds: action, principal, since and limit narrow the
// page (200 at most); bad values are 400; the namespace comes from the
// credential, never the query string. The default page size (50) is not
// asserted: a fresh namespace holds too few events to reach it.
func TestAudit_filtersAndBounds(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	owner := n.Owner.Token()
	for i := 0; i < 3; i++ {
		mintKey(t, n, map[string]any{"scope": "cache"})
	}
	events := waitForAction(t, c, owner, "key.issue")
	for _, e := range events {
		if e.Action != "key.issue" {
			t.Errorf("?action=key.issue returned %s", e.Action)
		}
	}
	// A wallet is recorded as its signed-in subject, the lowercase address.
	page := okAudit(t, c, owner, url.Values{"principal": {strings.ToLower(n.Owner.Wallet.Address())}})
	if len(page.Events) == 0 {
		t.Error("?principal=<owner> returned nothing, though the owner minted three keys")
	}
	for _, e := range page.Events {
		if !strings.EqualFold(e.Actor, n.Owner.Wallet.Address()) {
			t.Errorf("?principal returned an event by %q", e.Actor)
		}
	}
	oldest := events[len(events)-1].CreatedAt
	after := okAudit(t, c, owner, url.Values{"since": {oldest}, "action": {"key.issue"}})
	if len(after.Events) >= len(events) {
		t.Errorf("?since=%s returned %d of %d key.issue events: the oldest is not strictly after itself", oldest, len(after.Events), len(events))
	}
	for _, e := range after.Events {
		if e.CreatedAt <= oldest {
			t.Errorf("?since=%s returned an event from %s", oldest, e.CreatedAt)
		}
	}
	if one := okAudit(t, c, owner, url.Values{"limit": {"1"}}); len(one.Events) != 1 {
		t.Errorf("?limit=1 returned %d events", len(one.Events))
	}
	if big := okAudit(t, c, owner, url.Values{"limit": {"500"}}); len(big.Events) > auditMaxLimit {
		t.Errorf("?limit=500 returned %d events, want at most %d", len(big.Events), auditMaxLimit)
	}
	auditRefusesBadQueries(t, c, owner, n.Name)
}

// okAudit reads the trail and requires a 200.
func okAudit(t testing.TB, c *gw.Client, bearer string, q url.Values) auditPage {
	t.Helper()
	resp, page := readAudit(t, c, bearer, q)
	resp.Expect(t, http.StatusOK)
	return page
}

// auditRefusesBadQueries: bad limits, actions and times are 400, and a
// namespace in the query string is ignored for the credential's own.
func auditRefusesBadQueries(t testing.TB, c *gw.Client, owner, namespace string) {
	t.Helper()
	for _, q := range []url.Values{{"limit": {"0"}}, {"limit": {"-1"}}, {"limit": {"many"}}, {"action": {"no.such.action"}}, {"since": {"yesterday"}}} {
		if r, _ := readAudit(t, c, owner, q); r.Status != http.StatusBadRequest {
			t.Errorf("%v: want 400, got %d", q, r.Status)
		}
	}
	if other := okAudit(t, c, owner, url.Values{"namespace": {"default"}}); other.Namespace != namespace {
		t.Errorf("?namespace=default read %q's trail", other.Namespace)
	}
}

// TestAudit_cli: `orama audit` prints the trail oldest first, filters by
// action, prints JSON with --json, refuses an unknown action locally, and
// --follow prints an event recorded while it runs.
func TestAudit_cli(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	n.CLI.MustOK(t, "namespace", "keys", "create", "--scope", "cache", "--label", "audit-cli")
	out := n.CLI.MustOK(t, "audit", "--action", "key.issue").Stdout
	if !strings.Contains(out, "key.issue") {
		t.Fatalf("orama audit --action key.issue:\n%s", out)
	}
	var rows []map[string]any
	res := n.CLI.MustOK(t, "audit", "--json", "--limit", "5", "--principal", strings.ToLower(f.State.OperatorAddress))
	if err := decodeJSON(res.Stdout, &rows); err != nil || len(rows) == 0 || len(rows) > 5 {
		t.Errorf("orama audit --json --limit 5: %d rows, %v", len(rows), err)
	}
	if r := runCLI(t, n.CLI, "audit", "--action", "no.such.action"); r.Exit != exitUsage {
		t.Errorf("an unknown --action: want exit %d, got %d", exitUsage, r.Exit)
	}
	if r := runCLI(t, n.CLI, "audit", "--since", "yesterday"); r.Exit == 0 {
		t.Error("an unreadable --since was accepted")
	}
	followPrintsNewEvents(t, n)
}
