package vpnlaunch

import "testing"

func ready() Snapshot {
	return Snapshot{
		Dirauths:                   MinDirauths,
		IndependentDirauthMajority: true,
		Relays:                     MinRelays,
		RelayOperators:             MinRelayOperators,
		Exits:                      MinExits,
		ExitCountries:              MinExitCountries,
		MaxOperatorWeightPct:       MaxOperatorWeightPct,
		ObservedDays:               ObservationDays,
	}
}

func TestOpenOnlyWhenEveryThresholdHolds(t *testing.T) {
	if !Open(ready()) {
		t.Fatal("a snapshot on every threshold must open")
	}
	cases := []func(Snapshot) Snapshot{
		func(s Snapshot) Snapshot { s.ObservedDays = ObservationDays - 1; return s },
		func(s Snapshot) Snapshot { s.Dirauths = MinDirauths - 1; return s },
		func(s Snapshot) Snapshot { s.IndependentDirauthMajority = false; return s },
		func(s Snapshot) Snapshot { s.Relays = MinRelays - 1; return s },
		func(s Snapshot) Snapshot { s.RelayOperators = MinRelayOperators - 1; return s },
		func(s Snapshot) Snapshot { s.Exits = MinExits - 1; return s },
		func(s Snapshot) Snapshot { s.ExitCountries = MinExitCountries - 1; return s },
		func(s Snapshot) Snapshot { s.MaxOperatorWeightPct = MaxOperatorWeightPct + 1; return s },
	}
	for i, mutate := range cases {
		if Open(mutate(ready())) {
			t.Fatalf("case %d opened", i)
		}
	}
}
