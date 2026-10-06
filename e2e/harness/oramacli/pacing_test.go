package oramacli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/pace"
	"github.com/DeBrosOfficial/network/pkg/auth"
)

const testGatewayHost = "e2e-x.dbrsteting.bid"

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

// pacedRunner is newRunner with a fake-clock pacer at one token a minute.
func pacedRunner(t *testing.T, credBurst, challengeBurst int) (*Runner, *clock) {
	t.Helper()
	r, _ := newRunner(t)
	p, err := pace.New(filepath.Join(t.TempDir(), pace.FileName), pace.Budgets{
		Cred: pace.Budget{PerMinute: 1, Burst: credBurst}, Challenge: pace.Budget{PerMinute: 1, Burst: challengeBurst}})
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	r.Pacer, r.GatewayHost, r.Wallet = p.WithClock(clk.now, clk.sleep), testGatewayHost, "0xOperator"
	return r, clk
}

func TestCommandCost_classification(t *testing.T) {
	cases := map[string]struct {
		args     []string
		noWallet bool
		want     cost
	}{
		"wallet login":       {[]string{"auth", "login", "--namespace", "a"}, false, cost{cred: 2, challenge: true, known: true}},
		"flag first":         {[]string{"--env", "e2e-x", "auth", "login"}, false, cost{cred: 2, challenge: true, known: true}},
		"device login":       {[]string{"auth", "login"}, true, cost{cred: 1, device: true, known: true}},
		"approve":            {[]string{"auth", "approve", "ABCD"}, false, cost{cred: 2, challenge: true, known: true}},
		"approve no wallet":  {[]string{"auth", "approve"}, true, cost{known: true}},
		"other auth command": {[]string{"auth", "whoami"}, false, cost{}},
		"namespace":          {[]string{"namespace", "list", "--json"}, false, cost{}},
		"empty":              {nil, false, cost{}},
		"auth last":          {[]string{"auth"}, false, cost{}},
	}
	for name, c := range cases {
		if got := commandCost(c.args, c.noWallet); got != c.want {
			t.Errorf("%s: got %+v want %+v", name, got, c.want)
		}
	}
}

func TestRun_walletLoginWaitsOnBothBuckets(t *testing.T) {
	r, clk := pacedRunner(t, 2, 1)
	for i := 0; i < 2; i++ {
		if res, err := r.Run(context.Background(), "auth", "login"); err != nil || res.Exit != 0 {
			t.Fatalf("res %+v err %v", res, err)
		}
	}
	// Login 1 spends the challenge token and both address tokens. Login 2
	// waits 1m for the challenge bucket, takes the address token that minute
	// earned, and waits 1m more for the second.
	if clk.total() != 2*time.Minute {
		t.Fatalf("slept %s, want 2m", clk.total())
	}
}

func TestRun_refusesUnpaceableRunner(t *testing.T) {
	r, _ := pacedRunner(t, 2, 1)
	r.GatewayHost = ""
	if _, err := r.Run(context.Background(), "version"); err == nil || !strings.Contains(err.Error(), "GatewayHost") {
		t.Fatalf("err %v", err)
	}
	r, _ = pacedRunner(t, 2, 1)
	r.Wallet = ""
	if _, err := r.Run(context.Background(), "auth", "approve", "X"); err == nil || !strings.Contains(err.Error(), "Wallet") {
		t.Fatalf("err %v", err)
	}
	if res, err := r.Run(context.Background(), "version"); err != nil || res.Exit != 0 {
		t.Fatalf("a command that signs nothing needs no Wallet: %+v %v", res, err)
	}
}

func TestRun_sessionRenewalChargedAfterwards(t *testing.T) {
	r, clk := pacedRunner(t, 1, 1)
	script := "#!/bin/sh\nif [ \"$1\" = renew ]; then mkdir -p \"$HOME/.orama\"; echo \"$2\" > \"$HOME/.orama/credentials.json\"; fi\n"
	if err := os.WriteFile(r.Bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, args := range [][]string{{"version"}, {"renew", "v1"}, {"renew", "v1"}} {
		if _, err := r.Run(ctx, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Only the first renewal changed the file: one charge, which took the
	// only token, so the next Wait sleeps a minute.
	if err := r.Pacer.Wait(ctx, testGatewayHost, pace.BucketCred); err != nil {
		t.Fatal(err)
	}
	if clk.total() != time.Minute {
		t.Fatalf("slept %s, want 1m (exactly one renewal charged)", clk.total())
	}
}

func TestRun_pacerFromFleetEnv(t *testing.T) {
	t.Setenv(config.EnvState, filepath.Join(t.TempDir(), "state.json"))
	r, _ := newRunner(t)
	if _, err := r.Run(context.Background(), "version"); err == nil {
		t.Fatal("a fleet-mode runner without GatewayHost ran")
	}
	r.GatewayHost = testGatewayHost
	if res, err := r.Run(context.Background(), "version"); err != nil || res.Exit != 0 {
		t.Fatalf("res %+v err %v", res, err)
	}
	t.Setenv(pace.EnvChallengePerMin, "x")
	if _, err := r.Run(context.Background(), "version"); err == nil {
		t.Fatal("an invalid pacing budget was ignored")
	}
}

func TestPollCounter_feed(t *testing.T) {
	var c pollCounter
	chunks := []string{"good for 10m0s. Wai", "ting", ".", "..", "\nAuthentication successful."}
	total := 0
	for _, ch := range chunks {
		total += c.feed([]byte(ch))
	}
	if total != 3 {
		t.Fatalf("counted %d polls, want 3", total)
	}
	var none pollCounter
	if none.feed([]byte("no marker... at all.\n")) != 0 {
		t.Fatal("dots before the marker counted")
	}
}

func TestForState_setsPacingFields(t *testing.T) {
	st := &fleet.State{OramaBin: "/b", Home: "/h", RWSock: "/s", GatewayURL: "https://" + testGatewayHost, OperatorAddress: "0xOp",
		PreviousOramaBin: "/p"}
	for _, r := range []*Runner{ForState(st, nil), ForPreviousRelease(t, st, nil)} {
		if r.GatewayHost != testGatewayHost || r.Wallet != "0xOp" {
			t.Fatalf("runner %+v", r)
		}
	}
	if gatewayHost("::bad") != "" {
		t.Fatal("unparseable URL has a host")
	}
}

// A key in ORAMA_TOKEN is exchanged on /v1/auth/token before the command runs;
// that request changes no file, so it was never charged, and a test running
// eleven commands with a key in a row spent eleven unpaced credential requests
// and was refused 429 (stagenet, 2026-09-30).
func TestRun_envTokenKeyExchangeChargedBeforehand(t *testing.T) {
	r, clk := pacedRunner(t, 1, 1)
	r.Env = append(r.Env, auth.TokenEnvVar+"=orama_rk_garbage")
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := r.Run(ctx, "namespace", "list"); err != nil {
			t.Fatal(err)
		}
	}
	// One token a minute, burst one: the first exchange takes it, the second
	// waits a minute for the next.
	if clk.total() != time.Minute {
		t.Fatalf("slept %s, want 1m (one exchange charged per command)", clk.total())
	}
}

// A token in ORAMA_TOKEN is sent as it is: nothing is exchanged or charged.
// RunOpts.Env overrides the runner's Env, as exec does with a later value.
func TestRun_envTokenJWTIsNotCharged(t *testing.T) {
	r, clk := pacedRunner(t, 1, 1)
	r.Env = append(r.Env, auth.TokenEnvVar+"=orama_rk_key")
	ctx := context.Background()
	jwt := RunOpts{Env: []string{auth.TokenEnvVar + "=eyJhbGciOi.eyJzdWIiOi.c2ln"}}
	for i := 0; i < 3; i++ {
		if _, err := r.RunWith(ctx, jwt, "namespace", "list"); err != nil {
			t.Fatal(err)
		}
	}
	if clk.total() != 0 {
		t.Fatalf("slept %s for commands that exchange nothing", clk.total())
	}
}

func TestExchangesEnvToken(t *testing.T) {
	cases := map[string]struct {
		env  []string
		want bool
	}{
		"none":         {nil, false},
		"key":          {[]string{"ORAMA_TOKEN=orama_rk_x"}, true},
		"garbage":      {[]string{"ORAMA_TOKEN=not-a-credential"}, true},
		"jwt":          {[]string{"ORAMA_TOKEN=eyA.eyB.sig"}, false},
		"empty":        {[]string{"ORAMA_TOKEN="}, false},
		"later wins":   {[]string{"ORAMA_TOKEN=orama_rk_x", "ORAMA_TOKEN=eyA.eyB.sig"}, false},
		"similar name": {[]string{"ORAMA_TOKEN_X=orama_rk_x"}, false},
	}
	for name, c := range cases {
		if got := exchangesEnvToken(c.env); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
