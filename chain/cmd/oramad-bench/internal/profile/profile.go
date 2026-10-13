// Package profile defines what a run does: which transaction types, and for each a series of steps,
// every step either an open-loop rate or a closed-loop window.
package profile

import (
	"fmt"
	"math"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/report"
)

const (
	// MaxClosedTxs caps the transactions signed for one closed-loop step.
	MaxClosedTxs = 50_000
)

// Step is one load level.
type Step struct {
	// Mode is report.ModeOpen or report.ModeClosed.
	Mode string
	// Rate is the offered transactions per second of an open-loop step.
	Rate float64
	// Window is how many transactions a closed-loop step keeps in flight.
	Window   int
	Duration time.Duration
}

// Txs is how many transactions must be signed for the step: what an open-loop step offers, and the
// most a closed-loop window can send in its time (a window of w completes at most w transactions a
// second when a block takes a second).
func (s Step) Txs() int {
	if s.Mode == report.ModeOpen {
		return int(math.Ceil(s.Rate * s.Duration.Seconds()))
	}
	return min(s.Window*int(math.Ceil(s.Duration.Seconds())), MaxClosedTxs)
}

// Validate checks the step.
func (s Step) Validate() error {
	if s.Duration <= 0 {
		return fmt.Errorf("a step needs a positive duration")
	}
	switch s.Mode {
	case report.ModeOpen:
		if s.Rate <= 0 || math.IsNaN(s.Rate) || math.IsInf(s.Rate, 0) {
			return fmt.Errorf("open-loop rate %v must be positive", s.Rate)
		}
	case report.ModeClosed:
		if s.Window < 1 {
			return fmt.Errorf("closed-loop window %d must be at least 1", s.Window)
		}
	default:
		return fmt.Errorf("unknown step mode %q", s.Mode)
	}
	return nil
}

// TypePlan is the steps one transaction type runs.
type TypePlan struct {
	Type  string
	Steps []Step
}

// TotalTxs is how many transactions the type signs over all its steps.
func (t TypePlan) TotalTxs() int {
	n := 0
	for _, s := range t.Steps {
		n += s.Txs()
	}
	return n
}

// Profile is a named run.
type Profile struct {
	Name string
	// Accounts is how many bench accounts the run uses.
	Accounts int
	Types    []TypePlan
}

// Validate checks every step of the profile.
func (p Profile) Validate() error {
	if p.Accounts < 2 {
		return fmt.Errorf("profile %s: needs at least two accounts", p.Name)
	}
	if len(p.Types) == 0 {
		return fmt.Errorf("profile %s: no transaction types", p.Name)
	}
	for _, t := range p.Types {
		if len(t.Steps) == 0 {
			return fmt.Errorf("profile %s: type %s has no steps", p.Name, t.Type)
		}
		for i, s := range t.Steps {
			if err := s.Validate(); err != nil {
				return fmt.Errorf("profile %s: type %s step %d: %w", p.Name, t.Type, i, err)
			}
		}
	}
	return nil
}

// Only returns the profile restricted to the named types, in the profile's order. It fails on a name
// the profile does not run.
func (p Profile) Only(names []string) (Profile, error) {
	if len(names) == 0 {
		return p, nil
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := p
	out.Types = nil
	for _, t := range p.Types {
		if want[t.Type] {
			out.Types = append(out.Types, t)
			delete(want, t.Type)
		}
	}
	for n := range want {
		return Profile{}, fmt.Errorf("profile %s does not run type %q", p.Name, n)
	}
	return out, nil
}

// ByName returns a built-in profile.
func ByName(name string) (Profile, error) {
	switch name {
	case "smoke":
		return Smoke(), nil
	case "full":
		return Full(), nil
	}
	return Profile{}, fmt.Errorf("unknown profile %q; the profiles are smoke and full", name)
}

func open(rate float64, d time.Duration) Step {
	return Step{Mode: report.ModeOpen, Rate: rate, Duration: d}
}

func closed(window int, d time.Duration) Step {
	return Step{Mode: report.ModeClosed, Window: window, Duration: d}
}
