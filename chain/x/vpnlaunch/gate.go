// Package vpnlaunch decides whether a public VPN beta may open.
// The thresholds are plans/open-network/track-e-tor-network.md E7.
// Opening the beta is a separate act. This package only answers the predicate.
package vpnlaunch

// Thresholds are the E7 minimums, measured over 30 days.
const (
	MinDirauths          = 5
	MinRelays            = 100
	MinRelayOperators    = 40
	MinExits             = 15
	MinExitCountries     = 5
	MaxOperatorWeightPct = 10
	ObservationDays      = 30
)

// Snapshot is one observation of the private network.
type Snapshot struct {
	Dirauths                   int
	IndependentDirauthMajority bool
	Relays                     int
	RelayOperators             int
	Exits                      int
	ExitCountries              int
	MaxOperatorWeightPct       int
	ObservedDays               int
}

// Open is true only when every E7 condition holds.
func Open(s Snapshot) bool {
	return s.ObservedDays >= ObservationDays &&
		s.Dirauths >= MinDirauths &&
		s.IndependentDirauthMajority &&
		s.Relays >= MinRelays &&
		s.RelayOperators >= MinRelayOperators &&
		s.Exits >= MinExits &&
		s.ExitCountries >= MinExitCountries &&
		s.MaxOperatorWeightPct <= MaxOperatorWeightPct
}
