package rwagent

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The MsgSend of RootWallet's cross-language ORAMA vector
// (apps/desktop/src-tauri/tests/fixtures/orama_tx_vector.json): a SignDoc on
// orama-stagenet-2, signed by the all-"abandon" mnemonic at m/44'/118'/0'/0/0.
const (
	vectorSignDocHex   = "0ab1010a92010a1c2f636f736d6f732e62616e6b2e763162657461312e4d736753656e6412720a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a6775713061357475703073122c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a67757130613574757030731a140a066e6f72616d61120a31353030303030303030121a726f6f7477616c6c6574206f72616d6120747820766563746f7212680a500a460a1f2f636f736d6f732e63727970746f2e736563703235366b312e5075624b657912230a21024f4e2ad99c34d60b9ba6283c9431a8418af8673212961f97a77b6377fcd05b6212040a020801180312140a0e0a066e6f72616d6112043230303010c09a0c1a106f72616d612d73746167656e65742d322007"
	vectorSignatureHex = "2d4505ccba630b7179be4d26c6f362dbba9bd769e3606980dbb15ce87bfd8e61401ec28ac6bb9b555fe6ed834353f0f666853ba1f390edb417811198178cef6b"
	vectorPubKeyHex    = "024f4e2ad99c34d60b9ba6283c9431a8418af8673212961f97a77b6377fcd05b62"
	vectorAddress      = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex: %v", err)
	}
	return b
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// signedAnswer is the agent's success envelope for the given fields.
func signedAnswer(signature, pubKey, address string) string {
	return fmt.Sprintf(`{"ok":true,"data":{"signature":%q,"pubKey":%q,"address":%q}}`, signature, pubKey, address)
}

func TestSignOramaTx_Success(t *testing.T) {
	signDoc := mustHex(t, vectorSignDocHex)
	signature := mustHex(t, vectorSignatureHex)
	pubKey := mustHex(t, vectorPubKeyHex)

	var sent map[string]any
	client := agentStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/orama/tx/sign" {
			t.Errorf("request = %s %s, want POST /v1/orama/tx/sign", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &sent); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		rawJSON(200, signedAnswer(b64(signature), b64(pubKey), vectorAddress))(w, r)
	})

	got, err := client.SignOramaTx(context.Background(), signDoc)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if len(sent) != 1 || sent["signDoc"] != b64(signDoc) {
		t.Errorf("request body = %v, want exactly the base64 SignDoc", sent)
	}
	if hex.EncodeToString(got.Signature) != vectorSignatureHex ||
		hex.EncodeToString(got.PubKey) != vectorPubKeyHex ||
		got.Address != vectorAddress {
		t.Errorf("signature = %x / %x / %s, want the vector's", got.Signature, got.PubKey, got.Address)
	}
}

func TestSignOramaTx_Refused(t *testing.T) {
	client := agentStub(t, rawJSON(403, `{"ok":false,"error":"refusing to sign this ORAMA transaction: its chain id \"orama-stagenet-1\" is not an ORAMA chain this agent signs for","code":"ORAMA_TX_REFUSED"}`))

	sig, err := client.SignOramaTx(context.Background(), mustHex(t, vectorSignDocHex))
	if sig != nil || !IsOramaTxRefused(err) {
		t.Fatalf("sig = %v, err = %v, want ORAMA_TX_REFUSED", sig, err)
	}
	if IsRetryable(err) {
		t.Errorf("a refusal is not retryable: %v", err)
	}
	var ae *AgentError
	if !errors.As(err, &ae) || ae.StatusCode != 403 {
		t.Errorf("err = %#v, want the agent's 403 wrapped", err)
	}
	if !strings.Contains(err.Error(), "orama-stagenet-1") || !strings.Contains(err.Error(), "will not sign") {
		t.Errorf("the error should carry the agent's reason and the hint: %v", err)
	}
}

func TestSignOramaTx_ApprovalDenied(t *testing.T) {
	client := agentStub(t, rawJSON(403, `{"ok":false,"error":"the transaction was denied in RootWallet; nothing was signed","code":"APPROVAL_DENIED"}`))

	sig, err := client.SignOramaTx(context.Background(), mustHex(t, vectorSignDocHex))
	if sig != nil || !IsApprovalDenied(err) {
		t.Fatalf("sig = %v, err = %v, want APPROVAL_DENIED", sig, err)
	}
	if IsRetryable(err) {
		t.Errorf("a denial is not retryable: %v", err)
	}
}

func TestSignOramaTx_ApprovalTimeout(t *testing.T) {
	client := agentStub(t, rawJSON(403, `{"ok":false,"error":"nobody approved the transaction in time; nothing was signed","code":"APPROVAL_TIMEOUT"}`))

	sig, err := client.SignOramaTx(context.Background(), mustHex(t, vectorSignDocHex))
	if sig != nil || !IsApprovalTimeout(err) {
		t.Fatalf("sig = %v, err = %v, want APPROVAL_TIMEOUT", sig, err)
	}
	if !IsRetryable(err) {
		t.Errorf("an unanswered prompt is retryable: %v", err)
	}
}

// A 403 that is not the agent's envelope still means "not approved", and never
// yields a signature.
func TestSignOramaTx_NotApproved(t *testing.T) {
	client := agentStub(t, rawJSON(403, "forbidden\n"))

	sig, err := client.SignOramaTx(context.Background(), mustHex(t, vectorSignDocHex))
	if sig != nil || !IsApprovalDenied(err) {
		t.Fatalf("sig = %v, err = %v, want a not-approved error", sig, err)
	}
}

func TestSignOramaTx_Locked(t *testing.T) {
	client := agentStub(t, rawJSON(401, `{"ok":false,"error":"wallet is locked","code":"AGENT_LOCKED"}`))

	_, err := client.SignOramaTx(context.Background(), mustHex(t, vectorSignDocHex))
	if !IsLocked(err) {
		t.Fatalf("err = %v, want AGENT_LOCKED", err)
	}
	if !strings.Contains(err.Error(), "does not wait") {
		t.Errorf("a wallet route refuses at once, and the hint should say so: %v", err)
	}
}

func TestSignOramaTx_WrongResponseShape(t *testing.T) {
	signature := mustHex(t, vectorSignatureHex)
	pubKey := mustHex(t, vectorPubKeyHex)
	flipped := append([]byte(nil), signature...)
	flipped[10] ^= 0x01

	for name, body := range map[string]string{
		"signature not base64":    signedAnswer("***", b64(pubKey), vectorAddress),
		"pubKey not base64":       signedAnswer(b64(signature), "***", vectorAddress),
		"65-byte signature":       signedAnswer(b64(append(append([]byte(nil), signature...), 27)), b64(pubKey), vectorAddress),
		"uncompressed-size key":   signedAnswer(b64(signature), b64(make([]byte, 65)), vectorAddress),
		"not an ORAMA address":    signedAnswer(b64(signature), b64(pubKey), "cosmos19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"),
		"signature does not hold": signedAnswer(b64(flipped), b64(pubKey), vectorAddress),
		"no data":                 `{"ok":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			client := agentStub(t, rawJSON(200, body))
			sig, err := client.SignOramaTx(context.Background(), mustHex(t, vectorSignDocHex))
			if sig != nil || !errors.Is(err, errMalformedOramaTxSignature) {
				t.Fatalf("sig = %v, err = %v, want a malformed-signature error", sig, err)
			}
		})
	}
}

// The agent's signature is over the SignDoc it was sent; one that verifies over
// some other SignDoc is not an answer to this request.
func TestSignOramaTx_SignatureOverAnotherSignDoc(t *testing.T) {
	client := agentStub(t, rawJSON(200, signedAnswer(
		b64(mustHex(t, vectorSignatureHex)), b64(mustHex(t, vectorPubKeyHex)), vectorAddress)))

	other := append(mustHex(t, vectorSignDocHex), 0x00)
	if _, err := client.SignOramaTx(context.Background(), other); !errors.Is(err, errMalformedOramaTxSignature) {
		t.Fatalf("err = %v, want a malformed-signature error", err)
	}
}
