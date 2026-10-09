package reporter

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

var epochStart = time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

func dayWindow() Window { return Window{From: epochStart, To: epochStart.Add(24 * time.Hour)} }

// voteAt parses a vote valid at hour h of the test epoch.
func voteAt(t *testing.T, h int, relays ...relaySpec) Vote {
	t.Helper()
	return mustParse(t, testVote(testAuthorityHex, epochStart.Add(time.Duration(h)*time.Hour), relays...))
}

// hours builds one vote per listed hour carrying the same relays.
func hours(t *testing.T, hs []int, relays ...relaySpec) []Vote {
	t.Helper()
	votes := make([]Vote, len(hs))
	for i, h := range hs {
		votes[i] = voteAt(t, h, relays...)
	}
	return votes
}

func seq(from, to int) []int {
	var s []int
	for i := from; i < to; i++ {
		s = append(s, i)
	}
	return s
}

func byFingerprint(t *testing.T, obs []Observation, n byte) Observation {
	t.Helper()
	for _, o := range obs {
		if o.Fingerprint == fp(n) {
			return o
		}
	}
	t.Fatalf("no observation for relay %d", n)
	return Observation{}
}

func TestObserve_weightIsTheMeasuredMedianNeverTheAdvertisedBandwidth(t *testing.T) {
	votes := []Vote{
		voteAt(t, 0, relaySpec{n: 1, flags: "Running Fast", measured: "Bandwidth=900000 Measured=100"}, relaySpec{n: 2, flags: "Running", measured: "Bandwidth=999999999"}),
		voteAt(t, 1, relaySpec{n: 1, flags: "Running Fast", measured: "Bandwidth=900000 Measured=300"}, relaySpec{n: 2, flags: "Running", measured: "Bandwidth=999999999"}),
		voteAt(t, 2, relaySpec{n: 1, flags: "Running Fast", measured: "Bandwidth=900000 Measured=200"}, relaySpec{n: 2, flags: "Running", measured: "Bandwidth=999999999"}),
	}
	w := Window{From: epochStart, To: epochStart.Add(3 * time.Hour)}
	obs, err := Observe(votes, w, time.Hour)
	require.NoError(t, err)

	require.EqualValues(t, 200, byFingerprint(t, obs, 1).Weight)
	require.Zero(t, byFingerprint(t, obs, 2).Weight, "a relay that is not measured weighs nothing however much it advertises")
	entry := byFingerprint(t, obs, 1).Entry()
	require.Equal(t, math.NewInt(200*NoramaPerWeight), entry.ConsensusWeight)
	require.NoError(t, entry.Validate())
}

func TestObserve_medianOfAnEvenCountIsTheFlooredMean(t *testing.T) {
	require.EqualValues(t, 3, medianUint([]uint64{2, 5}))
	require.EqualValues(t, 5, medianUint([]uint64{5, 5}))
	require.EqualValues(t, 7, medianUint([]uint64{9, 1, 7}))
	require.Zero(t, medianUint(nil))
	big := ^uint64(0)
	require.Equal(t, big, medianUint([]uint64{big, big}), "the mean of two large values must not overflow")
}

func TestObserve_uptimeIsTheShareOfPresentVotesWithGaps(t *testing.T) {
	up := relaySpec{n: 1, flags: "Running", measured: "Measured=10"}
	down := relaySpec{n: 1, flags: "Fast", measured: "Measured=10"}
	// 20 of 24 expected votes are archived; the relay is Running in 10.
	votes := append(hours(t, seq(0, 10), up), hours(t, seq(10, 20), down)...)
	obs, err := Observe(votes, dayWindow(), time.Hour)
	require.NoError(t, err)
	require.Equal(t, "0.500000000000000000", byFingerprint(t, obs, 1).Uptime.String(),
		"4 missing votes are the archive's gap, not the relay's downtime")
}

func TestObserve_refusesAnArchiveThatMissesTooMuch(t *testing.T) {
	up := relaySpec{n: 1, flags: "Running"}
	_, err := Observe(hours(t, seq(0, 19), up), dayWindow(), time.Hour)
	require.ErrorIs(t, err, ErrIncompleteArchive, "19 of 24 is under four fifths")
	_, err = Observe(hours(t, seq(0, 20), up), dayWindow(), time.Hour)
	require.NoError(t, err, "20 of 24 is four fifths")
	_, err = Observe(nil, dayWindow(), time.Hour)
	require.ErrorIs(t, err, ErrIncompleteArchive)
}

func TestObserve_badParameters(t *testing.T) {
	_, err := Observe(nil, dayWindow(), 0)
	require.Error(t, err)
	_, err = Observe(nil, Window{From: epochStart, To: epochStart.Add(time.Minute)}, time.Hour)
	require.Error(t, err)
}

func TestObserve_flags(t *testing.T) {
	exit := relaySpec{n: 1, flags: "Exit Running Guard", measured: "Measured=1"}
	plain := relaySpec{n: 1, flags: "Running", measured: "Measured=1"}
	bad := relaySpec{n: 2, flags: "Exit BadExit Running", measured: "Measured=1"}
	votes := append(hours(t, seq(0, 12), exit), hours(t, seq(12, 24), plain)...)
	for i := range votes {
		votes[i].Routers = append(votes[i].Routers, mustParse(t, testVote(testAuthorityHex, epochStart, bad)).Routers...)
	}
	obs, err := Observe(votes, dayWindow(), time.Hour)
	require.NoError(t, err)
	got := byFingerprint(t, obs, 1)
	require.NotZero(t, got.Flags&FlagExit, "exit in half the votes sets the bit")
	require.NotZero(t, got.Flags&FlagGuard)
	require.Zero(t, byFingerprint(t, obs, 2).Flags&FlagExit, "a BadExit relay is not an exit")
}

func TestObserve_ed25519IsTheLatestVotes(t *testing.T) {
	old := voteAt(t, 0, relaySpec{n: 1, flags: "Running"})
	latest := voteAt(t, 1, relaySpec{n: 1, flags: "Running"})
	latest.Routers[0].Ed25519 = ed(9)
	obs, err := Observe([]Vote{old, latest}, Window{From: epochStart, To: epochStart.Add(2 * time.Hour)}, time.Hour)
	require.NoError(t, err)
	require.Equal(t, ed(9), obs[0].Ed25519)
}

func TestObserve_orderDoesNotChangeTheRoot(t *testing.T) {
	var specs []relaySpec
	for n := byte(1); n <= 30; n++ {
		specs = append(specs, relaySpec{n: n, flags: "Running Fast", measured: fmt.Sprintf("Measured=%d", int(n)*7)})
	}
	votes := hours(t, seq(0, 24), specs...)
	rootOf := func(vs []Vote) []byte {
		obs, err := Observe(vs, dayWindow(), time.Hour)
		require.NoError(t, err)
		var entries []relaytypes.RelayObservation
		for _, o := range obs {
			entries = append(entries, o.Entry())
		}
		root, err := relaytypes.InputsRoot(entries)
		require.NoError(t, err)
		return root
	}
	want := rootOf(votes)
	shuffled := append([]Vote(nil), votes...)
	rand.New(rand.NewSource(1)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	// Select puts the votes in time order, which is what a loader hands Observe.
	sel, err := Select(shuffled, testAuthority(), dayWindow())
	require.NoError(t, err)
	require.Equal(t, want, rootOf(sel))
}

func TestSelect(t *testing.T) {
	r := relaySpec{n: 1, flags: "Running"}
	other := mustParse(t, testVote("cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd", epochStart, r))
	before := voteAt(t, -1, r)
	atEnd := voteAt(t, 24, r)
	inside := voteAt(t, 5, r)
	got, err := Select([]Vote{other, before, atEnd, inside, inside}, testAuthority(), dayWindow())
	require.NoError(t, err)
	require.Len(t, got, 1, "another authority, outside the window, and a copy of the same vote do not count")
	require.Equal(t, inside.ValidAfter, got[0].ValidAfter)

	conflicting := voteAt(t, 5, r, relaySpec{n: 2, flags: "Running"})
	_, err = Select([]Vote{inside, conflicting}, testAuthority(), dayWindow())
	require.Error(t, err, "two different votes for one valid-after")
}
