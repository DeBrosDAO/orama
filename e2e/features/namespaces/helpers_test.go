//go:build e2e_fleet

package namespaces

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	// cleanupBudget bounds one cleanup call.
	cleanupBudget = time.Minute
	// pollEvery paces waits in this package.
	pollEvery = 5 * time.Second
)

// endSession logs out u's one session (not every session of the wallet).
func endSession(t testing.TB, u *gw.User) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	s := u.Session
	resp, err := u.Client.Logout(ctx, s.AccessToken, s.RefreshToken, s.Namespace, false)
	if err != nil && (resp == nil || resp.Status != http.StatusUnauthorized) {
		t.Errorf("cleanup: failed to end a session of %s: %v", u.Wallet.Address(), err)
	}
}
