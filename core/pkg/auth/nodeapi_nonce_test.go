package auth

import (
	"net/http"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

const nodeRegisterPath = "/v1/internal/node/heartbeat"

func TestNodeAPI_aStampIsGoodForOneCall(t *testing.T) {
	now, body := time.Now(), []byte(`{"ip_address":"1.2.3.4"}`)
	r := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
	if _, _, ok := VerifyNodeAPI(testVerifier, r, body, now); !ok {
		t.Fatal("first use refused")
	}
	if _, _, ok := VerifyNodeAPI(testVerifier, r, body, now.Add(time.Second)); ok {
		t.Fatal("a captured stamp was accepted a second time inside its window")
	}
}

func TestNodeAPI_eachSigningDrawsAFreshNonce(t *testing.T) {
	now, body := time.Now(), []byte(`{}`)
	for i := 0; i < 3; i++ {
		r := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
		if _, _, ok := VerifyNodeAPI(testVerifier, r, body, now); !ok {
			t.Fatalf("call %d of a node heartbeating was refused", i)
		}
	}
}

func TestNodeAPI_aNoncedStampWithoutAUsableNonceIsRefused(t *testing.T) {
	now, body := time.Now(), []byte(`{}`)
	for name, nonce := range map[string]string{"missing": "", "not hex": "zz", "too short": "00ff"} {
		r := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
		if nonce == "" {
			r.Header.Del(NodeNonceHeader)
		} else {
			r.Header.Set(NodeNonceHeader, nonce)
		}
		if _, _, ok := VerifyNodeAPI(testVerifier, r, body, now); ok {
			t.Errorf("a stamp with a %s nonce was accepted", name)
		}
	}
}

func TestNodeAPI_aSwappedNonceInvalidatesTheStamp(t *testing.T) {
	now, body := time.Now(), []byte(`{}`)
	r := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
	other := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
	r.Header.Set(NodeNonceHeader, other.Header.Get(NodeNonceHeader))
	if _, _, ok := VerifyNodeAPI(testVerifier, r, body, now); ok {
		t.Fatal("a stamp verified under a nonce it was not made with")
	}
}

// Rolling upgrade: a node on the previous build stamps the unnonced form only.
func TestNodeAPI_anUnnoncedStampFromAnOldNodeIsAcceptedInTheMixedWindow(t *testing.T) {
	if !AcceptLegacyNodeStamp {
		t.Skip("the unnonced stamp is no longer accepted")
	}
	now, body := time.Now(), []byte(`{}`)
	r := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
	r.Header.Del(NodeStampV2Header)
	r.Header.Del(NodeNonceHeader)
	if id, _, ok := VerifyNodeAPI(testVerifier, r, body, now); !ok || id != "node-a" {
		t.Fatalf("an old node's stamp was refused: %q %v", id, ok)
	}
}

func TestNodeAPI_aFailedNoncedStampIsNotRetriedAsUnnonced(t *testing.T) {
	now, body := time.Now(), []byte(`{}`)
	r := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
	r.Header.Set(NodeStampV2Header, "1.00ff")
	if _, _, ok := VerifyNodeAPI(testVerifier, r, body, now); ok {
		t.Fatal("a broken nonced stamp verified through the unnonced one beside it")
	}
}

func TestNodeAPI_theUnnoncedSignatureIsNotAValidNoncedOne(t *testing.T) {
	now, body := time.Now(), []byte(`{}`)
	r := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
	r.Header.Set(NodeStampV2Header, r.Header.Get(NodeStampHeader))
	if _, _, ok := VerifyNodeAPI(testVerifier, r, body, now); ok {
		t.Fatal("the unnonced signature verified as a nonced one")
	}
}

// Enrolment is checked against the key inside a peer id, which any machine can
// produce for an id it generated; its nonces must not share a cache with the
// heartbeats of enrolled nodes.
func TestNodeAPI_enrolmentNoncesAreRememberedApartFromHeartbeats(t *testing.T) {
	priv, pub, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := NodeIdentityVerifier(id.String())
	if err != nil {
		t.Fatal(err)
	}
	if nodeAPIReplaysFor(identity) == nodeAPIReplaysFor(testVerifierValue()) {
		t.Fatal("identity-verified calls share a replay cache with registered-key calls")
	}
	now, body := time.Now(), []byte(`{}`)
	r := httpRequestSignedBy(t, NodeIdentitySigner(priv), id.String(), body, now)
	verifierFor := func(string) (NodeStampVerifier, error) { return identity, nil }
	if _, _, ok := VerifyNodeAPI(verifierFor, r, body, now); !ok {
		t.Fatal("a first enrolment was refused")
	}
	if _, _, ok := VerifyNodeAPI(verifierFor, r, body, now); ok {
		t.Fatal("a replayed enrolment was accepted")
	}
}

func testVerifierValue() NodeStampVerifier {
	v, _ := testVerifier("")
	return v
}

func httpRequestSignedBy(t *testing.T, signer NodeStampSigner, nodeID string, body []byte, now time.Time) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, "http://localhost"+nodeRegisterPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := SignNodeAPI(signer, r, nodeID, body, now); err != nil {
		t.Fatal(err)
	}
	return r
}
