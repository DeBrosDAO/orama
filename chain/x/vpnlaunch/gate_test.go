package vpnlaunch

import (
	"errors"
	"strings"
	"testing"
)

func readyDay() Day {
	return Day{
		Dirauths:                   MinDirauths,
		IndependentDirauthMajority: true,
		Relays:                     MinRelays,
		RelayOperators:             MinRelayOperators,
		Exits:                      MinExits,
		ExitCountries:              MinExitCountries,
		TopWeight:                  10,
		TotalWeight:                100,
	}
}

func window(n int, d Day) []Day {
	w := make([]Day, n)
	for i := range w {
		w[i] = d
	}
	return w
}

func TestThresholdsHoldOnlyWhenEveryThresholdHoldsEveryDay(t *testing.T) {
	if !ThresholdsHold(window(ObservationDays, readyDay())) {
		t.Fatal("a window on every threshold must open")
	}
	cases := map[string]func(Day) Day{
		"dirauths":            func(d Day) Day { d.Dirauths = MinDirauths - 1; return d },
		"independent":         func(d Day) Day { d.IndependentDirauthMajority = false; return d },
		"relays":              func(d Day) Day { d.Relays = MinRelays - 1; return d },
		"operators":           func(d Day) Day { d.RelayOperators = MinRelayOperators - 1; return d },
		"exits":               func(d Day) Day { d.Exits = MinExits - 1; return d },
		"countries":           func(d Day) Day { d.ExitCountries = MinExitCountries - 1; return d },
		"heaviest operator":   func(d Day) Day { d.TopWeight = 11; return d },
		"no weight at all":    func(d Day) Day { d.TopWeight, d.TotalWeight = 0, 0; return d },
		"top above the total": func(d Day) Day { d.TopWeight = 200; return d },
	}
	for name, mutate := range cases {
		for _, at := range []int{0, ObservationDays / 2, ObservationDays - 1} {
			w := window(ObservationDays, readyDay())
			w[at] = mutate(w[at])
			if ThresholdsHold(w) {
				t.Errorf("%s failing on day %d still opened: one bad day in the window must close it", name, at)
			}
		}
	}
}

// 10.4% of the weight used to pass because the share was truncated to a whole
// percent before it was compared with 10.
func TestThresholdsHoldShareIsNotRoundedDown(t *testing.T) {
	d := readyDay()
	d.TopWeight, d.TotalWeight = 1040, 10000
	if ThresholdsHold(window(ObservationDays, d)) {
		t.Fatal("an operator holding 10.4% of the weight opened the gate")
	}
	d.TopWeight = 1000
	if !ThresholdsHold(window(ObservationDays, d)) {
		t.Fatal("exactly 10% must pass")
	}
}

func TestThresholdsHoldShareDoesNotOverflow(t *testing.T) {
	d := readyDay()
	d.TopWeight, d.TotalWeight = ^uint64(0)/2, ^uint64(0)
	if ThresholdsHold(window(ObservationDays, d)) {
		t.Fatal("half of a near-maximal total opened the gate")
	}
}

func TestThresholdsHoldNeedsTheWholeWindow(t *testing.T) {
	if ThresholdsHold(window(ObservationDays-1, readyDay())) {
		t.Fatal("29 days opened")
	}
	if ThresholdsHold(nil) {
		t.Fatal("an empty window opened")
	}
	older := append(window(5, Day{}), window(ObservationDays, readyDay())...)
	if !ThresholdsHold(older) {
		t.Fatal("days older than the window must not count against it")
	}
}

func TestFailuresNameWhatIsWrong(t *testing.T) {
	w := window(ObservationDays, readyDay())
	w[3].Exits = 2
	w[3].Dirauths = 3
	fails := Failures(w)
	if len(fails) != 2 {
		t.Fatalf("failures = %v", fails)
	}
	for _, f := range fails {
		if f.Day != 3 || f.Have == "" || f.Want == "" || !strings.Contains(f.String(), "day 3") {
			t.Errorf("failure %+v", f)
		}
	}
	short := Failures(window(4, readyDay()))
	if len(short) != 1 || short[0].Metric != "observed days" {
		t.Errorf("short window failures = %v", short)
	}
}

func TestAuthorize(t *testing.T) {
	err := Authorize(window(ObservationDays, readyDay()))
	if !errors.Is(err, ErrLaunchSwitchedOff) {
		t.Fatalf("a network that measures up is still closed while the switch is off, got %v", err)
	}
	bad := window(ObservationDays, readyDay())
	bad[0].Relays = 1
	err = Authorize(bad)
	if err == nil || errors.Is(err, ErrLaunchSwitchedOff) || !strings.Contains(err.Error(), "relays") {
		t.Fatalf("a network under the thresholds is refused for them, got %v", err)
	}
}

// The public VPN is not launched. Turning the switch on is a release decision
// that updates this test with the measurement it relied on.
func TestLaunchStaysSwitchedOff(t *testing.T) {
	if LaunchEnabled {
		t.Fatal("LaunchEnabled is true: the public VPN launch needs the owner's decision")
	}
}
