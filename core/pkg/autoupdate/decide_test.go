package autoupdate

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

func healthy() Health { return Health{Voters: 3, HealthyVoters: 2} }

func TestDecide_notifyDoesNotUpgrade(t *testing.T) {
	d, err := Decide(DefaultSettings(), healthy(), time.Now(), "1.2.0", Candidate{Version: "1.3.0", Channel: "stable"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionNotify {
		t.Fatalf("action %s, want notify: %s", d.Action, d.Reason)
	}
}

func TestDecide_autoUpgradesInsideTheWindow(t *testing.T) {
	s := DefaultSettings()
	s.Mode = ModeAuto
	s.WindowStart = 1
	s.WindowEnd = 5
	now := time.Date(2026, 9, 28, 2, 0, 0, 0, time.UTC)
	d, err := Decide(s, healthy(), now, "1.2.0", Candidate{Version: "1.3.0", Channel: "stable"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionUpgrade {
		t.Fatalf("action %s, want upgrade: %s", d.Action, d.Reason)
	}
}

func TestDecide_outsideTheWindowNotifiesInstead(t *testing.T) {
	s := DefaultSettings()
	s.Mode = ModeAuto
	s.WindowStart = 1
	s.WindowEnd = 5
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	d, err := Decide(s, healthy(), now, "1.2.0", Candidate{Version: "1.3.0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionNotify {
		t.Fatalf("action %s, want notify: %s", d.Action, d.Reason)
	}
}

func TestDecide_refusesADowngradeAndABadRelease(t *testing.T) {
	d, err := Decide(DefaultSettings(), healthy(), time.Now(), "1.3.0", Candidate{Version: "1.2.0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionRefuse {
		t.Fatalf("downgrade action %s, want refuse", d.Action)
	}

	d, err = Decide(DefaultSettings(), healthy(), time.Now(), "1.2.0", Candidate{Version: "1.3.0", Bad: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionRefuse {
		t.Fatalf("bad release action %s, want refuse", d.Action)
	}
}

func TestDecide_refusesWhenTheClusterIsNotHealthy(t *testing.T) {
	for _, health := range []Health{
		{Degraded: true, Voters: 3, HealthyVoters: 3},
		{Voters: 3, HealthyVoters: 1},
		{Voters: 2, HealthyVoters: 1},
	} {
		d, err := Decide(DefaultSettings(), health, time.Now(), "1.0.0", Candidate{Version: "1.1.0"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if d.Action != ActionRefuse {
			t.Fatalf("health %+v action %s, want refuse", health, d.Action)
		}
	}
}

func TestDecide_refusesUnverifiedMetadata(t *testing.T) {
	cases := []error{
		releaseverify.ErrRollback,
		releaseverify.ErrFreeze,
		releaseverify.ErrThreshold,
		releaseverify.ErrTargetHash,
		errors.New("root: no signatures"),
	}
	for _, verifyErr := range cases {
		d, err := Decide(DefaultSettings(), healthy(), time.Now(), "1.0.0", Candidate{Version: "1.1.0"}, verifyErr)
		if err != nil {
			t.Fatal(err)
		}
		if d.Action != ActionRefuse {
			t.Fatalf("%v action %s, want refuse", verifyErr, d.Action)
		}
	}
}

func TestDecide_offDoesNothing(t *testing.T) {
	s := DefaultSettings()
	s.Mode = ModeOff
	d, err := Decide(s, healthy(), time.Now(), "1.0.0", Candidate{Version: "9.0.0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionNone {
		t.Fatalf("action %s, want none", d.Action)
	}
}

func TestDecide_rejectsParallelismAboveOne(t *testing.T) {
	s := DefaultSettings()
	s.MaxParallel = 2
	if _, err := Decide(s, healthy(), time.Now(), "1.0.0", Candidate{Version: "1.1.0"}, nil); err == nil {
		t.Fatal("max_parallel 2 was accepted")
	}
}

func TestCompare_ordersNumericSegments(t *testing.T) {
	newer, err := Compare("1.10.0", "1.9.0")
	if err != nil || newer <= 0 {
		t.Fatalf("1.10.0 vs 1.9.0 = %d, %v", newer, err)
	}
	same, err := Compare("v1.2.0", "1.2")
	if err != nil || same != 0 {
		t.Fatalf("v1.2.0 vs 1.2 = %d, %v", same, err)
	}
	if _, err := Compare("1.2.beta", "1.2.0"); err == nil {
		t.Fatal("a non-numeric version was ordered")
	}
}

func TestLock_oneWinnerAndACrashFreesIt(t *testing.T) {
	lock := &Lock{}
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	var wins int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if lock.Try(now, fmt.Sprintf("node-%d", id), time.Minute) {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d nodes took the lock, want 1", wins)
	}
	if lock.Holder(now.Add(time.Minute)) != "" {
		t.Fatal("an expired lease was still held")
	}
	if !lock.Try(now.Add(time.Minute), "node-other", time.Minute) {
		t.Fatal("a crashed holder's lease was not free")
	}
}

func TestApply_aFailedGateRollsBack(t *testing.T) {
	var log []string
	step := func(name string) Step {
		return Step{
			Name: name,
			Do:   func() error { log = append(log, "do "+name); return nil },
			Undo: func() error { log = append(log, "undo "+name); return nil },
		}
	}
	rolled, err := Apply([]Step{step("stop"), step("swap"), step("start")}, func() error {
		return errors.New("rqlite has no leader")
	})
	if err == nil || !rolled {
		t.Fatalf("rolled=%v err=%v, want a rollback", rolled, err)
	}
	want := []string{"do stop", "do swap", "do start", "undo start", "undo swap", "undo stop"}
	if len(log) != len(want) {
		t.Fatalf("log %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("log %v, want %v", log, want)
		}
	}
}

func TestApply_aFailedStepDoesNotUndoWhatDidNotRun(t *testing.T) {
	var undone []string
	bad, err := Apply([]Step{
		{Name: "stop", Do: func() error { return nil }, Undo: func() error { undone = append(undone, "stop"); return nil }},
		{Name: "swap", Do: func() error { return errors.New("bad archive") }, Undo: func() error { undone = append(undone, "swap"); return nil }},
		{Name: "start", Do: func() error { return nil }, Undo: func() error { undone = append(undone, "start"); return nil }, Blames: true},
	}, func() error { return nil })
	if err == nil {
		t.Fatal("a failed swap was success")
	}
	if bad {
		t.Fatal("a failed swap blamed the release")
	}
	if len(undone) != 1 || undone[0] != "stop" {
		t.Fatalf("undone %v, want only stop", undone)
	}
}
