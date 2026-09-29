//go:build e2e_fleet

package chaoslifecycle

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	// herdSize is far above what one address may ask for: 30 a minute,
	// burst 10, per gateway (core/pkg/gateway/gateway.go configureRateLimiters).
	herdSize     = 120
	quiesceLimit = 2 * time.Minute
)

// herdCount is what a thundering herd got back.
type herdCount struct {
	mu                        sync.Mutex
	ok, limited, other, errs  int
	limitedWithoutRetryAfter  int
	firstOther, firstTransErr string
}

func (h *herdCount) add(resp *gw.Response, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var se *gw.StatusError
	switch {
	case err == nil:
		h.ok++
	case errors.As(err, &se) && se.Status == http.StatusTooManyRequests:
		h.limited++
		if resp == nil || resp.Header.Get("Retry-After") == "" {
			h.limitedWithoutRetryAfter++
		}
	case errors.As(err, &se):
		h.other++
		if h.firstOther == "" {
			h.firstOther = se.Error()
		}
	default:
		h.errs++
		if h.firstTransErr == "" {
			h.firstTransErr = err.Error()
		}
	}
}

// TestChaosLifecycle_authChallengeThunderingHerd: 120 fresh wallets ask for
// a sign-in challenge at the same moment from one address, unpaced. The
// gateway serves up to its limit and answers the rest 429 with Retry-After;
// nothing is a 5xx or a dropped connection, the gateway stays healthy, and
// once the budget refills a paced sign-in works (docs/SECURITY.md; README
// "Pacing credential calls": this spends the run's budget, so it runs alone
// and quiesces the run's pacer before and after).
func TestChaosLifecycle_authChallengeThunderingHerd(t *testing.T) {
	f := harness.Fleet(t)
	host := harness.CLI(t).GatewayHost
	quiesce(t, host)
	t.Cleanup(func() { quiesce(t, host) })
	c := harness.GW(t).Unpaced()
	var h herdCount
	var wg sync.WaitGroup
	for range herdSize {
		w, err := wallet.NewEVM()
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, resp, err := c.Challenge(t.Context(), gw.ChallengeRequest{Wallet: w.Address()})
			h.add(resp, err)
		}()
	}
	wg.Wait()
	t.Logf("herd of %d: %d served, %d limited, %d other, %d transport errors", herdSize, h.ok, h.limited, h.other, h.errs)
	if h.other > 0 || h.errs > 0 {
		t.Errorf("the herd got %d non-429 errors (first: %s) and %d transport errors (first: %s)", h.other, h.firstOther, h.errs, h.firstTransErr)
	}
	if h.ok == 0 || h.limited == 0 {
		t.Errorf("want some challenges served and the rest limited, got %d served and %d limited", h.ok, h.limited)
	}
	if h.limitedWithoutRetryAfter > 0 {
		t.Errorf("%d 429s carried no Retry-After", h.limitedWithoutRetryAfter)
	}
	harness.GW(t).MustSend(t, gw.Req{Path: "/v1/health"}).Expect(t, http.StatusOK)
	quiesce(t, host)
	gw.NewUser(t, f, gw.LobbyNamespace)
}

// quiesce holds the run pacer's whole credential burst, so the product's
// bucket on every gateway has refilled.
func quiesce(t testing.TB, host string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), quiesceLimit)
	defer cancel()
	if err := edge.Quiesce(ctx, host); err != nil {
		t.Errorf("quiescing the credential budget of %s: %v", host, err)
	}
}
