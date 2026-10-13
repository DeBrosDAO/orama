// Package vpnlaunch decides whether a public VPN beta may open.
// The thresholds are plans/open-network/track-e-tor-network.md E7.
//
// Two things must hold before anything public opens: every threshold, on each
// of the last ObservationDays days, and the launch switch (LaunchEnabled),
// which a release turns on after the owner reads the status page. Opening the
// beta is a separate act; this package only answers whether it may. Nothing
// links it into oramad or the node: it is a pure gate.
package vpnlaunch

import (
	"errors"
	"fmt"
	"math/bits"
	"strings"
)

// Thresholds are the E7 minimums. Each must hold on every day of the
// ObservationDays window, not on average.
const (
	MinDirauths       = 5
	MinRelays         = 100
	MinRelayOperators = 40
	MinExits          = 15
	MinExitCountries  = 5
	// MaxWeightPercent is the most consensus weight any one operator or
	// family may hold.
	MaxWeightPercent = 10
	ObservationDays  = 30
)

// LaunchEnabled is the launch switch. It is false in this build, and the
// public VPN stays closed whatever the network looks like. A release that
// opens the beta changes this constant in the same commit that records the
// 30-day measurement it relied on; TestLaunchStaysSwitchedOff pins it so that
// no other change can flip it unnoticed.
const LaunchEnabled = false

// Day is one day's measurement of the private network.
type Day struct {
	Dirauths                   int
	IndependentDirauthMajority bool
	Relays                     int
	RelayOperators             int
	Exits                      int
	ExitCountries              int
	// TopWeight is the consensus weight of the heaviest operator or family,
	// and TotalWeight the whole network's. The share is compared exactly, in
	// integers, so 10.4% is not rounded down to 10%.
	TopWeight   uint64
	TotalWeight uint64
}

// ErrLaunchSwitchedOff is the answer while LaunchEnabled is false.
var ErrLaunchSwitchedOff = errors.New("the public VPN launch switch is off")

// Failure is one threshold that did not hold on one day.
type Failure struct {
	// Day is the index into the judged window, 0 the oldest; WholeWindow when
	// the failure is about the window and not one day.
	Day    int
	Metric string
	Have   string
	Want   string
}

// WholeWindow is the Day of a failure that concerns the window, not one day.
const WholeWindow = -1

func (f Failure) String() string {
	if f.Day == WholeWindow {
		return fmt.Sprintf("%s is %s, need %s", f.Metric, f.Have, f.Want)
	}
	return fmt.Sprintf("day %d: %s is %s, need %s", f.Day, f.Metric, f.Have, f.Want)
}

// Failures lists what is wrong with window, the most recent days last. A
// window shorter than ObservationDays is one failure on its own; a longer one
// is judged on its last ObservationDays days.
func Failures(window []Day) []Failure {
	if len(window) < ObservationDays {
		return []Failure{{Day: WholeWindow, Metric: "observed days", Have: fmt.Sprint(len(window)), Want: fmt.Sprintf("%d", ObservationDays)}}
	}
	var out []Failure
	for i, d := range window[len(window)-ObservationDays:] {
		out = append(out, d.failures(i)...)
	}
	return out
}

func (d Day) failures(i int) []Failure {
	var out []Failure
	check := func(ok bool, metric, have, want string) {
		if !ok {
			out = append(out, Failure{Day: i, Metric: metric, Have: have, Want: want})
		}
	}
	atLeast := func(metric string, have, want int) {
		check(have >= want, metric, fmt.Sprint(have), fmt.Sprintf("at least %d", want))
	}
	atLeast("directory authorities", d.Dirauths, MinDirauths)
	check(d.IndependentDirauthMajority, "independent directory-authority majority", "no", "yes")
	atLeast("relays", d.Relays, MinRelays)
	atLeast("relay operators", d.RelayOperators, MinRelayOperators)
	atLeast("exits", d.Exits, MinExits)
	atLeast("exit countries", d.ExitCountries, MinExitCountries)
	check(d.TotalWeight > 0, "total consensus weight", "0", "more than 0")
	check(d.TotalWeight > 0 && d.TopWeight <= d.TotalWeight && shareAtMost(d.TopWeight, d.TotalWeight),
		"heaviest operator's share of consensus weight",
		fmt.Sprintf("%d of %d", d.TopWeight, d.TotalWeight), fmt.Sprintf("at most %d%%", MaxWeightPercent))
	return out
}

// ThresholdsHold is true only when every E7 threshold holds on each of the last
// ObservationDays days. It is the figure for the status page and says nothing
// about whether the beta may open: that is Authorize, which also needs the
// launch switch.
func ThresholdsHold(window []Day) bool {
	return len(Failures(window)) == 0
}

// Authorize is nil only when the network measures up AND the launch switch is
// on. The error says which: the switch, or the list of thresholds.
func Authorize(window []Day) error {
	if fails := Failures(window); len(fails) > 0 {
		parts := make([]string, len(fails))
		for i, f := range fails {
			parts[i] = f.String()
		}
		return fmt.Errorf("the network does not meet the launch thresholds: %s", strings.Join(parts, "; "))
	}
	if !LaunchEnabled {
		return ErrLaunchSwitchedOff
	}
	return nil
}

// shareAtMost is top/total <= MaxWeightPercent/100 without rounding or
// overflow: top*100 <= total*MaxWeightPercent in 128 bits.
func shareAtMost(top, total uint64) bool {
	lhsHi, lhsLo := bits.Mul64(top, 100)
	rhsHi, rhsLo := bits.Mul64(total, MaxWeightPercent)
	return lhsHi < rhsHi || (lhsHi == rhsHi && lhsLo <= rhsLo)
}
