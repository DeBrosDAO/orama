package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"time"
)

func TestWalletLimiter_allowsTheBurstThenRefuses(t *testing.T) {
	l := newWalletLimiter(60, 3)

	for i := 0; i < 3; i++ {
		if !l.allow("0xwallet") {
			t.Fatalf("request %d of the burst was refused", i+1)
		}
	}
	if l.allow("0xwallet") {
		t.Error("a fourth request got through a burst of three")
	}
}

// One wallet being ground must not stop anybody else signing in.
func TestWalletLimiter_isPerWallet(t *testing.T) {
	l := newWalletLimiter(60, 2)

	for i := 0; i < 5; i++ {
		l.allow("0xvictim")
	}
	if !l.allow("0xsomeoneelse") {
		t.Error("exhausting one wallet's budget refused another wallet")
	}
}

// The same wallet in different capitalisations is the same wallet, or the
// limit is one spelling away from useless.
func TestWalletLimiter_isCaseInsensitive(t *testing.T) {
	l := newWalletLimiter(60, 2)

	l.allow("0xWallet")
	l.allow("0xWALLET")
	if l.allow("0xwallet") {
		t.Error("changing the capitalisation reset the budget")
	}
}

func TestWalletLimiter_refillsOverTime(t *testing.T) {
	l := newWalletLimiter(60, 2) // one token per second
	now := time.Now()
	l.now = func() time.Time { return now }

	l.allow("0xwallet")
	l.allow("0xwallet")
	if l.allow("0xwallet") {
		t.Fatal("the burst was not exhausted")
	}

	now = now.Add(2 * time.Second)
	if !l.allow("0xwallet") {
		t.Error("the bucket never refilled")
	}
}

// A wallet idle for an hour must come back with a full budget and no more.
// Without the cap the bucket accumulates a token per second forever, so one
// quiet day buys tens of thousands of challenges in a burst.
func TestWalletLimiter_doesNotAccumulateWhileIdle(t *testing.T) {
	l := newWalletLimiter(60, 3) // one token per second, three at once
	now := time.Now()
	l.now = func() time.Time { return now }

	l.allow("0xwallet")
	now = now.Add(24 * time.Hour)

	allowed := 0
	for i := 0; i < 100; i++ {
		if l.allow("0xwallet") {
			allowed++
		}
	}

	if allowed > 3 {
		t.Errorf("a wallet idle for a day was allowed %d requests at once; the burst is 3, "+
			"so the bucket is accumulating without a ceiling", allowed)
	}
}

func TestWalletLimiter_forgetDropsIdleWallets(t *testing.T) {
	l := newWalletLimiter(60, 2)
	now := time.Now()
	l.now = func() time.Time { return now }

	l.allow("0xold")
	now = now.Add(time.Hour)
	l.allow("0xrecent")

	l.forget(30 * time.Minute)

	if _, present := l.buckets["0xold"]; present {
		t.Error("an idle wallet was kept, so the map grows without bound")
	}
	if _, present := l.buckets["0xrecent"]; !present {
		t.Error("an active wallet was dropped, resetting its budget")
	}
}

func TestWalletLimiter_anEmptyWalletIsNotLimited(t *testing.T) {
	l := newWalletLimiter(60, 1)

	l.allow("")
	if !l.allow("   ") {
		t.Error("empty wallets share a bucket, so one bad client would make " +
			"the 'wallet is required' error look like a rate limit")
	}
}

// The handler, not just the bucket: a challenge writes a nonce row for a wallet
// the caller does not have to own.
//
// The bucket is exhausted first, so no request in this test reaches the nonce
// write at all — which is the behaviour being asserted.
func TestChallengeHandler_refusesAnExhaustedWallet(t *testing.T) {
	limiter := newWalletLimiter(60, 2)
	limiter.allow("0xvictim")
	limiter.allow("0xvictim")

	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	h := &Handlers{authService: svc, challengeLimiter: limiter}

	body, _ := json.Marshal(ChallengeRequest{Wallet: "0xvictim"})
	w := httptest.NewRecorder()
	h.ChallengeHandler(w, httptest.NewRequest(http.MethodPost, "/v1/auth/challenge", bytes.NewReader(body)))

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After, so a client has nothing to back off by")
	}
}

// The limit has to be checked before the nonce is written, or the row it is
// meant to prevent is written anyway.
func TestChallengeHandler_limitsBeforeWritingTheNonce(t *testing.T) {
	src, err := os.ReadFile("challenge_handler.go")
	if err != nil {
		t.Fatalf("read challenge_handler.go: %v", err)
	}
	body := string(src)

	limit := strings.Index(body, "h.challengeLimiter.allow(")
	write := strings.Index(body, "h.authService.CreateNonce(")

	if limit < 0 {
		t.Fatal("the challenge handler does not consult the per-wallet limiter")
	}
	if write < 0 {
		t.Fatal("the challenge handler no longer writes a nonce; update this test")
	}
	if limit > write {
		t.Error("the nonce is written before the limit is checked, so the row the " +
			"limit exists to prevent is written anyway")
	}
}
