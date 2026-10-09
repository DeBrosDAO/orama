package auth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const acmeCleanupPath = "/v1/internal/acme/cleanup"

func acmeKey(t *testing.T) []byte {
	t.Helper()
	key, err := ACMEChallengeKey("a cluster secret")
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signedACME(t *testing.T, key, body []byte, now time.Time) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, acmeCleanupPath, bytes.NewReader(body))
	if err := SignACME(key, r, body, now); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestACME_aSignedCallVerifiesOnceOnly(t *testing.T) {
	key, now, body := acmeKey(t), time.Now(), []byte(`{"fqdn":"_acme-challenge.a.","value":"v"}`)
	r := signedACME(t, key, body, now)
	if !VerifyACME(key, r, body, now) {
		t.Fatal("first use refused")
	}
	if VerifyACME(key, r, body, now.Add(time.Second)) {
		t.Fatal("a captured cleanup replayed inside its window was accepted")
	}
}

func TestACME_eachSigningDrawsAFreshNonce(t *testing.T) {
	key, now, body := acmeKey(t), time.Now(), []byte(`{}`)
	for i := 0; i < 3; i++ {
		if !VerifyACME(key, signedACME(t, key, body, now), body, now) {
			t.Fatalf("call %d of Caddy renewing was refused", i)
		}
	}
}

func TestACME_aDifferentBodyIsRefused(t *testing.T) {
	key, now := acmeKey(t), time.Now()
	r := signedACME(t, key, []byte(`{"fqdn":"a"}`), now)
	if VerifyACME(key, r, []byte(`{"fqdn":"b"}`), now) {
		t.Fatal("a stamp verified over another record")
	}
}

func TestACME_aNoncedStampWithoutAUsableNonceIsRefused(t *testing.T) {
	key, now, body := acmeKey(t), time.Now(), []byte(`{}`)
	for name, nonce := range map[string]string{"missing": "", "not hex": "zz", "too short": "00ff"} {
		r := signedACME(t, key, body, now)
		if nonce == "" {
			r.Header.Del(ACMENonceHeader)
		} else {
			r.Header.Set(ACMENonceHeader, nonce)
		}
		if VerifyACME(key, r, body, now) {
			t.Errorf("a stamp with a %s nonce was accepted", name)
		}
	}
}

func TestACME_staleAndFutureStampsAreRefused(t *testing.T) {
	key, now, body := acmeKey(t), time.Now(), []byte(`{}`)
	if VerifyACME(key, signedACME(t, key, body, now), body, now.Add(2*coordinationMaxSkew)) {
		t.Error("a stale stamp verified")
	}
	if VerifyACME(key, signedACME(t, key, body, now), body, now.Add(-2*coordinationMaxSkew)) {
		t.Error("a future stamp verified")
	}
}

func TestACME_noKeyAndAnotherKeyAreRefused(t *testing.T) {
	key, now, body := acmeKey(t), time.Now(), []byte(`{}`)
	if VerifyACME(nil, signedACME(t, key, body, now), body, now) {
		t.Error("verified with no key")
	}
	other, err := ACMEChallengeKey("another cluster secret")
	if err != nil {
		t.Fatal(err)
	}
	if VerifyACME(other, signedACME(t, key, body, now), body, now) {
		t.Error("verified under another cluster's key")
	}
	if err := SignACME(nil, httptest.NewRequest(http.MethodPost, acmeCleanupPath, nil), body, now); err == nil {
		t.Error("signed with no key")
	}
}

// Rolling upgrade: a Caddy built before the nonce sends the unnonced stamp only.
func TestACME_anUnnoncedStampFromAnOlderCaddyIsAcceptedInTheMixedWindow(t *testing.T) {
	if !AcceptLegacyACMEMAC {
		t.Skip("the unnonced stamp is no longer accepted")
	}
	key, now, body := acmeKey(t), time.Now(), []byte(`{}`)
	r := signedACME(t, key, body, now)
	r.Header.Del(ACMEMACV2Header)
	r.Header.Del(ACMENonceHeader)
	if !VerifyACME(key, r, body, now) {
		t.Fatal("an older Caddy's stamp was refused")
	}
}

func TestACME_aFailedNoncedStampIsNotRetriedAsUnnonced(t *testing.T) {
	key, now, body := acmeKey(t), time.Now(), []byte(`{}`)
	r := signedACME(t, key, body, now)
	r.Header.Set(ACMEMACV2Header, "1.00ff")
	if VerifyACME(key, r, body, now) {
		t.Fatal("a broken nonced stamp verified through the unnonced one beside it")
	}
}

func TestACME_theUnnoncedMACIsNotAValidNoncedOne(t *testing.T) {
	key, now, body := acmeKey(t), time.Now(), []byte(`{}`)
	r := signedACME(t, key, body, now)
	r.Header.Set(ACMEMACV2Header, r.Header.Get(CoordinationMACHeader))
	if VerifyACME(key, r, body, now) {
		t.Fatal("the unnonced MAC verified as a nonced one")
	}
}
