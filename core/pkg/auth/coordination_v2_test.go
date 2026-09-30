package auth

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func v2Key(t *testing.T) []byte {
	t.Helper()
	key, err := CoordinationKey("a cluster secret")
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signedPost(t *testing.T, key []byte, body string, now time.Time) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/namespace/spawn", bytes.NewReader([]byte(body)))
	if err := SignCoordination(key, r, now); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCheckCoordination_signedRequestIsV2AndBodyIsRestored(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := signedPost(t, key, `{"action":"stop-rqlite"}`, now)
	ver, ok := CheckCoordination(key, r, now)
	if !ok || ver != CoordinationV2 {
		t.Fatalf("got version %d ok=%v, want v2 true", ver, ok)
	}
	got, _ := io.ReadAll(r.Body)
	if string(got) != `{"action":"stop-rqlite"}` {
		t.Fatalf("body after verify = %q", got)
	}
}

func TestCheckCoordination_bodySubstitutionRefused(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := signedPost(t, key, `{"action":"stop-rqlite","namespace":"mine"}`, now)
	r.Body = io.NopCloser(bytes.NewReader([]byte(`{"action":"teardown-namespace","namespace":"victim"}`)))
	if ver, ok := CheckCoordination(key, r, now); ok {
		t.Fatalf("a swapped body verified (version %d), and v1 must not be a fallback", ver)
	}
}

func TestCheckCoordination_replayedNonceRefused(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := signedPost(t, key, `{}`, now)
	if !VerifyCoordination(key, r, now) {
		t.Fatal("first use refused")
	}
	again := signedPost(t, key, `{}`, now)
	again.Header = r.Header.Clone()
	if VerifyCoordination(key, again, now.Add(time.Second)) {
		t.Fatal("the same nonce verified twice")
	}
}

func TestCheckCoordination_staleAndFutureRefused(t *testing.T) {
	key, now := v2Key(t), time.Now()
	if VerifyCoordination(key, signedPost(t, key, `{}`, now), now.Add(2*coordinationMaxSkew)) {
		t.Error("a stale stamp verified")
	}
	if VerifyCoordination(key, signedPost(t, key, `{}`, now), now.Add(-2*coordinationMaxSkew)) {
		t.Error("a future stamp verified")
	}
}

func TestCheckCoordination_v1OnlyStampIsV1(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := signedPost(t, key, `{}`, now)
	r.Header.Del(CoordinationMACV2Header)
	r.Header.Del(CoordinationNonceHeader)
	ver, ok := CheckCoordination(key, r, now)
	if !ok || ver != CoordinationV1 {
		t.Fatalf("got version %d ok=%v, want v1 true", ver, ok)
	}
}

func TestCheckCoordination_v2StampWithoutItsNonceRefused(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := signedPost(t, key, `{}`, now)
	r.Header.Del(CoordinationNonceHeader)
	if VerifyCoordination(key, r, now) {
		t.Fatal("a v2 stamp without a nonce verified")
	}
}

func TestCheckCoordination_bodyOverTheCapRefused(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := signedPost(t, key, string(make([]byte, CoordinationMaxBody+1)), now)
	if VerifyCoordination(key, r, now) {
		t.Fatal("a body over the cap verified")
	}
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestSignCoordination_unreadableBodyRefused(t *testing.T) {
	key := v2Key(t)
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Body = io.NopCloser(failingBody{})
	if err := SignCoordination(key, r, time.Now()); err == nil {
		t.Fatal("signed a body that could not be read")
	}
}

func TestSignCoordination_bodyStillSendable(t *testing.T) {
	key := v2Key(t)
	r := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte("payload")))
	if err := SignCoordination(key, r, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r.Body)
	again, _ := r.GetBody()
	second, _ := io.ReadAll(again)
	if string(got) != "payload" || string(second) != "payload" {
		t.Fatalf("body after signing = %q / %q", got, second)
	}
}

func TestSignCoordination_emptyBodyRoundTrips(t *testing.T) {
	key, now := v2Key(t), time.Now()
	r := httptest.NewRequest(http.MethodGet, "/v1/network/status", nil)
	if err := SignCoordination(key, r, now); err != nil {
		t.Fatal(err)
	}
	if ver, ok := CheckCoordination(key, r, now); !ok || ver != CoordinationV2 {
		t.Fatalf("got %d %v", ver, ok)
	}
}

func TestReplayCache_boundedAndExpires(t *testing.T) {
	c := newReplayCache(2, time.Minute)
	now := time.Now()
	if !c.firstUse("a", now) || !c.firstUse("b", now) {
		t.Fatal("fresh nonces refused")
	}
	if c.firstUse("c", now) {
		t.Fatal("a full cache of live nonces accepted another")
	}
	if c.firstUse("a", now) {
		t.Fatal("a duplicate accepted")
	}
	if !c.firstUse("c", now.Add(2*time.Minute)) {
		t.Fatal("expired entries were not released")
	}
	if !c.firstUse("a", now.Add(2*time.Minute)) {
		t.Fatal("an expired nonce stayed remembered")
	}
}

func TestVerifyCoordinationV2_refusesV1Only(t *testing.T) {
	key := v2Key(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/secrets/reencrypt", bytes.NewReader([]byte(`{"root":"attacker"}`)))
	if err := SignCoordination(key, r, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !VerifyCoordination(key, r, time.Now()) {
		t.Fatal("a fully stamped request does not verify")
	}
	r2 := httptest.NewRequest(http.MethodPost, "/v1/internal/secrets/reencrypt", bytes.NewReader([]byte(`{"root":"attacker"}`)))
	r2.Header = r.Header.Clone()
	r2.Header.Del(CoordinationMACV2Header)
	r2.Header.Del(CoordinationNonceHeader)
	if !VerifyCoordination(key, r2, time.Now()) {
		t.Fatal("v1 stamp no longer verifies under VerifyCoordination")
	}
	if VerifyCoordinationV2(key, r2, time.Now()) {
		t.Fatal("a stripped-v2 request verified under VerifyCoordinationV2")
	}
}

func TestVerifyCoordinationV2_bindsTheRecipientHost(t *testing.T) {
	key := v2Key(t)
	r := httptest.NewRequest(http.MethodPost, "http://10.0.0.1:6001/x", bytes.NewReader([]byte("b")))
	if err := SignCoordination(key, r, time.Now()); err != nil {
		t.Fatal(err)
	}
	r.Host = "10.0.0.2:6001"
	if VerifyCoordinationV2(key, r, time.Now()) {
		t.Fatal("a stamp signed for 10.0.0.1 verified at 10.0.0.2")
	}
	r.Host = "10.0.0.1:6001"
	if !VerifyCoordinationV2(key, r, time.Now()) {
		t.Fatal("the stamp does not verify at the host it was signed for")
	}
}
