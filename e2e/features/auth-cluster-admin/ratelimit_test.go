//go:build e2e_fleet

package authclusteradmin

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// The sign-in limits (core/pkg/gateway/gateway.go configureRateLimiters,
// handlers/auth/wallet_rate_limit.go, auth/nonce_limits.go). These floods run
// here, in a package that runs alone, because the per-address bucket is
// shared with every other test the runner sends.
const (
	// perWalletBurst challenges per wallet per gateway before a 429.
	perWalletBurst = 5
	// perAddressBurst credential operations per address per gateway.
	perAddressBurst = 10
	// maxOutstanding unanswered challenges per wallet and namespace.
	maxOutstanding = 10
	// flood is more than one bucket's burst.
	flood = perAddressBurst + 4
	// racers is how many gateways-spread verifies race one nonce.
	racers = 9
	// Retry-After values the refusals carry.
	walletRetryAfter      = "60"
	outstandingRetryAfter = "300"
)

func challenge(t testing.TB, c *gw.Client, wallet string) *gw.Response {
	t.Helper()
	return postJSON(t, c, gw.PathChallenge, "", gw.ChallengeRequest{Wallet: wallet})
}

// TestSignInLimits_inOrder runs the floods one after another, each on
// unpaced clients pinned to gateways so the buckets it drains are known, and
// each between two edge.Quiesce calls: it starts on full product buckets and
// the next flood (or paced request of the run) finds them full again.
func TestSignInLimits_inOrder(t *testing.T) {
	f := harness.Fleet(t)
	nodes := perNode(t, f, harness.GW(t).Unpaced())
	if len(nodes) < 3 {
		t.Fatalf("these floods need three gateways, the run has %d", len(nodes))
	}
	floods := []struct {
		name string
		run  func(*testing.T)
	}{
		{"outstanding challenges capped", func(t *testing.T) { outstandingCapped(t, nodes) }},
		{"per-wallet bucket", func(t *testing.T) { perWalletBucket(t, nodes[0].Client) }},
		{"per-address bucket", func(t *testing.T) { perAddressBucket(t, nodes[1].Client) }},
		{"one nonce, one winner", func(t *testing.T) { oneWinner(t, nodes) }},
	}
	for _, fl := range floods {
		t.Run(fl.name, func(t *testing.T) {
			quiesce(t, f)
			fl.run(t)
		})
	}
}

// outstandingCapped: a wallet holding ten unanswered challenges in one
// namespace is refused an eleventh with TOO_MANY_CHALLENGES (429,
// Retry-After 300) — whichever gateway it asks — and answering one frees a
// slot (docs/AUTH.md sign-in codes).
func outstandingCapped(t *testing.T, nodes []nodeClient) {
	w := newWallet(t)
	var first *gw.ChallengeResponse
	for i := 0; i < maxOutstanding; i++ {
		nc := nodes[i/perWalletBurst]
		ch, _, err := nc.Client.For(t).Challenge(t.Context(), gw.ChallengeRequest{Wallet: w.Address()})
		if err != nil {
			t.Fatalf("challenge %d via %s: %v", i+1, nc.Node.Name, err)
		}
		if first == nil {
			first = ch
		}
	}
	resp := challenge(t, nodes[2].Client, w.Address())
	if resp.Status != http.StatusTooManyRequests || resp.ErrorCode() != "TOO_MANY_CHALLENGES" ||
		resp.Header.Get("Retry-After") != outstandingRetryAfter {
		t.Fatalf("challenge %d: want 429 TOO_MANY_CHALLENGES Retry-After %s, got %d %q %s",
			maxOutstanding+1, outstandingRetryAfter, resp.Status, resp.Header.Get("Retry-After"), resp.Body)
	}
	sig, err := w.Sign(first.Message)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := nodes[2].Client.For(t).Verify(t.Context(), gw.VerifyRequest{Message: first.Message, Signature: sig}); err != nil {
		t.Fatalf("answering an outstanding challenge: %v", err)
	}
	challenge(t, nodes[2].Client, w.Address()).Expect(t, http.StatusOK)
}

// perWalletBucket: one wallet gets five challenges a gateway, then a 429 with
// Retry-After 60; another wallet from the same address is unaffected.
func perWalletBucket(t *testing.T, c *gw.Client) {
	w := newWallet(t).Address()
	for i := 0; i < perWalletBurst; i++ {
		challenge(t, c, w).Expect(t, http.StatusOK)
	}
	resp := challenge(t, c, w)
	if resp.Status != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != walletRetryAfter ||
		!strings.Contains(string(resp.Body), "too many challenges for this wallet") {
		t.Fatalf("challenge %d for one wallet: want 429 Retry-After %s, got %d %q %s",
			perWalletBurst+1, walletRetryAfter, resp.Status, resp.Header.Get("Retry-After"), resp.Body)
	}
	challenge(t, c, newWallet(t).Address()).Expect(t, http.StatusOK)
}

// perAddressBucket: past the burst, credential operations from one address
// are refused with RATE_LIMITED and Retry-After 60.
func perAddressBucket(t *testing.T, c *gw.Client) {
	limited := 0
	for i := 0; i < flood; i++ {
		resp := challenge(t, c, newWallet(t).Address())
		switch resp.Status {
		case http.StatusOK:
		case http.StatusTooManyRequests:
			var body struct {
				OK    bool `json:"ok"`
				Error struct {
					Code      string `json:"code"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			if err := resp.Decode(&body); err != nil || body.OK || body.Error.Code != "RATE_LIMITED" ||
				!body.Error.Retryable || resp.Header.Get("Retry-After") != walletRetryAfter {
				t.Errorf("rate-limit refusal shape: %d %q %s", resp.Status, resp.Header.Get("Retry-After"), resp.Body)
			}
			limited++
		default:
			t.Errorf("challenge %d answered %d: %s", i+1, resp.Status, resp.Body)
		}
	}
	if limited == 0 {
		t.Fatalf("%d credential operations from one address in a burst were all admitted; the burst is %d", flood, perAddressBurst)
	}
}

// oneWinner: one signed message presented to every gateway at once signs in
// exactly once; every other attempt is AUTH_CHALLENGE_INVALID (the nonce is
// single-use cluster-wide, docs/AUTH.md#signing-in).
func oneWinner(t *testing.T, nodes []nodeClient) {
	w := newWallet(t)
	ch, _, err := nodes[0].Client.For(t).Challenge(t.Context(), gw.ChallengeRequest{Wallet: w.Address()})
	if err != nil {
		t.Fatal(err)
	}
	sig, err := w.Sign(ch.Message)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(gw.VerifyRequest{Message: ch.Message, Signature: sig})
	if err != nil {
		t.Fatal(err)
	}
	statuses, codes := make([]int, racers), make([]string, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := nodes[i%len(nodes)].Client.Send(t.Context(), gw.Req{Method: http.MethodPost, Path: gw.PathVerify,
				Header: http.Header{"Content-Type": {"application/json"}}, Body: body})
			if err == nil {
				statuses[i], codes[i] = resp.Status, resp.ErrorCode()
			}
		}()
	}
	wg.Wait()
	won := 0
	for i := range statuses {
		switch {
		case statuses[i] == http.StatusOK:
			won++
		case statuses[i] == http.StatusUnauthorized && codes[i] == "AUTH_CHALLENGE_INVALID":
		default:
			t.Errorf("racer %d answered %d %s", i, statuses[i], codes[i])
		}
	}
	if won != 1 {
		t.Fatalf("%d of %d concurrent verifies of one nonce signed in, want exactly 1", won, racers)
	}
}
