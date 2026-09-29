package gw

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/pace"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// clock is a fake clock whose sleep advances it.
type clock struct {
	mu    sync.Mutex
	t     time.Time
	slept time.Duration
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *clock) sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.t, c.slept = c.t.Add(d), c.slept+d
	c.mu.Unlock()
	return ctx.Err()
}

func (c *clock) total() time.Duration { c.mu.Lock(); defer c.mu.Unlock(); return c.slept }

// statusServer answers every request with status and counts requests by path.
func statusServer(t *testing.T, status int) (*httptest.Server, *sync.Map) {
	t.Helper()
	var hits sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := hits.LoadOrStore(r.URL.Path, new(int))
		*n.(*int)++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"code":"RATE_LIMITED","message":"m"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// pacedClient is a client for url paced by a one-token-a-minute pacer on a
// fake clock.
func pacedClient(t *testing.T, url string) (*Client, *clock, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), pace.FileName)
	p, err := pace.New(path, pace.Budgets{Cred: pace.Budget{PerMinute: 1, Burst: 1}, Challenge: pace.Budget{PerMinute: 1, Burst: 1}})
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	c, err := NewWithTLS(url, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.WithPacer(p.WithClock(clk.now, clk.sleep)), clk, path
}

func TestDo_credentialRoutesWaitOnPacer(t *testing.T) {
	srv, _ := statusServer(t, http.StatusOK)
	c, clk, _ := pacedClient(t, srv.URL)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := c.Send(ctx, Req{Method: http.MethodPost, Path: PathToken}); err != nil {
			t.Fatal(err)
		}
	}
	if clk.total() != time.Minute {
		t.Fatalf("two credential requests at 1/min slept %s, want 1m", clk.total())
	}
	for i := 0; i < 3; i++ {
		if _, err := c.Send(ctx, Req{Path: PathWhoami}); err != nil {
			t.Fatal(err)
		}
	}
	if clk.total() != time.Minute {
		t.Fatalf("a non-credential route was paced (slept %s)", clk.total())
	}
}

func TestChallenge_pacesTheWalletBucket(t *testing.T) {
	srv, _ := statusServer(t, http.StatusOK)
	c, clk, path := pacedClient(t, srv.URL)
	ctx := context.Background()
	if _, err := c.JSON(ctx, http.MethodPost, PathChallenge, "", ChallengeRequest{Wallet: "0xAbC"}, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "challenge:0xabc") || !strings.Contains(string(raw), "|cred") {
		t.Fatalf("state %s err %v", raw, err)
	}
	// A garbage body names no wallet: only the address bucket is spent.
	if _, err := c.Send(ctx, Req{Method: http.MethodPost, Path: PathChallenge, Body: []byte("{")}); err != nil {
		t.Fatal(err)
	}
	if clk.total() != time.Minute {
		t.Fatalf("slept %s, want 1m (the address bucket only)", clk.total())
	}
}

func TestDo_pacedTooManyRequestsIsPacingError(t *testing.T) {
	srv, _ := statusServer(t, http.StatusTooManyRequests)
	c, _, _ := pacedClient(t, srv.URL)
	resp, err := c.Send(context.Background(), Req{Method: http.MethodPost, Path: PathRefresh})
	var pe *PacingError
	var se *StatusError
	if !errors.As(err, &pe) || !errors.As(err, &se) || se.Status != http.StatusTooManyRequests {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(err.Error(), "pacing exceeded") || resp == nil || resp.Status != http.StatusTooManyRequests {
		t.Fatalf("err %v resp %+v", err, resp)
	}
	if _, _, err := c.Token(context.Background(), "ak"); !errors.As(err, &pe) {
		t.Fatalf("Token err %v", err)
	}
}

func TestUnpaced_noWaitAndPlain429(t *testing.T) {
	srv, hits := statusServer(t, http.StatusTooManyRequests)
	c, clk, _ := pacedClient(t, srv.URL)
	u := c.Unpaced()
	for i := 0; i < 5; i++ {
		resp, err := u.Send(context.Background(), Req{Method: http.MethodPost, Path: PathVerify})
		if err != nil || resp.Status != http.StatusTooManyRequests {
			t.Fatalf("resp %+v err %v", resp, err)
		}
	}
	n, _ := hits.Load(PathVerify)
	if clk.total() != 0 || *n.(*int) != 5 {
		t.Fatalf("slept %s hits %d", clk.total(), *n.(*int))
	}
	if c.unpaced {
		t.Fatal("Unpaced changed the original client")
	}
	var cfg userConfig
	Unpaced()(&cfg)
	if !cfg.unpaced {
		t.Fatal("the Unpaced user option did nothing")
	}
}

func TestDo_waitHonoursContext(t *testing.T) {
	srv, hits := statusServer(t, http.StatusOK)
	c, _, _ := pacedClient(t, srv.URL)
	if _, err := c.Send(context.Background(), Req{Method: http.MethodPost, Path: PathAPIKey}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Send(ctx, Req{Method: http.MethodPost, Path: PathAPIKey}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if n, _ := hits.Load(PathAPIKey); *n.(*int) != 1 {
		t.Fatalf("a request whose pacing wait was cancelled was sent (%d hits)", *n.(*int))
	}
}

func TestRaw_credentialRequestLinePaced(t *testing.T) {
	c, _, _ := startGateway(t)
	path := filepath.Join(t.TempDir(), pace.FileName)
	p, err := pace.New(path, pace.DefaultBudgets())
	if err != nil {
		t.Fatal(err)
	}
	c = c.WithPacer(p)
	if _, err := c.Raw(context.Background(), []byte("POST /v1/auth/verify?x=1 HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(path); err != nil || !strings.Contains(string(raw), "|cred") {
		t.Fatalf("raw credential request not paced: %s %v", raw, err)
	}
}

func TestRawRequestPath_forms(t *testing.T) {
	for in, want := range map[string]string{
		"POST /v1/auth/token HTTP/1.1\r\n": PathToken, "GET /v1/auth/refresh?a=b HTTP/1.1\n": PathRefresh,
		"garbage": "", "": "",
	} {
		if got := rawRequestPath([]byte(in)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	if !IsCredentialPath(PathDeviceToken) || IsCredentialPath(PathLogout) {
		t.Fatal("credential route set")
	}
}

func TestNewWithTLS_pacerFromFleetEnv(t *testing.T) {
	t.Setenv(config.EnvState, filepath.Join(t.TempDir(), "state.json"))
	c, err := NewWithTLS("https://gw.example", nil, nil)
	if err == nil {
		t.Fatal("fleet client without TLS accepted")
	}
	c, err = NewWithTLS("https://gw.example", testTLS(), nil)
	if err != nil || c.pacer == nil {
		t.Fatalf("fleet client is not paced: %v", err)
	}
	t.Setenv(pace.EnvCredPerMin, "1000")
	if _, err := NewWithTLS("https://gw.example", testTLS(), nil); err == nil {
		t.Fatal("a budget over the product limit accepted")
	}
}

func TestProtect_registersAndRefusesEmpty(t *testing.T) {
	rec, err := evidence.New(filepath.Join(t.TempDir(), "ev"), "gw", secrets.NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewWithTLS("http://127.0.0.1:1", nil, rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Protect("externally-minted-secret-1"); err != nil {
		t.Fatal(err)
	}
	if got := rec.Redactor().Redact("x externally-minted-secret-1 y"); strings.Contains(got, "externally-minted") {
		t.Fatalf("not redacted: %s", got)
	}
	if err := c.Protect("  "); err == nil {
		t.Fatal("empty secret accepted")
	}
}
