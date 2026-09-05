package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// What the CLI signs is the whole of what the signature means. It used to sign
// the bare nonce from the challenge response, which is a signature over an
// opaque blob: nothing in it said which gateway asked, for which namespace, or
// for how long, and the RootWallet dialog showed the user that blob and asked
// them to approve it.
//
// So the one thing these check is that the CLI takes the gateway's message and
// nothing else.

const challengeMessage = `gateway.example wants you to sign in with your Ethereum account:
0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB

Sign in to the acme namespace on Orama.

URI: https://gateway.example
Version: 1
Chain ID: 1
Nonce: 0123456789abcdef
Issued At: 2026-09-04T12:00:00Z
Expiration Time: 2026-09-04T12:05:00Z
Resources:
- urn:orama:namespace:acme`

func TestRequestChallenge_returnsTheMessageToSign(t *testing.T) {
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/challenge" {
			t.Errorf("asked for %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"message":    challengeMessage,
			"nonce":      "0123456789abcdef",
			"wallet":     "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB",
			"namespace":  "acme",
			"expires_at": "2026-09-04T12:05:00Z",
		})
	}))
	defer srv.Close()

	got, err := requestChallenge(srv.Client(), srv.URL, "0xWallet", "acme")
	if err != nil {
		t.Fatalf("requestChallenge: %v", err)
	}
	if got != challengeMessage {
		t.Errorf("the CLI did not return the message verbatim:\n%q", got)
	}
	if body["chain_type"] != "ETH" {
		t.Errorf("chain_type = %q; the gateway needs it to pick which grammar to render", body["chain_type"])
	}
	if body["wallet"] != "0xWallet" || body["namespace"] != "acme" {
		t.Errorf("request body = %v", body)
	}
}

// A gateway that answers with a nonce and no message is one that predates the
// sign-in message. Signing the nonce would produce exactly the credential this
// change exists to stop issuing, so the CLI says so instead.
func TestRequestChallenge_refusesAGatewayThatSendsOnlyANonce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"nonce":      "0123456789abcdef",
			"wallet":     "0xWallet",
			"namespace":  "acme",
			"expires_at": "2026-09-04T12:05:00Z",
		})
	}))
	defer srv.Close()

	_, err := requestChallenge(srv.Client(), srv.URL, "0xWallet", "acme")
	if err == nil {
		t.Fatal("a challenge with no message was accepted; the CLI would have signed the nonce")
	}
	if !strings.Contains(err.Error(), "upgrade the gateway") {
		t.Errorf("the error does not say what to do about it: %v", err)
	}
}

func TestRequestChallenge_surfacesAGatewayRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"namespace acme does not exist","code":"NAMESPACE_UNKNOWN"}`))
	}))
	defer srv.Close()

	_, err := requestChallenge(srv.Client(), srv.URL, "0xWallet", "acme")
	if err == nil {
		t.Fatal("a 404 was read as a challenge")
	}
	if !strings.Contains(err.Error(), "NAMESPACE_UNKNOWN") {
		t.Errorf("the gateway's own answer was dropped: %v", err)
	}
}

// The message and the signature are the whole credential now. Sending the
// wallet or the namespace beside them would be sending fields the signature
// does not cover, and the gateway reads neither.
func TestVerifySignature_sendsTheMessageAndNothingElse(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "jwt", "refresh_token": "rt",
			"subject": "0xwallet", "namespace": "acme", "api_key": "ak_1",
		})
	}))
	defer srv.Close()

	creds, err := verifySignature(srv.Client(), srv.URL, challengeMessage, "0xsig", "acme")
	if err != nil {
		t.Fatalf("verifySignature: %v", err)
	}
	if body["message"] != challengeMessage {
		t.Errorf("message = %v", body["message"])
	}
	if body["signature"] != "0xsig" {
		t.Errorf("signature = %v", body["signature"])
	}
	for _, unsigned := range []string{"wallet", "nonce", "namespace", "chain_type"} {
		if _, present := body[unsigned]; present {
			t.Errorf("the request carries %q beside the message; the signature does not cover it "+
				"and the gateway does not read it", unsigned)
		}
	}
	if creds.APIKey != "ak_1" {
		t.Errorf("api key = %q", creds.APIKey)
	}
}
