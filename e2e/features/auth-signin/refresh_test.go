//go:build e2e_fleet

package authsignin

import (
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// refreshRefusedText is the refresh route's answer for an unknown, spent or
// revoked refresh token (core/pkg/gateway/handlers/auth/jwt_handler.go).
const refreshRefusedText = "invalid or expired refresh token"

// refresh posts a refresh request and returns the raw response.
func refresh(t testing.TB, c *gw.Client, token, namespace string) *gw.Response {
	t.Helper()
	return postJSON(t, c, gw.PathRefresh, "", map[string]string{"refresh_token": token, "namespace": namespace})
}

// expectRefreshRefused: a dead refresh token is a 401.
func expectRefreshRefused(t testing.TB, resp *gw.Response, what string) {
	t.Helper()
	var body struct{ Error string }
	if resp.Status != http.StatusUnauthorized || resp.Decode(&body) != nil || body.Error != refreshRefusedText {
		t.Fatalf("%s: want 401 %q, got %d: %s", what, refreshRefusedText, resp.Status, resp.Body)
	}
}

// TestRefresh_rotatesOnEveryUse: a refresh returns a new refresh token and a
// fresh 15-minute access token that works (docs/AUTH.md#signing-in).
func TestRefresh_rotatesOnEveryUse(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	s := signIn(t, c, newWallet(t), "")
	var next gw.Session
	if err := refresh(t, c, s.RefreshToken, lobby).Expect(t, http.StatusOK).Decode(&next); err != nil {
		t.Fatal(err)
	}
	if next.RefreshToken == "" || next.RefreshToken == s.RefreshToken || next.AccessToken == s.AccessToken {
		t.Fatal("refreshing did not rotate the refresh token and mint a new access token")
	}
	_, before := jwtClaims(t, s.AccessToken)
	_, after := jwtClaims(t, next.AccessToken)
	if before["sid"] != after["sid"] {
		t.Errorf("the session id changed across a rotation: %v -> %v", before["sid"], after["sid"])
	}
	if got := claimTime(t, after, "exp").Sub(claimTime(t, after, "iat")); got != accessTokenLifetime {
		t.Errorf("refreshed access token lives %s, want %s", got, accessTokenLifetime)
	}
	whoami(t, c, next.AccessToken).Expect(t, http.StatusOK)
}

// TestRefresh_replayRefusedAfterGrace: a rotated refresh token is accepted
// once more inside the reuse grace (a client that lost the response), then
// never again; outside the grace a replay is refused at once.
func TestRefresh_replayRefusedAfterGrace(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	s := signIn(t, c, newWallet(t), "")
	// The grace is 60s from the rotation. The three uses take their pacer
	// tokens first: paced one by one behind the other packages' credential
	// calls, the second could land after the grace and be refused for that.
	inGrace, err := c.Prepay(t.Context(), 3)
	if err != nil {
		t.Fatal(err)
	}
	refresh(t, inGrace, s.RefreshToken, lobby).Expect(t, http.StatusOK)
	refresh(t, inGrace, s.RefreshToken, lobby).Expect(t, http.StatusOK)
	expectRefreshRefused(t, refresh(t, inGrace, s.RefreshToken, lobby), "third use of a rotated token inside the grace")

	s2 := signIn(t, c, newWallet(t), "")
	var next gw.Session
	if err := refresh(t, c, s2.RefreshToken, lobby).Expect(t, http.StatusOK).Decode(&next); err != nil {
		t.Fatal(err)
	}
	// The grace runs from s2's rotation, which the gateway stamped before it
	// answered: start the clock once that answer is in hand.
	rotatedAt := time.Now()
	waitUntil(t, rotatedAt.Add(refreshReuseGrace+stalenessSlack), "the refresh reuse grace to pass")
	expectRefreshRefused(t, refresh(t, c, s2.RefreshToken, lobby), "a rotated token after the grace")
	refresh(t, c, next.RefreshToken, lobby).Expect(t, http.StatusOK)
}

// TestRefresh_concurrentUseHasOneWinnerPerGrace: the same refresh token
// presented by several clients at once never mints more sessions than the
// rotation plus its single grace slot.
func TestRefresh_concurrentUseHasOneWinnerPerGrace(t *testing.T) {
	t.Parallel()
	const racers = 5
	f := harness.Fleet(t)
	c := harness.GW(t)
	s := signIn(t, c, newWallet(t), "")
	nodes := perNode(t, f, c)
	results := make(chan int, racers)
	for i := 0; i < racers; i++ {
		nc := nodes[i%len(nodes)]
		go func() {
			resp, err := nc.Client.Send(t.Context(), refreshReq(s.RefreshToken, lobby))
			if err != nil {
				results <- 0
				return
			}
			results <- resp.Status
		}()
	}
	ok := 0
	for i := 0; i < racers; i++ {
		switch st := <-results; st {
		case http.StatusOK:
			ok++
		case http.StatusUnauthorized:
		default:
			t.Errorf("a racing refresh answered %d", st)
		}
	}
	if ok < 1 || ok > 2 {
		t.Fatalf("%d of %d racing refreshes of one token succeeded, want the rotation and at most one grace use", ok, racers)
	}
}

// TestRefresh_malformedAndMismatched: garbage, an access token in place of a
// refresh token, wrong method, bad JSON and missing fields are all refused,
// and none of those refusals spends the real refresh token.
func TestRefresh_malformedAndMismatched(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	s := signIn(t, c, newWallet(t), "")
	expectRefreshRefused(t, refresh(t, c, "not-a-refresh-token", lobby), "a garbage refresh token")
	expectRefreshRefused(t, refresh(t, c, s.AccessToken, lobby), "an access token presented as a refresh token")
	cases := map[string]struct {
		req  gw.Req
		want int
	}{
		"GET":           {gw.Req{Path: gw.PathRefresh}, http.StatusMethodNotAllowed},
		"not JSON":      {jsonBody(gw.PathRefresh, `{"refresh_token":`), http.StatusBadRequest},
		"missing token": {jsonBody(gw.PathRefresh, `{"namespace":"default"}`), http.StatusBadRequest},
		"wrong type":    {jsonBody(gw.PathRefresh, `{"refresh_token":7}`), http.StatusBadRequest},
	}
	for name, tc := range cases {
		if resp := c.MustSend(t, tc.req); resp.Status != tc.want {
			t.Errorf("%s: want %d, got %d: %.300s", name, tc.want, resp.Status, resp.Body)
		}
	}
	// None of the refusals spent the real token.
	refresh(t, c, s.RefreshToken, lobby).Expect(t, http.StatusOK)
}

func refreshReq(token, namespace string) gw.Req {
	return jsonBody(gw.PathRefresh, `{"refresh_token":"`+token+`","namespace":"`+namespace+`"}`)
}
