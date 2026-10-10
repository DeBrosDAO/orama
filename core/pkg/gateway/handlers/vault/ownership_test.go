package vault

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

func testIdentity(t *testing.T) (identity, pubHex string, priv ed25519.PrivateKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:]), hex.EncodeToString(pub), key
}

func TestVerifyPushPullRoundTrip(t *testing.T) {
	identity, pubHex, priv := testIdentity(t)

	pushMsg := fmt.Sprintf("vault-push-v1:%s:%d", identity, 7)
	pushSig := hex.EncodeToString(ed25519.Sign(priv, []byte(pushMsg)))
	if !verifyPush(identity, 7, pubHex, pushSig) {
		t.Fatal("valid push signature rejected")
	}
	if verifyPush(identity, 8, pubHex, pushSig) {
		t.Fatal("push signature accepted for a different version")
	}

	const ts int64 = 1_700_000_000
	pullMsg := fmt.Sprintf("vault-pull-v1:%s:%d", identity, ts)
	pullSig := hex.EncodeToString(ed25519.Sign(priv, []byte(pullMsg)))
	if !verifyPull(identity, ts, ts+10, pubHex, pullSig) {
		t.Fatal("valid pull signature rejected")
	}
	if verifyPull(identity, ts, ts+10_000, pubHex, pullSig) {
		t.Fatal("stale pull signature accepted")
	}
}

func TestVerifySecretOwnership(t *testing.T) {
	identity, pubHex, priv := testIdentity(t)
	const ts int64 = 1_700_000_000

	putMsg := fmt.Sprintf("vault-secret-put-v1:%s:%s:%d", identity, "api-key", 3)
	putSig := hex.EncodeToString(ed25519.Sign(priv, []byte(putMsg)))
	if !verifySecretPut(identity, "api-key", 3, pubHex, putSig) {
		t.Fatal("valid put signature rejected")
	}
	if verifySecretPut(identity, "api-key", 4, pubHex, putSig) {
		t.Fatal("put signature accepted for a different version")
	}
	if verifySecretPut(identity, "other", 3, pubHex, putSig) {
		t.Fatal("put signature accepted for a different name")
	}

	getMsg := fmt.Sprintf("vault-secret-get-v1:%s:%s:%d", identity, "api-key", ts)
	getSig := hex.EncodeToString(ed25519.Sign(priv, []byte(getMsg)))
	if !verifySecretGet(identity, "api-key", ts, ts+10, pubHex, getSig) {
		t.Fatal("valid get signature rejected")
	}
	if verifySecretGet(identity, "api-key", ts, ts+10_000, pubHex, getSig) {
		t.Fatal("stale get signature accepted")
	}

	delMsg := fmt.Sprintf("vault-secret-delete-v1:%s:%s:%d", identity, "api-key", ts)
	delSig := hex.EncodeToString(ed25519.Sign(priv, []byte(delMsg)))
	if !verifySecretDelete(identity, "api-key", ts, ts, pubHex, delSig) {
		t.Fatal("valid delete signature rejected")
	}

	listMsg := fmt.Sprintf("vault-secret-list-v1:%s:%d", identity, ts)
	listSig := hex.EncodeToString(ed25519.Sign(priv, []byte(listMsg)))
	if !verifySecretList(identity, ts, ts, pubHex, listSig) {
		t.Fatal("valid list signature rejected")
	}
}

func TestIdentityMustMatchPubkey(t *testing.T) {
	_, pubHex, priv := testIdentity(t)
	wrong := hex.EncodeToString(make([]byte, 32))
	msg := fmt.Sprintf("vault-push-v1:%s:%d", wrong, 1)
	sig := hex.EncodeToString(ed25519.Sign(priv, []byte(msg)))
	if verifyPush(wrong, 1, pubHex, sig) {
		t.Fatal("signature accepted for an identity that is not SHA-256(pubkey)")
	}
}
