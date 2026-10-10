package pace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
)

// fakeClock is a clock whose sleep advances it: a wait takes no real time.
type fakeClock struct {
	mu    sync.Mutex
	t     time.Time
	slept []time.Duration
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.slept = append(c.slept, d)
	c.mu.Unlock()
	return ctx.Err()
}

func newTestPacer(t *testing.T, b Budgets) (*Pacer, *fakeClock) {
	t.Helper()
	p, err := New(filepath.Join(t.TempDir(), FileName), b)
	if err != nil {
		t.Fatal(err)
	}
	c := newFakeClock()
	return p.WithClock(c.now, c.sleep), c
}

func lookupOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestBudgetsFromEnv_defaultsUnderProductLimits(t *testing.T) {
	b, err := BudgetsFromEnv(lookupOf(nil))
	if err != nil || b != DefaultBudgets() {
		t.Fatalf("b %+v err %v", b, err)
	}
	if b.Cred.PerMinute >= ProductCredPerMin || b.Cred.Burst >= ProductCredBurst ||
		b.Challenge.PerMinute >= ProductChallengePerMin || b.Challenge.Burst >= ProductChallengeBurst {
		t.Fatalf("defaults %+v are not under the product limits", b)
	}
}

func TestBudgetsFromEnv_overrides(t *testing.T) {
	b, err := BudgetsFromEnv(lookupOf(map[string]string{EnvCredPerMin: " 12 ", EnvChallengeBurst: "2", EnvCredBurst: ""}))
	if err != nil || b.Cred.PerMinute != 12 || b.Challenge.Burst != 2 || b.Cred.Burst != DefaultCredBurst {
		t.Fatalf("b %+v err %v", b, err)
	}
}

func TestBudgetsFromEnv_rejectsInvalid(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"zero":          {EnvCredPerMin: "0"},
		"negative":      {EnvChallengePerMin: "-1"},
		"garbage":       {EnvCredBurst: "lots"},
		"above product": {EnvCredPerMin: "31"},
		"above burst":   {EnvChallengeBurst: "6"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := BudgetsFromEnv(lookupOf(env)); err == nil {
				t.Fatalf("%v accepted", env)
			}
		})
	}
}

func TestFromEnv_outsideFleetIsNil(t *testing.T) {
	p, err := FromEnv(lookupOf(nil))
	if err != nil || p != nil {
		t.Fatalf("p %v err %v", p, err)
	}
	if err := p.Wait(context.Background(), BucketCred); err != nil {
		t.Fatalf("nil pacer waited: %v", err)
	}
	if err := p.Charge(BucketCred); err != nil {
		t.Fatalf("nil pacer charged: %v", err)
	}
}

func TestFromEnv_stateBesideFleetState(t *testing.T) {
	p, err := FromEnv(lookupOf(map[string]string{config.EnvState: "/run/work/state.json", EnvCredPerMin: "20"}))
	if err != nil {
		t.Fatal(err)
	}
	if p.path != "/run/work/"+FileName || p.Budgets().Cred.PerMinute != 20 {
		t.Fatalf("path %s budgets %+v", p.path, p.Budgets())
	}
	if _, err := FromEnv(lookupOf(map[string]string{config.EnvState: "/s.json", EnvCredPerMin: "99"})); err == nil {
		t.Fatal("budget over the product limit accepted")
	}
}

func TestNew_rejectsBadInput(t *testing.T) {
	if _, err := New("relative/pace.json", DefaultBudgets()); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := New("/abs/pace.json", Budgets{Cred: Budget{PerMinute: 1, Burst: 0}, Challenge: Budget{1, 1}}); err == nil {
		t.Fatal("zero burst accepted")
	}
}

func TestChallengeBucket_normalizesWallet(t *testing.T) {
	if ChallengeBucket(" 0xABcd ") != "challenge:0xabcd" {
		t.Fatal(ChallengeBucket(" 0xABcd "))
	}
}

func TestWait_burstThenRate(t *testing.T) {
	p, c := newTestPacer(t, Budgets{Cred: Budget{PerMinute: 6, Burst: 3}, Challenge: Budget{1, 1}})
	for i := 0; i < 3; i++ {
		if err := p.Wait(context.Background(), BucketCred); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.slept) != 0 {
		t.Fatalf("the burst waited: %v", c.slept)
	}
	start := c.now()
	if err := p.Wait(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	if got := c.now().Sub(start); got != 10*time.Second {
		t.Fatalf("the fourth token came after %s, want 10s (6/min)", got)
	}
}

func TestWait_bucketsAreIndependent(t *testing.T) {
	p, c := newTestPacer(t, Budgets{Cred: Budget{PerMinute: 1, Burst: 1}, Challenge: Budget{PerMinute: 1, Burst: 1}})
	ctx := context.Background()
	for _, bucket := range []string{BucketCred, ChallengeBucket("0x1"), ChallengeBucket("0x2")} {
		if err := p.Wait(ctx, bucket); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.slept) != 0 {
		t.Fatalf("distinct buckets shared tokens: %v", c.slept)
	}
	if err := p.Wait(ctx, BucketCred); err != nil || len(c.slept) == 0 {
		t.Fatalf("the spent address bucket did not make a caller wait (slept %v, err %v)", c.slept, err)
	}
}

func TestWait_contextEnds(t *testing.T) {
	p, _ := newTestPacer(t, Budgets{Cred: Budget{PerMinute: 1, Burst: 1}, Challenge: Budget{1, 1}})
	if err := p.Wait(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Wait(ctx, BucketCred); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func TestWait_realTimerHonoursContext(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), FileName), Budgets{Cred: Budget{PerMinute: 1, Burst: 1}, Challenge: Budget{1, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := p.Wait(ctx, BucketCred); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
}

func TestWait_unknownBucket(t *testing.T) {
	p, _ := newTestPacer(t, DefaultBudgets())
	for _, b := range []string{"", "challenge:", "other"} {
		if err := p.Wait(context.Background(), b); err == nil {
			t.Fatalf("bucket %q accepted", b)
		}
	}
}

func TestCharge_debtDelaysLaterWaits(t *testing.T) {
	p, c := newTestPacer(t, Budgets{Cred: Budget{PerMinute: 60, Burst: 1}, Challenge: Budget{1, 1}})
	for i := 0; i < 3; i++ {
		if err := p.Charge(BucketCred); err != nil {
			t.Fatal(err)
		}
	}
	start := c.now()
	if err := p.Wait(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	// Burst 1 minus three charges is -2; one token more needs 3s at 1/s.
	if got := c.now().Sub(start); got != 3*time.Second {
		t.Fatalf("waited %s, want 3s", got)
	}
}

func TestUpdate_corruptStateIsAnError(t *testing.T) {
	p, _ := newTestPacer(t, DefaultBudgets())
	if err := os.WriteFile(p.path, []byte("{not json"), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(context.Background(), BucketCred); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("err %v", err)
	}
}

func TestTake_prunesFullBuckets(t *testing.T) {
	p, c := newTestPacer(t, Budgets{Cred: Budget{PerMinute: 60, Burst: 2}, Challenge: Budget{60, 2}})
	ctx := context.Background()
	old, current := ChallengeBucket("0xold"), ChallengeBucket("0xnew")
	if err := p.Wait(ctx, old); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	if err := p.Wait(ctx, current); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), old) || !strings.Contains(string(raw), current) {
		t.Fatalf("state %s", raw)
	}
	info, err := os.Stat(p.path)
	if err != nil || info.Mode().Perm() != fileMode {
		t.Fatalf("mode %v err %v", info.Mode(), err)
	}
}
