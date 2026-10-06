//go:build e2e_fleet

package invitejoin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	// unusedPublicIP is a public address no node of the run has (TEST-NET-3).
	unusedPublicIP = "203.0.113.77"
	// shortExpiry is the life of the invite the expiry test waits out.
	shortExpiry = 5 * time.Second
	// expiryPoll paces the wait for the cluster to judge the invite expired.
	expiryPoll = time.Second
	// concurrentJoins is how many identical joins race.
	concurrentJoins = 10
)

func expectRefusal(t testing.TB, status int, body []byte, wantStatus int, want string) {
	t.Helper()
	if status != wantStatus || !strings.Contains(string(body), want) {
		t.Errorf("join: HTTP %d %.300q, want %d containing %q", status, body, wantStatus, want)
	}
}

// TestJoin_unknownTokenRefused: a token no invite matches is 401 "no invite
// matches this token", and changes nothing.
func TestJoin_unknownTokenRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	infra.ForgetPhantomOnCleanup(t, unusedPublicIP)
	for _, tok := range []string{strings.Repeat("ab", 32), "' OR 1=1 --", strings.Repeat("0", 64), "\u202e" + strings.Repeat("f", 63)} {
		r := infra.Join(t, f.State.Nodes[0], infra.JoinBody{Token: tok, WGPublicKey: infra.NewWGKey(t), PublicIP: unusedPublicIP})
		expectRefusal(t, r.Status, r.Body, http.StatusUnauthorized, infra.JoinUnknown)
	}
}

// TestJoin_malformedRequestsRefused: the join validates its body before it
// looks at the token: missing fields, a non-IPv4 address, a key that is not
// 32 bytes of base64, a wrong method and a body that is not JSON are all
// refused without a token lookup (core/pkg/gateway/handlers/join/handler.go).
func TestJoin_malformedRequestsRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	infra.ForgetPhantomOnCleanup(t, unusedPublicIP)
	n := f.State.Nodes[0]
	key := infra.NewWGKey(t)
	tok := strings.Repeat("cd", 32)
	cases := []struct {
		raw  string
		want string
	}{
		{`{}`, "are required"},
		{`not json`, "invalid request body"},
		{fmt.Sprintf(`{"token":%q,"wg_public_key":%q,"public_ip":"::1"}`, tok, key), "public_ip"},
		{fmt.Sprintf(`{"token":%q,"wg_public_key":"AAAA","public_ip":%q}`, tok, unusedPublicIP), "wg_public_key"},
		{fmt.Sprintf(`{"token":%q,"wg_public_key":"a\nb=","public_ip":%q}`, tok, unusedPublicIP), "wg_public_key"},
		{fmt.Sprintf(`{"token":%q,"wg_public_key":%q,"public_ip":%q,"peer_id":"not-a-peer"}`, tok, key, unusedPublicIP), "peer_id"},
	}
	for _, c := range cases {
		r := infra.PostJoin(t, n, []byte(c.raw))
		expectRefusal(t, r.Status, r.Body, http.StatusBadRequest, c.want)
	}
	c := harness.GW(t).PinTo(n.PublicIP)
	if r := c.MustSend(t, reqGet()); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("GET %s: HTTP %d, want 405", infra.JoinPath, r.Status)
	}
}

// TestJoin_expiredTokenRefused: an invite minted on the node with a short
// life is refused once it has passed, with "has expired" (docs/CLI_REFERENCE.md
// "orama node invite" --expiry).
func TestJoin_expiredTokenRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	out := infra.OnNode(t, f, n, "node", "invite", "--raw", "--expiry", shortExpiry.String())
	if out.Exit != 0 {
		t.Fatalf("orama node invite: exit %d: %s", out.Exit, f.Redact(out.Stderr))
	}
	inv := infra.DecodeInvite(t, out.Stdout)
	infra.ForgetPhantomOnCleanup(t, unusedPublicIP)
	// The cluster's judgement, not the runner's clock: the join handler
	// compares expires_at with rqlite's CURRENT_TIMESTAMP (handlers/join
	// assertTokenLive), so the wait asks the same question of the same rqlite.
	eventually.Require(t, expiryPoll, shortExpiry+time.Minute, "the cluster to judge the invite expired", func() (bool, error) {
		q, err := infra.IndexQueryAt(t, f, n, "strong", "SELECT expires_at <= CURRENT_TIMESTAMP FROM invite_tokens WHERE token = ?", infra.HashToken(inv.Token))
		if err != nil {
			return false, err
		}
		if len(q.Values) != 1 {
			return false, eventually.Stop(fmt.Errorf("%d invite_tokens rows for the minted invite, want 1", len(q.Values)))
		}
		expired, _ := q.Values[0][0].(float64)
		return expired == 1, nil
	})
	r := infra.Join(t, n, infra.JoinBody{Token: inv.Token, WGPublicKey: infra.NewWGKey(t), PublicIP: unusedPublicIP})
	expectRefusal(t, r.Status, r.Body, http.StatusUnauthorized, infra.JoinExpired)
	if row := infra.ReadInvite(t, f, n, inv.Token); !row.Found || row.Used {
		t.Errorf("the expired invite's row is %+v: a refusal must not consume it", row)
	}
}

// TestJoin_liveIdentityRefusedTokenKept: a join naming a node that is up is
// 409 "identity already registered" before the token is consumed, however
// many race, and says nothing about which identity collided
// (handlers/join: refuseIfClaimed runs before the atomic consume).
func TestJoin_liveIdentityRefusedTokenKept(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	inv := infra.DecodeInvite(t, mint(t).Invite)
	victim := f.State.Nodes[1]
	c := harness.GW(t).PinTo(n.PublicIP)
	reqs := make([]gw.Req, concurrentJoins)
	for i := range reqs {
		raw, err := json.Marshal(infra.JoinBody{Token: inv.Token, WGPublicKey: infra.NewWGKey(t), PublicIP: victim.PublicIP})
		if err != nil {
			t.Fatal(err)
		}
		reqs[i] = gw.Req{Method: http.MethodPost, Path: infra.JoinPath, Header: http.Header{"Content-Type": {"application/json"}}, Body: raw}
	}
	var wg sync.WaitGroup
	statuses := make([]int, concurrentJoins)
	bodies := make([]string, concurrentJoins)
	errs := make([]error, concurrentJoins)
	for i := range concurrentJoins {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.Send(t.Context(), reqs[i])
			if err != nil {
				errs[i] = err
				return
			}
			statuses[i], bodies[i] = r.Status, string(r.Body)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("a concurrent join could not be sent: %v", err)
		}
	}
	for i := range concurrentJoins {
		expectRefusal(t, statuses[i], []byte(bodies[i]), http.StatusConflict, infra.JoinIdentityDup)
		if strings.Contains(bodies[i], victim.PublicIP) || strings.Contains(bodies[i], "wg_public_key") {
			t.Errorf("the 409 names which identity collided: %.200q", bodies[i])
		}
	}
	if row := infra.ReadInvite(t, f, n, inv.Token); !row.Found || row.Used {
		t.Fatalf("a refused join consumed the invite: %+v", row)
	}
	if out := f.MustExec(t, victim, "wg show wg0 peers"); strings.Count(out.Stdout, "\n") != len(f.State.Nodes)-1 {
		t.Errorf("%s has %d WireGuard peers after refused joins, want %d", victim.Name, strings.Count(out.Stdout, "\n"), len(f.State.Nodes)-1)
	}
}

// TestNodeInvite_localMint: `orama node invite` on a node mints a one-hour
// invite by default, stored hashed, printing the whole install command; --raw
// prints only the invite (docs/CLI_REFERENCE.md "orama node invite").
func TestNodeInvite_localMint(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[1]
	out := infra.OnNode(t, f, n, "node", "invite")
	if out.Exit != 0 || !strings.Contains(out.Stdout, "Invite created (expires in 1h0m0s)") ||
		!strings.Contains(out.Stdout, "orama node install --token") {
		t.Fatalf("orama node invite: exit %d:\n%s", out.Exit, f.Redact(out.Stdout))
	}
	raw := infra.OnNode(t, f, n, "node", "invite", "--raw")
	if raw.Exit != 0 || strings.Count(strings.TrimSpace(raw.Stdout), "\n") != 0 {
		t.Fatalf("--raw printed more than the invite (exit %d)", raw.Exit)
	}
	inv := infra.DecodeInvite(t, raw.Stdout)
	if inv.JoinURL != "https://"+n.PublicIP {
		t.Errorf("a local invite joins through %s, want %s", inv.JoinURL, n.PublicIP)
	}
	row := infra.ReadInvite(t, f, f.State.Nodes[0], inv.Token)
	if !row.Found || row.Used {
		t.Fatalf("the local invite is not replicated as an unused hashed row: %+v", row)
	}
	if d := expiresIn(t, row.Expires); d <= maxInviteLife-clockSlack || d > maxInviteLife+clockSlack {
		t.Errorf("a default local invite expires in %s, want 1h", d)
	}
}

func reqGet() gw.Req { return gw.Req{Method: http.MethodGet, Path: infra.JoinPath} }
