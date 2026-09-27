package monitor

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/auth"
)

func TestTokenCache_concurrentCallersShareOneFetch(t *testing.T) {
	var fetches atomic.Int32
	release := make(chan struct{})
	c := newTokenCache(func() (string, error) {
		fetches.Add(1)
		<-release
		return "tok", nil
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, err := c.get(); err != nil || tok != "tok" {
				t.Errorf("get = %q, %v", tok, err)
			}
		}()
	}
	close(release)
	wg.Wait()
	if n := fetches.Load(); n != 1 {
		t.Fatalf("%d fetches, want one renewal for all callers", n)
	}
}

func TestTokenCache_expiresInvalidatesAndDoesNotCacheErrors(t *testing.T) {
	now := time.Unix(1700000000, 0)
	calls := 0
	fail := true
	c := newTokenCache(func() (string, error) {
		calls++
		if fail {
			return "", errors.New("no credentials")
		}
		return fmt.Sprintf("tok-%d", calls), nil
	})
	c.now = func() time.Time { return now }

	if _, err := c.get(); err == nil {
		t.Fatal("a failed fetch returned a token")
	}
	fail = false
	first, _ := c.get()
	if again, _ := c.get(); again != first {
		t.Fatalf("a fresh token was not reused: %s then %s", first, again)
	}
	now = now.Add(tokenCacheTTL)
	if expired, _ := c.get(); expired == first {
		t.Fatal("an expired token was reused")
	}
	c.invalidate()
	if calls != 3 {
		t.Fatalf("calls = %d before invalidate's refetch", calls)
	}
	if _, err := c.get(); err != nil || calls != 4 {
		t.Fatalf("invalidate did not force a fetch: calls %d, %v", calls, err)
	}
}

func TestTokenError_transportIsUnavailableCredentialIsAuth(t *testing.T) {
	netErr := fmt.Errorf("reach https://gw/v1/auth/refresh: %w", &url.Error{Op: "Post", URL: "https://gw", Err: errors.New("connection refused")})
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"network failure while renewing", netErr, clierr.CodeUnavailable},
		{"gateway 503 while renewing", &auth.GatewayError{Status: 503, Message: "starting"}, clierr.CodeUnavailable},
		{"gateway refuses the key", &auth.GatewayError{Status: 401, Message: "invalid key"}, clierr.CodeAuth},
		{"no credential stored", errors.New("no credentials found for https://gw"), clierr.CodeAuth},
		{"session ended", fmt.Errorf("this session has ended (%w); run 'orama auth login'", netErr), clierr.CodeAuth},
	}
	for _, tc := range cases {
		got := tokenError("devnet", "https://gw", tc.err)
		if clierr.CodeOf(got) != tc.want {
			t.Errorf("%s: code %d, want %d: %v", tc.name, clierr.CodeOf(got), tc.want, got)
		}
		if tc.want == clierr.CodeAuth && !strings.Contains(got.Error(), "orama auth login") {
			t.Errorf("%s: no sign-in hint: %v", tc.name, got)
		}
	}
}
