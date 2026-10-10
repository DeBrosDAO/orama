package shared

import (
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

// TokenEnvVar names a pre-issued credential, so a CI job can run without a
// wallet session. It is deliberately independent of the gateway resolution:
// the caller supplying it is asserting the credential is valid for whichever
// gateway they also pointed the CLI at.
//
// It holds either an API key or a token. A key is exchanged for a session once
// per run rather than sent on every request; see auth.BearerFromEnv.
const TokenEnvVar = auth.TokenEnvVar

// envToken returns the credential named by the environment, if any.
func envToken() string {
	return strings.TrimSpace(os.Getenv(TokenEnvVar))
}
