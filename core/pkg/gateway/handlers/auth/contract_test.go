package auth

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/contracttest"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
)

// The bodies the TypeScript SDK sends to /v1/auth/* must decode into these
// request structs with nothing left over. See pkg/contracttest for why.
func TestAuthContract(t *testing.T) {
	fixtures, err := contracttest.For(".", "auth/")
	if err != nil {
		t.Fatalf("load contracts: %v", err)
	}
	if len(fixtures) < 4 {
		t.Fatalf("expected the auth fixtures, found %d", len(fixtures))
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			var target any
			switch fixture.Route {
			case "/v1/auth/challenge":
				target = &ChallengeRequest{}
			case "/v1/auth/verify":
				target = &VerifyRequest{}
			case "/v1/auth/api-key":
				target = &APIKeyRequest{}
			case "/v1/auth/refresh":
				target = &RefreshRequest{}
			case "/v1/auth/logout":
				target = &LogoutRequest{}
			default:
				t.Fatalf("fixture %s names route %s, which this test does not know", fixture.Name, fixture.Route)
			}

			if err := fixture.DecodeStrict(target); err != nil {
				t.Fatalf("the gateway does not parse what %s sends: %v", fixture.SDK, err)
			}
		})
	}
}

// A wallet login is a signed sign-in message. A renewal is a refresh token.
// Losing either turns the call into an unauthenticated one.
func TestAuthContractCarriesTheCredentials(t *testing.T) {
	fixtures, err := contracttest.For(".", "auth/")
	if err != nil {
		t.Fatalf("load contracts: %v", err)
	}

	for _, fixture := range fixtures {
		switch fixture.Route {
		case "/v1/auth/challenge":
			var body ChallengeRequest
			if err := fixture.DecodeStrict(&body); err != nil {
				t.Fatal(err)
			}
			if body.Wallet == "" {
				t.Error("challenge: a wallet is required")
			}
		case "/v1/auth/verify":
			var body VerifyRequest
			if err := fixture.DecodeStrict(&body); err != nil {
				t.Fatal(err)
			}
			if body.Message == "" || body.Signature == "" {
				t.Errorf("verify: message=%q signature=%q — both are required",
					body.Message, body.Signature)
			}
			// The message is not an opaque token the SDK relays: everything
			// the gateway acts on is read out of it, so a fixture carrying one
			// the parser rejects would let the contract drift from the format.
			m, err := siw.Parse(body.Message)
			if err != nil {
				t.Errorf("verify: the message the SDK sends is not a sign-in message: %v", err)
			} else if m.Nonce == "" || m.Address == "" {
				t.Errorf("verify: the message carries no wallet or nonce: %+v", m)
			}
		case "/v1/auth/refresh":
			var body RefreshRequest
			if err := fixture.DecodeStrict(&body); err != nil {
				t.Fatal(err)
			}
			if body.RefreshToken == "" {
				t.Error("refresh: a refresh token is required — bugboard #239 was the namespace going missing from this same body")
			}
			if body.Namespace == "" {
				t.Error("refresh: the namespace the token was issued for is required (bugboard #239)")
			}
		}
	}
}
