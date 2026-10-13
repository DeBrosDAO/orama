package reporter

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"time"

	"cosmossdk.io/math"

	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

const (
	// NoramaPerWeight converts one unit of measured bandwidth (the Measured=
	// value, kilobytes per second in a bandwidth file) into the norama
	// x/relay's consensus_weight is counted in. x/relay does not rescale; the
	// per-relay cap (100 ORAMA) is reached at a measured 100000.
	NoramaPerWeight int64 = 1_000_000

	// MinCoverageNumerator and MinCoverageDenominator are the share of an
	// epoch's expected votes the archive must hold before the reporter will
	// report it: 4/5.
	MinCoverageNumerator   = 4
	MinCoverageDenominator = 5

	// Flag bits of RelayObservation.Flags. Only FlagExit changes pay; the
	// others are covered by inputs_root and let a reader see what the
	// authority said.
	FlagExit   = relaytypes.FlagExit
	FlagGuard  = 1 << 1
	FlagStable = 1 << 2
	FlagFast   = 1 << 3

	flagRunning = "Running"
	flagExit    = "Exit"
	flagBadExit = "BadExit"
)

// ErrIncompleteArchive means the archive holds too few of an epoch's votes to
// judge uptime from. The reporter reports nothing rather than a guess.
var ErrIncompleteArchive = errors.New("the vote archive does not cover the epoch")

// ErrEpochTooShort means the epoch lasted less than one voting interval of the
// network, so its window can hold no vote to judge uptime from. Nothing later
// changes that, so the epoch is dropped, not retried. It is a mismatch of the
// chain's epoch duration with the Tor network's voting interval, not a fault of
// the reporter.
var ErrEpochTooShort = errors.New("the epoch is shorter than one voting interval of the Tor network")

// Window is the half-open span [From, To) an epoch lasted.
type Window struct {
	From, To time.Time
}

// Observation is what the votes say of one relay over a window.
type Observation struct {
	Fingerprint [fingerprintLen]byte
	// Ed25519 is the identity in the latest vote that lists the relay; empty
	// when no vote carries one, and then the relay cannot be reported.
	Ed25519 []byte
	// Weight is the median Measured over the votes that list the relay as
	// Running and measured it; zero when none did.
	Weight uint64
	Flags  uint32
	// Uptime is the share of the window's votes that list the relay Running.
	Uptime math.LegacyDec
}

// Select keeps the votes of one authority whose valid-after is inside the
// window. A second, different vote for one valid-after is an error: which one
// the authority meant is not for the reporter to guess. A byte-identical copy
// is the same vote and counts once.
func Select(votes []Vote, authority [fingerprintLen]byte, w Window) ([]Vote, error) {
	byTime := map[time.Time]Vote{}
	for _, v := range votes {
		if v.Authority != authority || v.ValidAfter.Before(w.From) || !v.ValidAfter.Before(w.To) {
			continue
		}
		if have, ok := byTime[v.ValidAfter]; ok {
			if have.Digest != v.Digest {
				return nil, fmt.Errorf("two different votes for valid-after %s", v.ValidAfter.Format(voteTimeLayout))
			}
			continue
		}
		byTime[v.ValidAfter] = v
	}
	out := make([]Vote, 0, len(byTime))
	for _, v := range byTime {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ValidAfter.Before(out[j].ValidAfter) })
	return out, nil
}

// Observe computes one observation per relay listed in votes (already
// selected), sorted by fingerprint. interval is the authority's voting
// interval. It returns ErrIncompleteArchive when votes cover less than
// MinCoverage of the window.
func Observe(votes []Vote, w Window, interval time.Duration) ([]Observation, error) {
	expected, err := expectedVotes(w, interval)
	if err != nil {
		return nil, err
	}
	if len(votes)*MinCoverageDenominator < expected*MinCoverageNumerator {
		return nil, fmt.Errorf("%w: %d of %d expected votes", ErrIncompleteArchive, len(votes), expected)
	}
	acc := map[[fingerprintLen]byte]*accum{}
	for _, v := range votes {
		for _, rt := range v.Routers {
			a := acc[rt.Fingerprint]
			if a == nil {
				a = &accum{}
				acc[rt.Fingerprint] = a
			}
			a.add(rt)
		}
	}
	out := make([]Observation, 0, len(acc))
	for fp, a := range acc {
		out = append(out, a.observation(fp, len(votes)))
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Fingerprint[:], out[j].Fingerprint[:]) < 0 })
	return out, nil
}

// expectedVotes is the number of votes an authority casts in the window, or
// ErrEpochTooShort when the window is shorter than one voting interval.
func expectedVotes(w Window, interval time.Duration) (int, error) {
	if interval <= 0 {
		return 0, errors.New("vote interval must be positive")
	}
	expected := int(w.To.Sub(w.From) / interval)
	if expected < 1 {
		return 0, fmt.Errorf("%w: it lasted %s (%s to %s) and the network votes every %s", ErrEpochTooShort, w.To.Sub(w.From), w.From.Format(time.RFC3339), w.To.Format(time.RFC3339), interval)
	}
	return expected, nil
}

type accum struct {
	running  int
	measured []uint64
	exit     int
	guard    int
	stable   int
	fast     int
	ed25519  []byte
}

// add records one vote's entry. Votes arrive oldest first, so the last
// ed25519 identity seen is the latest.
func (a *accum) add(rt Router) {
	if len(rt.Ed25519) != 0 {
		a.ed25519 = rt.Ed25519
	}
	if !rt.Flags[flagRunning] {
		return
	}
	a.running++
	if rt.HasMeasured {
		a.measured = append(a.measured, rt.Measured)
	}
	if rt.Flags[flagExit] && !rt.Flags[flagBadExit] {
		a.exit++
	}
	for flag, n := range map[string]*int{"Guard": &a.guard, "Stable": &a.stable, "Fast": &a.fast} {
		if rt.Flags[flag] {
			*n++
		}
	}
}

// observation reduces the accumulated votes. A flag is set when at least half
// of the votes that listed the relay Running carry it.
func (a *accum) observation(fp [fingerprintLen]byte, votes int) Observation {
	o := Observation{
		Fingerprint: fp,
		Ed25519:     a.ed25519,
		Weight:      medianUint(a.measured),
		Uptime:      math.LegacyNewDec(int64(a.running)).QuoTruncate(math.LegacyNewDec(int64(votes))),
	}
	half := func(n int) bool { return a.running > 0 && n*2 >= a.running }
	if half(a.exit) {
		o.Flags |= FlagExit
	}
	if half(a.guard) {
		o.Flags |= FlagGuard
	}
	if half(a.stable) {
		o.Flags |= FlagStable
	}
	if half(a.fast) {
		o.Flags |= FlagFast
	}
	return o
}

// medianUint is the middle value, or the floored mean of the two middle
// values; zero for none. It is the rule x/relay applies across reporters.
func medianUint(vals []uint64) uint64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]uint64(nil), vals...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return s[n/2-1]/2 + s[n/2]/2 + (s[n/2-1]%2+s[n/2]%2)/2
}

// Entry converts an observation to the chain's form.
func (o Observation) Entry() relaytypes.RelayObservation {
	return relaytypes.RelayObservation{
		RsaFingerprint:  append([]byte(nil), o.Fingerprint[:]...),
		Ed25519Id:       append([]byte(nil), o.Ed25519...),
		ConsensusWeight: math.NewIntFromUint64(o.Weight).MulRaw(NoramaPerWeight),
		Flags:           o.Flags,
		UptimeFraction:  o.Uptime,
	}
}
