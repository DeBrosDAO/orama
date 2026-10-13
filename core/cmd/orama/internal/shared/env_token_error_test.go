package shared

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/auth"
)

// A key the gateway refuses is an auth failure (3); a gateway that says "not
// now" or cannot be reached is Unavailable (5), since the key may be fine. A
// rate-limited exchange used to exit 1.
func TestEnvTokenError_exitCodes(t *testing.T) {
	wrap := func(e error) error {
		return fmt.Errorf("%s could not be exchanged for a session: %w", auth.TokenEnvVar, e)
	}
	cases := map[string]struct {
		err  error
		want int
	}{
		"unknown key":  {wrap(&auth.GatewayError{Status: http.StatusUnauthorized, Code: auth.CodeAuthInvalidKey}), clierr.CodeAuth},
		"forbidden":    {wrap(&auth.GatewayError{Status: http.StatusForbidden}), clierr.CodeAuth},
		"rate limited": {wrap(&auth.GatewayError{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Retryable: true}), clierr.CodeUnavailable},
		"no leader":    {wrap(&auth.GatewayError{Status: http.StatusServiceUnavailable}), clierr.CodeUnavailable},
		"unreachable":  {wrap(fmt.Errorf("%w: reach x: connection refused", auth.ErrGatewayUnreachable)), clierr.CodeUnavailable},
		"other":        {wrap(errors.New("read the response: unexpected EOF")), clierr.CodeFailure},
	}
	for name, c := range cases {
		if got := clierr.CodeOf(envTokenError(c.err)); got != c.want {
			t.Errorf("%s: exit %d, want %d", name, got, c.want)
		}
	}
}
