//go:build e2e_fleet

package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// Quorum on three guardians (core/pkg/shamir AdaptiveThreshold, WriteQuorum:
// K = max(2, n/3), W = min(n, max(K+1, ceil(2n/3)))) and the gateway's limits
// (core/pkg/gateway/handlers/vault).
const (
	guardians3   = 3
	threshold3   = 2
	writeQuorum3 = 3
	pushBurst    = 5 // 30 pushes an hour per identity, burst 30/6
	pullBurst    = 20
	pullSkew     = 120 * time.Second
	shareMax     = 512 << 10 // a guardian's decoded share limit (docs/vault/API.md)
	pushRetry    = "120"
	pullRetry    = "30"
)

func envelope(t testing.TB, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// push stores env as o at version and returns the raw answer.
func push(t testing.TB, c *gw.Client, o *services.VaultOwner, version uint64, env []byte) *gw.Response {
	t.Helper()
	return services.PostJSON(t, c, services.VaultPush, o.PushBody(o.Identity(), version, env, o))
}

// pushOK is push that must succeed with the full quorum on three guardians.
func pushOK(t testing.TB, c *gw.Client, o *services.VaultOwner, version uint64, env []byte) {
	t.Helper()
	var r services.PushResult
	if err := push(t, c, o, version, env).Expect(t, http.StatusOK).Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "ok" || r.Total != guardians3 || r.Quorum != writeQuorum3 || r.Threshold != threshold3 || r.AckCount < writeQuorum3 {
		t.Fatalf("push answered %+v, want ok %d/%d quorum %d K %d", r, writeQuorum3, guardians3, writeQuorum3, threshold3)
	}
}

// pull reads o's envelope now.
func pull(t testing.TB, c *gw.Client, o *services.VaultOwner) *gw.Response {
	t.Helper()
	return services.PostJSON(t, c, services.VaultPull, o.PullBody(o.Identity(), time.Now(), o))
}

// pullEquals fails unless o's envelope reads back as want.
func pullEquals(t testing.TB, c *gw.Client, o *services.VaultOwner, want []byte) services.PullResult {
	t.Helper()
	var r services.PullResult
	if err := pull(t, c, o).Expect(t, http.StatusOK).Decode(&r); err != nil {
		t.Fatal(err)
	}
	got, err := base64.StdEncoding.DecodeString(r.Envelope)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("pulled %d bytes that differ from the %d pushed (%v)", len(got), len(want), err)
	}
	if r.Threshold != threshold3 || r.Collected < threshold3 {
		t.Errorf("pull collected %d shares with K %d, want at least %d with K %d", r.Collected, r.Threshold, threshold3, threshold3)
	}
	return r
}
