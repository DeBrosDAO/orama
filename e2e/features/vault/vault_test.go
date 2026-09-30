//go:build e2e_fleet

package vault

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// TestStatus_threeGuardiansQuorum: /v1/vault/status reports every node as a
// guardian with K=2, W=3 and /v1/vault/health says healthy, anonymously.
func TestStatus_threeGuardiansQuorum(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	var s struct {
		Guardians   int `json:"guardians"`
		Healthy     int `json:"healthy"`
		Threshold   int `json:"threshold"`
		WriteQuorum int `json:"write_quorum"`
	}
	if err := c.MustSend(t, gw.Req{Path: services.VaultStatus}).Expect(t, http.StatusOK).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.Guardians != guardians3 || s.Healthy != guardians3 || s.Threshold != threshold3 || s.WriteQuorum != writeQuorum3 {
		t.Errorf("vault status %+v, want %d guardians, all healthy, K %d, W %d", s, guardians3, threshold3, writeQuorum3)
	}
	var h struct{ Status string }
	if err := c.MustSend(t, gw.Req{Path: services.VaultHealth}).Expect(t, http.StatusOK).Decode(&h); err != nil {
		t.Fatal(err)
	}
	if h.Status != "healthy" {
		t.Errorf("vault health %q, want healthy", h.Status)
	}
	for _, p := range []string{services.VaultStatus, services.VaultHealth} {
		if r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: p}); r.Status != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: want 405, got %d", p, r.Status)
		}
	}
}

// TestPush_pullRoundTrip: a pushed envelope is split over three guardians
// and reconstructed from K=2 on pull, through every node's gateway.
func TestPush_pullRoundTrip(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	o := services.NewVaultOwner(t)
	env := envelope(t, 4<<10)
	pushOK(t, c, o, 1, env)
	for _, n := range f.State.Nodes {
		pullEquals(t, c.PinTo(n.PublicIP), o, env)
	}
}

// TestPush_versionsAreMonotonic: a higher version replaces the envelope; the
// same or a lower version is 409 version_conflict and changes nothing
// (docs/vault/API.md: anti-rollback).
func TestPush_versionsAreMonotonic(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	o := services.NewVaultOwner(t)
	v1, v2 := envelope(t, 256), envelope(t, 256)
	pushOK(t, c, o, 1, v1)
	pushOK(t, c, o, 2, v2)
	pullEquals(t, c, o, v2)
	for _, version := range []uint64{2, 1} {
		var r services.PushResult
		resp := push(t, c, o, version, envelope(t, 256))
		if resp.Status != http.StatusConflict || resp.Decode(&r) != nil || r.Status != "version_conflict" {
			t.Errorf("push at version %d after 2: want 409 version_conflict, got %d %.200s", version, resp.Status, resp.Body)
		}
	}
	pullEquals(t, c, o, v2)
}

// TestPush_largeEnvelope: the gateway sends each guardian a share of one
// x-coordinate byte plus len(envelope) bytes (handlers/vault/push_handler.go)
// and a guardian refuses a decoded share over its 512 KiB limit
// (vault/src/server/handler_push.zig MAX_SHARE_SIZE). So an envelope of
// shareMax-1 bytes is the largest that round-trips; one of shareMax is
// never reported stored and leaves the old one.
func TestPush_largeEnvelope(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	o := services.NewVaultOwner(t)
	env := envelope(t, shareMax-shareXByte)
	pushOK(t, c, o, 1, env)
	pullEquals(t, c, o, env)
	if r := push(t, c, o, 2, envelope(t, shareMax)); r.Status == http.StatusOK {
		t.Errorf("an envelope over the share limit was stored: %s", r.Body)
	}
	pullEquals(t, c, o, env)
}

// TestOwnership_wrongOwnerRefused: a push or pull for an identity signed by
// another key, over another message, or with a stale timestamp is 401.
func TestOwnership_wrongOwnerRefused(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	owner, thief := services.NewVaultOwner(t), services.NewVaultOwner(t)
	env := envelope(t, 128)
	pushOK(t, c, owner, 1, env)
	id := owner.Identity()
	stolenPush := owner.PushBody(id, 2, envelope(t, 128), thief)
	wrongVersion := owner.PushBody(id, 3, env, owner)
	wrongVersion["version"] = 4
	cases := map[string]*gw.Response{
		"push signed by another key":  services.PostJSON(t, c, services.VaultPush, stolenPush),
		"push signature of a version": services.PostJSON(t, c, services.VaultPush, wrongVersion),
		"pull signed by another key":  services.PostJSON(t, c, services.VaultPull, owner.PullBody(id, time.Now(), thief)),
		"pull 3 minutes old":          services.PostJSON(t, c, services.VaultPull, owner.PullBody(id, time.Now().Add(-pullSkew-time.Minute), owner)),
		"pull 3 minutes ahead":        services.PostJSON(t, c, services.VaultPull, owner.PullBody(id, time.Now().Add(pullSkew+time.Minute), owner)),
	}
	for name, r := range cases {
		if r.Status != http.StatusUnauthorized || !strings.Contains(string(r.Body), "ownership") {
			t.Errorf("%s: want 401 invalid ownership signature, got %d %.200s", name, r.Status, r.Body)
		}
	}
	pullEquals(t, c, owner, env)
}

// TestPull_neverPushedIdentity: an identity nothing was stored for has no
// read set: 503 with no version-consistent shares
// (handlers/vault/pull_handler.go), and no envelope.
func TestPull_neverPushedIdentity(t *testing.T) {
	t.Parallel()
	r := pull(t, harness.GW(t), services.NewVaultOwner(t))
	if r.Status != http.StatusServiceUnavailable || !strings.Contains(string(r.Body), noReadSet) || strings.Contains(string(r.Body), `"envelope"`) {
		t.Fatalf("pull of an identity that never pushed: want 503 %q, got %d %.200s", noReadSet, r.Status, r.Body)
	}
}
