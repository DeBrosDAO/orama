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

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/pkg/shamir"
)

// The gateway's limits (core/pkg/gateway/handlers/vault).
const (
	pushBurst  = 5 // 30 pushes an hour per identity, burst 30/6
	pullBurst  = 20
	pullSkew   = 120 * time.Second
	shareMax   = 512 << 10 // a guardian's decoded share limit (docs/whitepaper/technical-reference/vol1/28-vault.md, Limits and scale)
	shareXByte = 1         // the x-coordinate byte the gateway prepends to each share
	noReadSet  = "not enough consistent shares"
	pushRetry  = "120"
	pullRetry  = "30"
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
	guardians3, threshold3, writeQuorum3 := quorum(t)
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
	_, threshold3, _ := quorum(t)
	if r.Threshold != threshold3 || r.Collected < threshold3 {
		t.Errorf("pull collected %d shares with K %d, want at least %d with K %d", r.Collected, r.Threshold, threshold3, threshold3)
	}
	return r
}

// quorum is the vault's shape on this fleet: every node is a guardian, the
// read threshold K and write quorum W follow core/pkg/shamir
// (K = max(2, n/3), W = min(n, max(K+1, ceil(2n/3)))).
func quorum(t testing.TB) (guardians, threshold, writeQuorum int) {
	t.Helper()
	n := len(harness.Fleet(t).State.Nodes)
	return n, shamir.AdaptiveThreshold(n), shamir.WriteQuorum(n)
}
