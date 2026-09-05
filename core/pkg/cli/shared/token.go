package shared

import (
	"os"
	"strings"
)

// TokenEnvVar names a pre-issued credential, so a CI job can run without a
// wallet session. It is deliberately independent of the gateway resolution:
// the caller supplying it is asserting the token is valid for whichever
// gateway they also pointed the CLI at.
const TokenEnvVar = "ORAMA_TOKEN"

// envToken returns the credential named by the environment, if any.
func envToken() string {
	return strings.TrimSpace(os.Getenv(TokenEnvVar))
}
