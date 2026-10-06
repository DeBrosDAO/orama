package cli

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/auth"
)

// A rate-limited or unreachable gateway is "try later" (5), never "your wallet
// was refused" (3): a script that re-logs-in on 3 would give up on a login that
// a minute's wait would have let through.
func TestLoginFailure_exitCodes(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"rate limited": {fmt.Errorf("failed to verify signature: %w",
			&auth.GatewayError{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Retryable: true, RetryAfter: 60}), clierr.CodeUnavailable},
		"no leader":   {&auth.GatewayError{Status: http.StatusServiceUnavailable}, clierr.CodeUnavailable},
		"unreachable": {fmt.Errorf("%w: dial tcp: connection refused", auth.ErrGatewayUnreachable), clierr.CodeUnavailable},
		"bad signature": {fmt.Errorf("failed to verify signature: %w",
			&auth.GatewayError{Status: http.StatusUnauthorized, Code: "AUTH_SIGNATURE_INVALID"}), clierr.CodeAuth},
		"not a member":          {&auth.GatewayError{Status: http.StatusForbidden, Code: auth.CodeOwnershipRequired}, clierr.CodeAuth},
		"wallet could not sign": {errors.New("failed to sign challenge: agent locked"), clierr.CodeAuth},
	}
	for name, c := range cases {
		if got := clierr.CodeOf(loginFailure(c.err)); got != c.want {
			t.Errorf("%s: exit %d, want %d", name, got, c.want)
		}
	}
}
