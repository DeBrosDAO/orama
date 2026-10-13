package pace

import (
	"context"
	"testing"
	"time"
)

func TestWaitFull_fullBucketTakenAtOnce(t *testing.T) {
	p, c := newTestPacer(t, DefaultBudgets())
	if err := p.WaitFull(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	if len(c.slept) != 0 {
		t.Fatalf("a fresh (full) bucket waited %v", c.slept)
	}
	// The whole burst is held: the next paced call waits a token's refill.
	if err := p.Wait(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	perToken := time.Minute / time.Duration(DefaultCredPerMin)
	if len(c.slept) != 1 || c.slept[0] < perToken-time.Millisecond {
		t.Fatalf("after WaitFull the next token came after %v, want about %v", c.slept, perToken)
	}
}

func TestWaitFull_waitsForAWholeRefill(t *testing.T) {
	p, c := newTestPacer(t, DefaultBudgets())
	for range 3 {
		if err := p.Wait(context.Background(), BucketCred); err != nil {
			t.Fatal(err)
		}
	}
	start := c.now()
	if err := p.WaitFull(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	waited := c.now().Sub(start)
	want := 3 * time.Minute / time.Duration(DefaultCredPerMin)
	if waited < want-time.Millisecond || waited > want+time.Second {
		t.Fatalf("waited %v for 3 spent tokens, want about %v", waited, want)
	}
}

func TestWaitFull_otherBucketIndependent(t *testing.T) {
	p, c := newTestPacer(t, DefaultBudgets())
	if err := p.WaitFull(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	if err := p.WaitFull(context.Background(), ChallengeBucket("0xAbC")); err != nil {
		t.Fatal(err)
	}
	if len(c.slept) != 0 {
		t.Fatalf("independent buckets waited %v", c.slept)
	}
}

func TestWaitFull_errors(t *testing.T) {
	var nilPacer *Pacer
	if err := nilPacer.WaitFull(context.Background(), BucketCred); err != nil {
		t.Fatalf("nil pacer: %v", err)
	}
	p, _ := newTestPacer(t, DefaultBudgets())
	if err := p.WaitFull(context.Background(), "bogus"); err == nil {
		t.Fatal("unknown bucket accepted")
	}
	if err := p.Wait(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.WaitFull(ctx, BucketCred); err == nil {
		t.Fatal("a cancelled wait succeeded")
	}
}

func TestWaitFull_cancelGivesTheTokensBack(t *testing.T) {
	p, c := newTestPacer(t, DefaultBudgets())
	if err := p.Wait(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.WaitFull(ctx, BucketCred); err == nil {
		t.Fatal("a cancelled wait succeeded")
	}
	slept := len(c.slept)
	if err := p.Wait(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	if len(c.slept) != slept {
		t.Fatalf("the tokens a cancelled WaitFull collected were not given back: slept %v", c.slept[slept:])
	}
}

func TestWaitFull_chargedDebtIsWaitedOut(t *testing.T) {
	p, c := newTestPacer(t, DefaultBudgets())
	for range DefaultCredBurst + 2 {
		if err := p.Charge(BucketCred); err != nil {
			t.Fatal(err)
		}
	}
	start := c.now()
	if err := p.WaitFull(context.Background(), BucketCred); err != nil {
		t.Fatal(err)
	}
	perToken := time.Minute / time.Duration(DefaultCredPerMin)
	if waited := c.now().Sub(start); waited < time.Duration(DefaultCredBurst+2)*perToken-time.Millisecond {
		t.Fatalf("waited %v; a bucket 2 tokens in debt needs %v", waited, time.Duration(DefaultCredBurst+2)*perToken)
	}
}
