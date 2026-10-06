//go:build e2e_fleet

package vault

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// TestInput_malformedRefused: every malformed body is 400 (or 405 for the
// wrong method), before any guardian is contacted.
func TestInput_malformedRefused(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	o := services.NewVaultOwner(t)
	notB64 := o.PushBody(o.Identity(), 1, nil, o)
	notB64["envelope"] = "%%% not base64 %%%"
	shortID := o.PushBody(o.Identity(), 1, []byte("x"), o)
	shortID["identity"] = "abc"
	nonHex := o.PushBody(o.Identity(), 1, []byte("x"), o)
	nonHex["identity"] = strings.Repeat("z", 64)
	cases := map[string]*gw.Response{
		"push not JSON":         services.PostJSON(t, c, services.VaultPush, []byte("identity=x")),
		"push empty":            services.PostJSON(t, c, services.VaultPush, []byte{}),
		"push version a string": services.PostJSON(t, c, services.VaultPush, []byte(`{"identity":"`+o.Identity()+`","version":"1"}`)),
		"push short identity":   services.PostJSON(t, c, services.VaultPush, shortID),
		"push non-hex identity": services.PostJSON(t, c, services.VaultPush, nonHex),
		"push envelope base64":  services.PostJSON(t, c, services.VaultPush, notB64),
		"push empty envelope":   services.PostJSON(t, c, services.VaultPush, o.PushBody(o.Identity(), 1, nil, o)),
		"push over 1 MiB":       services.PostJSON(t, c, services.VaultPush, []byte(`{"envelope":"`+strings.Repeat("A", 1<<20)+`"}`)),
		"pull over 4 KiB":       services.PostJSON(t, c, services.VaultPull, []byte(`{"identity":"`+strings.Repeat("a", 5<<10)+`"}`)),
		"pull short identity":   services.PostJSON(t, c, services.VaultPull, []byte(`{"identity":"00"}`)),
	}
	for name, r := range cases {
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %.200s", name, r.Status, r.Body)
		}
	}
	for _, p := range []string{services.VaultPush, services.VaultPull} {
		if r := c.MustSend(t, gw.Req{Path: p}); r.Status != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: want 405, got %d", p, r.Status)
		}
	}
}

// TestRateLimit_perIdentityPush: an identity may push a burst of five; the
// next is 429 with Retry-After 120, and another identity is unaffected.
func TestRateLimit_perIdentityPush(t *testing.T) {
	t.Parallel()
	c := harness.GW(t).PinTo(harness.Fleet(t).State.Nodes[0].PublicIP)
	o := services.NewVaultOwner(t)
	for v := uint64(1); v <= pushBurst; v++ {
		pushOK(t, c, o, v, envelope(t, 64))
	}
	r := push(t, c, o, pushBurst+1, envelope(t, 64))
	if r.Status != http.StatusTooManyRequests || r.Header.Get("Retry-After") != pushRetry {
		t.Errorf("push %d: want 429 Retry-After %s, got %d %q", pushBurst+1, pushRetry, r.Status, r.Header.Get("Retry-After"))
	}
	pushOK(t, c, services.NewVaultOwner(t), 1, envelope(t, 64))
}

// TestRateLimit_perIdentityPull: twenty pulls in a burst, then 429
// Retry-After 30, on one gateway (the limiter is per gateway).
func TestRateLimit_perIdentityPull(t *testing.T) {
	t.Parallel()
	c := harness.GW(t).PinTo(harness.Fleet(t).State.Nodes[1].PublicIP)
	o := services.NewVaultOwner(t)
	pushOK(t, c, o, 1, envelope(t, 64))
	limited := false
	for i := 0; i < pullBurst+2 && !limited; i++ {
		r := pull(t, c, o)
		switch r.Status {
		case http.StatusOK:
		case http.StatusTooManyRequests:
			limited = true
			if r.Header.Get("Retry-After") != pullRetry {
				t.Errorf("pull 429 Retry-After %q, want %s", r.Header.Get("Retry-After"), pullRetry)
			}
			if i < pullBurst {
				t.Errorf("pull limited after %d, the burst is %d", i, pullBurst)
			}
		default:
			t.Fatalf("pull %d: %d %.200s", i+1, r.Status, r.Body)
		}
	}
	if !limited {
		t.Errorf("%d pulls of one identity were never limited", pullBurst+2)
	}
}
