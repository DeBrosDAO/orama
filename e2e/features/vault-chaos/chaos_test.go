//go:build e2e_fleet

package vaultchaos

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	vaultUnit   = "orama-namespace-vault@index.service"
	pollEvery   = 2 * time.Second
	probeBudget = time.Minute
	// pushPerMinutePerIP is the gateway's per-address push limit
	// (core/pkg/gateway/handlers/vault/ratelimit.go).
	pushPerMinutePerIP = 30
)

type status struct {
	Guardians   int `json:"guardians"`
	Healthy     int `json:"healthy"`
	Threshold   int `json:"threshold"`
	WriteQuorum int `json:"write_quorum"`
}

// TestGuardianDown_readsSurviveWritesRefused: with just enough guardians
// stopped that the rest are one short of the write quorum W (one of three,
// two of five), status shows them gone and health "degraded" (K <= healthy
// < W), a pull still reconstructs from K, and a push is refused 503
// insufficient_quorum rather than reported stored with fewer than W shares
// (core/pkg/shamir WriteQuorum: W > K). After the restart a push succeeds
// again. The cluster's own status gives N and W.
func TestGuardianDown_readsSurviveWritesRefused(t *testing.T) {
	c := harness.GW(t)
	f := harness.Fleet(t)
	o := services.NewVaultOwner(t)
	env := randomEnvelope(t)
	mustPush(t, c, o, 1, env, http.StatusOK)
	var before status
	if err := c.MustSend(t, gw.Req{Path: services.VaultStatus}).Decode(&before); err != nil {
		t.Fatal(err)
	}
	total := before.Guardians
	down := total - before.WriteQuorum + 1
	if before.Healthy != total || down < 1 || total-down < before.Threshold || len(f.State.Nodes) < down {
		t.Fatalf("vault status %+v: cannot stop guardians to one below the write quorum and keep the read threshold", before)
	}
	t.Run("one below the write quorum", func(t *testing.T) {
		nodes := f.State.Nodes[len(f.State.Nodes)-down:]
		for _, n := range nodes {
			f.HoldDown(t, n, vaultUnit)
		}
		eventually.Require(t, pollEvery, probeBudget, "status to see the guardians gone", func() (bool, error) {
			var s status
			if err := c.MustSend(t, gw.Req{Path: services.VaultStatus}).Decode(&s); err != nil {
				return false, err
			}
			return s.Guardians == total && s.Healthy == total-down, nil
		})
		var h struct{ Status string }
		if err := c.MustSend(t, gw.Req{Path: services.VaultHealth}).Decode(&h); err != nil || h.Status != "degraded" {
			t.Errorf("health with %d of %d guardians: %q (%v), want degraded", total-down, total, h.Status, err)
		}
		mustPull(t, c, o, env)
		refused := randomEnvelope(t)
		var r services.PushResult
		if err := json.Unmarshal(mustPush(t, c, o, 2, refused, http.StatusServiceUnavailable).Body, &r); err != nil || r.Status != "insufficient_quorum" {
			t.Errorf("push with %d of %d guardians: %+v, want insufficient_quorum", total-down, total, r)
		}
		// Refusing the push does not undo it: the live guardians stored their
		// shares of version 2, at least the read threshold, so a pull
		// reconstructs the newer envelope ("a sub-quorum write may be
		// unrecoverable", handlers/vault/push_handler.go). What it must never
		// be is neither: an error, or something that was never pushed.
		mustPullOneOf(t, c, o, env, refused)
	})
	eventually.Require(t, pollEvery, probeBudget, "the guardian back", func() (bool, error) {
		var s status
		err := c.MustSend(t, gw.Req{Path: services.VaultStatus}).Decode(&s)
		return err == nil && s.Healthy == total, err
	})
	next := randomEnvelope(t)
	mustPush(t, c, o, 3, next, http.StatusOK)
	mustPull(t, c, o, next)
}

// TestRateLimit_forwardedForNotTrusted: the per-address push limit reads
// X-Forwarded-For first, so it must be Caddy's value, not the client's.
// Pushes with a bad signature and a fresh XFF each still hit 429 within the
// per-minute budget. An unpaced flood: destructive, runs alone.
func TestRateLimit_forwardedForNotTrusted(t *testing.T) {
	c := harness.GW(t).PinTo(harness.Fleet(t).State.Nodes[0].PublicIP)
	o, other := services.NewVaultOwner(t), services.NewVaultOwner(t)
	body, err := json.Marshal(o.PushBody(o.Identity(), 1, []byte("x"), other))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= pushPerMinutePerIP+5; i++ {
		r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: services.VaultPush, Body: body,
			Header: http.Header{"Content-Type": {"application/json"}, "X-Forwarded-For": {"198.51.100." + strconv.Itoa(i+1)}}})
		if r.Status == http.StatusTooManyRequests {
			return
		}
		if r.Status != http.StatusUnauthorized {
			t.Fatalf("push %d with a bad signature: %d %.200s", i+1, r.Status, r.Body)
		}
	}
	t.Errorf("%d pushes from one address with rotating X-Forwarded-For were never limited: the limiter trusts the client's header",
		pushPerMinutePerIP+6)
}

func randomEnvelope(t testing.TB) []byte {
	t.Helper()
	b := make([]byte, 512)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func mustPush(t testing.TB, c *gw.Client, o *services.VaultOwner, v uint64, env []byte, want int) *gw.Response {
	t.Helper()
	return services.PostJSON(t, c, services.VaultPush, o.PushBody(o.Identity(), v, env, o)).Expect(t, want)
}

func mustPull(t testing.TB, c *gw.Client, o *services.VaultOwner, want []byte) {
	t.Helper()
	mustPullOneOf(t, c, o, want)
}

// mustPullOneOf pulls and requires the envelope to be one of want (every
// envelope in it was pushed, so anything else is corruption or a stranger's).
func mustPullOneOf(t testing.TB, c *gw.Client, o *services.VaultOwner, want ...[]byte) {
	t.Helper()
	var r services.PullResult
	if err := services.PostJSON(t, c, services.VaultPull, o.PullBody(o.Identity(), time.Now(), o)).
		Expect(t, http.StatusOK).Decode(&r); err != nil {
		t.Fatal(err)
	}
	var st status
	if err := c.MustSend(t, gw.Req{Path: services.VaultStatus}).Decode(&st); err != nil {
		t.Fatal(err)
	}
	got, err := base64.StdEncoding.DecodeString(r.Envelope)
	matched := false
	for _, w := range want {
		matched = matched || bytes.Equal(got, w)
	}
	if err != nil || !matched || r.Threshold != st.Threshold {
		t.Errorf("pull: %d bytes, K %d, want one of the %d pushed envelope(s) with the cluster's K %d", len(got), r.Threshold, len(want), st.Threshold)
	}
}
