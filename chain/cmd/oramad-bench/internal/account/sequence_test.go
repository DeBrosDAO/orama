package account

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestSequencer_takesConsecutiveSequences(t *testing.T) {
	s := NewSequencer()
	s.Set("a", 7)
	s.Set("b", 0)
	for want := uint64(7); want < 10; want++ {
		got, err := s.Take("a")
		if err != nil || got != want {
			t.Fatalf("a: got %d, %v; want %d", got, err, want)
		}
	}
	if got, _ := s.Take("b"); got != 0 {
		t.Fatalf("b starts at %d, accounts must not share a counter", got)
	}
	if next, ok := s.Peek("a"); !ok || next != 10 {
		t.Fatalf("peek a = %d, %v", next, ok)
	}
}

func TestSequencer_unknownAccountIsAnError(t *testing.T) {
	if _, err := NewSequencer().Take("ghost"); !errors.Is(err, ErrUnknownAccount) {
		t.Fatalf("got %v", err)
	}
	if _, ok := NewSequencer().Peek("ghost"); ok {
		t.Fatal("peek of an unknown account reported a sequence")
	}
}

func TestSequencer_concurrentTakesAreUniqueAndContiguous(t *testing.T) {
	s := NewSequencer()
	s.Set("a", 100)
	const n = 500
	var wg sync.WaitGroup
	got := make([]uint64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			seq, err := s.Take("a")
			if err != nil {
				t.Error(err)
			}
			got[i] = seq
		}(i)
	}
	wg.Wait()
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	for i, seq := range got {
		if seq != uint64(100+i) {
			t.Fatalf("sequence %d at rank %d: not contiguous", seq, i)
		}
	}
}

func TestSequencer_reloadReplacesWhatWasTaken(t *testing.T) {
	s := NewSequencer()
	s.Set("a", 5)
	for i := 0; i < 3; i++ {
		if _, err := s.Take("a"); err != nil {
			t.Fatal(err)
		}
	}
	// Only one of the three transactions was included: the chain says 6.
	lookup := func(_ context.Context, addr string) (uint64, error) { return 6, nil }
	if err := s.Reload(context.Background(), lookup, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Take("a"); got != 6 {
		t.Fatalf("after reload a = %d, want the chain's 6", got)
	}
	if got, _ := s.Take("b"); got != 6 {
		t.Fatalf("after reload b = %d", got)
	}
}

func TestSequencer_reloadNamesTheAccountThatFailed(t *testing.T) {
	lookup := func(_ context.Context, addr string) (uint64, error) {
		if addr == "bad" {
			return 0, errors.New("no such account")
		}
		return 1, nil
	}
	err := NewSequencer().Reload(context.Background(), lookup, []string{"ok", "bad"})
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("got %v", err)
	}
}

func TestGate_blocksOnlyLaterSequencesOfTheFailedAccount(t *testing.T) {
	g := NewGate()
	if g.Blocked("a", 0) {
		t.Fatal("a fresh gate blocked something")
	}
	g.Fail("a", 5)
	g.Fail("a", 9) // a later failure does not move the first one
	cases := []struct {
		addr string
		seq  uint64
		want bool
	}{
		{"a", 4, false}, {"a", 5, false}, {"a", 6, true}, {"a", 100, true}, {"b", 6, false},
	}
	for _, c := range cases {
		if got := g.Blocked(c.addr, c.seq); got != c.want {
			t.Errorf("Blocked(%s, %d) = %v, want %v", c.addr, c.seq, got, c.want)
		}
	}
	g.Fail("a", 2)
	if !g.Blocked("a", 3) || g.Failures() != 1 {
		t.Fatalf("an earlier failure must replace a later one; failures %d", g.Failures())
	}
}
