package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rwagent"
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

func TestAcceptLoginChallenge(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 1, 0, 0, time.UTC)
	wallet := "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB"
	if err := acceptLoginChallenge("https://gateway.example", wallet, challengeMessage, now); err != nil {
		t.Fatal(err)
	}
	if err := acceptLoginChallenge("https://evil.example", wallet, challengeMessage, now); err == nil {
		t.Fatal("a challenge for another domain was accepted")
	}
	if err := acceptLoginChallenge("https://gateway.example", "0x0000000000000000000000000000000000000001", challengeMessage, now); err == nil {
		t.Fatal("a challenge for another wallet was accepted")
	}
	if err := acceptLoginChallenge("https://gateway.example", wallet, "Orama build archive v1\nversion: 1\n", now); err == nil {
		t.Fatal("an archive signing request was accepted as a login")
	}
	if err := acceptLoginChallenge("https://gateway.example", wallet, challengeMessage, now.Add(time.Hour)); err == nil {
		t.Fatal("an expired challenge was accepted")
	}
}

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

	got, err := requestChallenge(srv.Client(), srv.URL, "0xWallet", "acme", "")
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

	_, err := requestChallenge(srv.Client(), srv.URL, "0xWallet", "acme", "")
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

	_, err := requestChallenge(srv.Client(), srv.URL, "0xWallet", "acme", "")
	if err == nil {
		t.Fatal("a 404 was read as a challenge")
	}
	var refusal *GatewayError
	if !errors.As(err, &refusal) || refusal.Code != "NAMESPACE_UNKNOWN" || refusal.Status != http.StatusNotFound ||
		!strings.Contains(err.Error(), "namespace acme does not exist") {
		t.Errorf("the gateway's own answer was dropped: %v", err)
	}
}

func TestRequestChallenge_namesTheDevice(t *testing.T) {
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"message": challengeMessage})
	}))
	defer srv.Close()

	if _, err := requestChallenge(srv.Client(), srv.URL, "0xWallet", "acme", "device-thumb"); err != nil {
		t.Fatal(err)
	}
	if body["device_id"] != "device-thumb" {
		t.Fatalf("device_id = %q", body["device_id"])
	}
}

func TestVerifySignature_sendsTheDeviceProofAndNotItsPrivateKey(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "jwt", "refresh_token": "rt",
			"subject": "0xwallet", "namespace": "acme",
		})
	}))
	defer srv.Close()

	device := &LoginDevice{
		ID:        "device-thumb",
		PublicJWK: json.RawMessage(`{"kty":"OKP","crv":"Ed25519","x":"abc"}`),
		Sign:      func(string) (string, error) { return "device-sig", nil },
	}
	if _, err := verifySignature(srv.Client(), srv.URL, challengeMessage, "0xsig", "acme", device); err != nil {
		t.Fatal(err)
	}
	if body["device_signature"] != "device-sig" {
		t.Fatalf("device_signature = %v", body["device_signature"])
	}
	raw, _ := json.Marshal(body["device_key"])
	if strings.Contains(string(raw), `"d"`) {
		t.Fatalf("the private half was sent: %s", raw)
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

	creds, err := verifySignature(srv.Client(), srv.URL, challengeMessage, "0xsig", "acme", nil)
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

func TestIsRootWalletInstalled_e2eGuardRefusalIsNotNoWallet(t *testing.T) {
	t.Setenv(rwagent.E2EEnvVar, "1")
	t.Setenv("RW_AGENT_SOCK", "")
	if !IsRootWalletInstalled() {
		t.Fatal("the e2e guard's refusal read as 'no wallet here': login would fall into the device flow")
	}
	if _, err := getRootWalletAddress(); !errors.Is(err, rwagent.ErrE2EDefaultSocket) {
		t.Fatalf("the RootWallet path did not surface the guard's refusal: %v", err)
	}
}

func TestIsRootWalletInstalled_unreachableAgentIsNoWallet(t *testing.T) {
	t.Setenv(rwagent.E2EEnvVar, "")
	t.Setenv("RW_AGENT_SOCK", filepath.Join(t.TempDir(), "absent.sock"))
	if IsRootWalletInstalled() {
		t.Fatal("an agent that does not answer was reported as installed")
	}
}
