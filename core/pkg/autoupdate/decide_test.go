package autoupdate

import (
	"errors"
	"strings"
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

func TestDecide_aValidatorOnAutoSkipsAndIsNotAnError(t *testing.T) {
	settings := DefaultSettings()
	settings.Role = RoleValidator
	settings.Mode = ModeAuto
	health := Health{Voters: 3, HealthyVoters: 3}
	d, err := Decide(settings, health, time.Now(), "1.0.0", Candidate{Version: "1.0.1"}, nil)
	if err != nil || d.Action != ActionSkip || !strings.Contains(d.Reason, "by hand") {
		t.Fatalf("a validator on auto: %+v, %v", d, err)
	}
	// With nothing newer there is nothing to skip.
	d, err = Decide(settings, health, time.Now(), "1.0.1", Candidate{Version: "1.0.1"}, nil)
	if err != nil || d.Action != ActionNone {
		t.Fatalf("a validator on auto with nothing newer: %+v, %v", d, err)
	}
	settings.Mode = ModeNotify
	d, err = Decide(settings, health, time.Now(), "1.0.0", Candidate{Version: "1.0.1"}, nil)
	if err != nil || d.Action != ActionNotify {
		t.Fatalf("a validator on notify: %+v, %v", d, err)
	}
}

// The window is in UTC whatever zone a node's clock is set to: a node on
// Europe/Berlin must not open it two hours early.
func TestDecide_theWindowIsInUTCWhateverTheNodesZone(t *testing.T) {
	berlin := time.FixedZone("CEST", 2*60*60)
	settings := DefaultSettings()
	settings.Mode = ModeAuto
	settings.WindowStart, settings.WindowEnd = 1, 5
	health := Health{Voters: 3, HealthyVoters: 3}
	candidate := Candidate{Version: "1.0.1"}
	// 04:30 UTC is 06:30 in Berlin: inside the window.
	inside, err := Decide(settings, health, time.Date(2026, 10, 1, 6, 30, 0, 0, berlin), "1.0.0", candidate, nil)
	if err != nil || inside.Action != ActionUpgrade {
		t.Fatalf("04:30 UTC (06:30 local): %+v, %v", inside, err)
	}
	// 02:30 local is 00:30 UTC: outside the window.
	outside, err := Decide(settings, health, time.Date(2026, 10, 1, 2, 30, 0, 0, berlin), "1.0.0", candidate, nil)
	if err != nil || outside.Action != ActionNotify {
		t.Fatalf("00:30 UTC (02:30 local): %+v, %v", outside, err)
	}
}
