package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/client/tx"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

type fakeChain struct {
	epoch     Epoch
	relays    map[string]relaytypes.Relay
	settled   map[uint64]bool
	submitted []*relaytypes.MsgReportEpoch
	// failOn is the 0-based submit call that fails once.
	failOn   int
	failed   bool
	lookups  int
	querying error
}

func (c *fakeChain) CurrentEpoch(context.Context) (Epoch, error) { return c.epoch, c.querying }

func (c *fakeChain) Relay(_ context.Context, f []byte) (relaytypes.Relay, bool, error) {
	c.lookups++
	r, ok := c.relays[string(f)]
	return r, ok, nil
}

func (c *fakeChain) EpochSettled(_ context.Context, e uint64) (bool, error) { return c.settled[e], nil }

func (c *fakeChain) Submit(_ context.Context, m *relaytypes.MsgReportEpoch) error {
	if !c.failed && len(c.submitted) == c.failOn && c.failOn >= 0 {
		c.failed = true
		return errors.New("broadcast refused")
	}
	c.submitted = append(c.submitted, m)
	return nil
}

func account(t *testing.T, seed byte) string {
	t.Helper()
	a, err := tx.DeriveAccount(bytes.Repeat([]byte{seed}, 32))
	require.NoError(t, err)
	return a.Address
}

type rig struct {
	t      *testing.T
	home   string
	votes  string
	chain  *fakeChain
	runner *Runner
	other  string
	own    string
}

// newRig starts a chain at epoch 5, which began at epochStart, with relays 1
// to 3 registered to another operator.
func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	r := &rig{t: t, home: filepath.Join(dir, "home"), votes: filepath.Join(dir, "votes"), other: account(t, 1), own: account(t, 2)}
	require.NoError(t, os.MkdirAll(r.home, 0o750))
	require.NoError(t, os.MkdirAll(r.votes, 0o750))
	r.chain = &fakeChain{epoch: Epoch{Number: 5, Start: epochStart}, relays: map[string]relaytypes.Relay{}, settled: map[uint64]bool{}, failOn: -1}
	for n := byte(1); n <= 3; n++ {
		r.register(n, r.other, ed(n))
	}
	var err error
	r.runner, err = NewRunner(Config{
		Home: r.home, VotesDir: r.votes, Reporter: account(t, 3), Operator: r.own,
		Authority: testAuthority(), VoteInterval: time.Hour, ChunkEntries: 2,
	}, r.chain)
	require.NoError(t, err)
	return r
}

func (r *rig) register(n byte, operator string, edID []byte) {
	f := fp(n)
	r.chain.relays[string(f[:])] = relaytypes.Relay{NodeId: fmt.Sprint("node-", n), RsaFingerprint: f[:], Ed25519Id: edID, Operator: operator}
}

// archive writes one vote per hour of the closed epoch.
func (r *rig) archive(hs []int, relays ...relaySpec) {
	for _, h := range hs {
		doc := testVote(testAuthorityHex, epochStart.Add(time.Duration(h)*time.Hour), relays...)
		require.NoError(r.t, os.WriteFile(filepath.Join(r.votes, fmt.Sprintf("h%02d%s", h, VoteSuffix)), []byte(doc), 0o640))
	}
}

// closeEpoch advances the chain one epoch, 24 hours after the last started.
func (r *rig) closeEpoch() {
	r.chain.epoch = Epoch{Number: r.chain.epoch.Number + 1, Start: r.chain.epoch.Start.Add(24 * time.Hour)}
}

func (r *rig) step() ([]uint64, error) { return r.runner.Step(context.Background()) }

func three() []relaySpec {
	return []relaySpec{
		{n: 1, flags: "Running Fast", measured: "Measured=100"},
		{n: 2, flags: "Running Exit", measured: "Measured=200"},
		{n: 3, flags: "Running", measured: "Measured=300"},
		{n: 4, flags: "Running", measured: "Measured=400"}, // not registered
	}
}

func TestStep_firstPassOnlyRecordsTheEpoch(t *testing.T) {
	r := newRig(t)
	e, err := r.step()
	require.NoError(t, err)
	require.Empty(t, e)
	require.Empty(t, r.chain.submitted)
	st, err := loadState(r.home)
	require.NoError(t, err)
	require.EqualValues(t, 5, st.Seen)

	e, err = r.step()
	require.NoError(t, err)
	require.Zero(t, e, "the epoch has not closed")
}

func TestStep_reportsTheClosedEpochOnce(t *testing.T) {
	r := newRig(t)
	r.archive(seq(0, 24), three()...)
	_, err := r.step()
	require.NoError(t, err)
	r.closeEpoch()

	e, err := r.step()
	require.NoError(t, err)
	require.Equal(t, []uint64{5}, e)
	require.Len(t, r.chain.submitted, 2, "three relays in chunks of two")
	var got []relaytypes.RelayObservation
	for _, m := range r.chain.submitted {
		require.EqualValues(t, 5, m.Epoch)
		got = append(got, m.Entries...)
	}
	require.Len(t, got, 3, "the unregistered relay is left out, since x/relay refuses a chunk that holds one")
	root, err := relaytypes.InputsRoot(got)
	require.NoError(t, err)
	require.Equal(t, root, r.chain.submitted[0].InputsRoot)
	for _, g := range got {
		require.NoError(t, g.Validate())
	}

	e, err = r.step()
	require.NoError(t, err)
	require.Empty(t, e)
	require.Len(t, r.chain.submitted, 2, "an epoch is reported once")

	var mon Monitor
	readJSON(t, filepath.Join(r.home, monitorFile), &mon)
	require.Equal(t, Monitor{Epoch: 5, Chunks: 2, Relays: 3, Unregistered: 1}, mon)
}

func TestStep_neverReportsItsOwnOperatorsRelays(t *testing.T) {
	r := newRig(t)
	r.register(2, r.own, ed(2))
	r.archive(seq(0, 24), three()...)
	_, _ = r.step()
	r.closeEpoch()
	_, err := r.step()
	require.NoError(t, err)
	for _, m := range r.chain.submitted {
		for _, e := range m.Entries {
			two := fp(2)
			require.NotEqual(t, two[:], e.RsaFingerprint, "a dirauth operator cannot vouch for its own relay")
		}
	}
	var mon Monitor
	readJSON(t, filepath.Join(r.home, monitorFile), &mon)
	require.Equal(t, 1, mon.OwnOperator)
}

func TestStep_leavesOutARelayWhoseKeyDisagreesWithTheRegistry(t *testing.T) {
	r := newRig(t)
	r.register(3, r.other, ed(99))
	r.archive(seq(0, 24), three()...)
	_, _ = r.step()
	r.closeEpoch()
	_, err := r.step()
	require.NoError(t, err, "one wrong key must not stop every other relay being paid")
	var mon Monitor
	readJSON(t, filepath.Join(r.home, monitorFile), &mon)
	require.Equal(t, 1, mon.KeyMismatch)
	require.Equal(t, 2, mon.Relays)
}

func TestStep_aFailedSubmitRetriesTheWholeReportIdentically(t *testing.T) {
	r := newRig(t)
	r.archive(seq(0, 24), three()...)
	_, _ = r.step()
	r.closeEpoch()
	r.chain.failOn = 1

	_, err := r.step()
	require.ErrorContains(t, err, "chunk 2 of 2")
	require.Len(t, r.chain.submitted, 1)
	st, err := loadState(r.home)
	require.NoError(t, err)
	require.EqualValues(t, 6, st.Seen)
	require.Equal(t, []Due{{Epoch: 5, FromUnixNano: epochStart.UnixNano(), ToUnixNano: epochStart.Add(24 * time.Hour).UnixNano()}}, st.Due,
		"the epoch stays owed, with its span")

	e, err := r.step()
	require.NoError(t, err)
	require.Equal(t, []uint64{5}, e)
	require.Len(t, r.chain.submitted, 3)
	require.Equal(t, r.chain.submitted[0], r.chain.submitted[1], "chunk 0 is resent byte for byte, which x/relay accepts as a repeat")
}

func TestStep_anIncompleteArchiveReportsNothingAndIsRetried(t *testing.T) {
	r := newRig(t)
	r.archive(seq(0, 10), three()...)
	_, _ = r.step()
	r.closeEpoch()
	_, err := r.step()
	require.ErrorIs(t, err, ErrIncompleteArchive)
	require.Empty(t, r.chain.submitted)

	r.archive(seq(10, 24), three()...)
	e, err := r.step()
	require.NoError(t, err, "the archive caught up")
	require.Equal(t, []uint64{5}, e)
}

func TestStep_missedEpochsAreNamedAndSkipped(t *testing.T) {
	r := newRig(t)
	_, _ = r.step()
	r.closeEpoch()
	r.closeEpoch()
	r.closeEpoch()
	_, err := r.step()
	require.ErrorIs(t, err, ErrEpochMissed)
	require.Empty(t, r.chain.submitted)

	st, err := loadState(r.home)
	require.NoError(t, err)
	require.EqualValues(t, 8, st.Seen, "the next pass is not stuck behind epochs whose span is lost")
}

func TestStep_aSettledEpochIsNotRetriedForever(t *testing.T) {
	r := newRig(t)
	r.archive(seq(0, 24), three()...)
	_, _ = r.step()
	r.closeEpoch()
	r.chain.settled[5] = true
	_, err := r.step()
	require.ErrorIs(t, err, ErrEpochSettled)
	st, err := loadState(r.home)
	require.NoError(t, err)
	require.EqualValues(t, 6, st.Seen)
	require.Empty(t, st.Due)
}

// An epoch that cannot be reported yet must not cost the epoch after it its
// span: the boundary is saved before any report is tried.
func TestStep_aFailingEpochDoesNotLoseTheNextOnesSpan(t *testing.T) {
	r := newRig(t)
	_, _ = r.step()
	r.closeEpoch()
	_, err := r.step()
	require.ErrorIs(t, err, ErrIncompleteArchive, "epoch 5 has no votes")
	r.closeEpoch()
	_, err = r.step()
	require.ErrorIs(t, err, ErrIncompleteArchive)
	require.NotErrorIs(t, err, ErrEpochMissed, "epoch 6 closed one epoch after the last pass saw its start")

	st, err := loadState(r.home)
	require.NoError(t, err)
	require.Len(t, st.Due, 2, "both closed epochs are owed with their spans")

	r.archive(seq(0, 24), three()...)
	r.archive(seq(24, 48), three()...)
	e, err := r.step()
	require.NoError(t, err)
	require.Equal(t, []uint64{5, 6}, e)
}

// The registry can change between the first chunk and a retry. The retry must
// send the entries it chose the first time, or the chain, which already holds
// chunk 0, refuses the new root.
func TestStep_aRetryResendsTheEntriesChosenFirst(t *testing.T) {
	r := newRig(t)
	r.archive(seq(0, 24), three()...)
	_, _ = r.step()
	r.closeEpoch()
	r.chain.failOn = 1
	_, err := r.step()
	require.Error(t, err)
	first := append([]*relaytypes.MsgReportEpoch(nil), r.chain.submitted...)
	require.Len(t, first, 1)

	one := fp(1)
	delete(r.chain.relays, string(one[:]))
	e, err := r.step()
	require.NoError(t, err)
	require.Equal(t, []uint64{5}, e)

	var got []relaytypes.RelayObservation
	for _, m := range r.chain.submitted[1:] {
		require.Equal(t, first[0].InputsRoot, m.InputsRoot, "the same report, so the chain completes it")
		got = append(got, m.Entries...)
	}
	require.Equal(t, first[0].Entries, r.chain.submitted[1].Entries, "chunk 0 is byte-identical")
	require.Len(t, got, 3, "relay 1 left the registry after the first attempt and is still in the report")
	_, err = os.Stat(reportPath(r.home, 5))
	require.ErrorIs(t, err, os.ErrNotExist, "the saved report is removed once the chain has it all")
}

func TestReport_aSavedReportThatIsNotJSONIsNamed(t *testing.T) {
	r := newRig(t)
	r.archive(seq(0, 24), three()...)
	_, _ = r.step()
	r.closeEpoch()
	require.NoError(t, os.WriteFile(reportPath(r.home, 5), []byte("{"), 0o640))
	_, err := r.step()
	require.ErrorContains(t, err, "report-5.json")
}

func TestStep_aChainBehindTheStateIsAnError(t *testing.T) {
	r := newRig(t)
	_, _ = r.step()
	r.chain.epoch.Number = 2
	_, err := r.step()
	require.ErrorContains(t, err, "behind")
}

func TestStep_chainErrorsAreReturned(t *testing.T) {
	r := newRig(t)
	r.chain.querying = errors.New("rpc down")
	_, err := r.step()
	require.ErrorContains(t, err, "rpc down")
}

func TestStep_aDamagedVoteIsNotReportedAround(t *testing.T) {
	r := newRig(t)
	r.archive(seq(0, 24), three()...)
	require.NoError(t, os.WriteFile(filepath.Join(r.votes, "bad"+VoteSuffix), []byte("not a vote"), 0o640))
	_, _ = r.step()
	r.closeEpoch()
	_, err := r.step()
	require.ErrorContains(t, err, "bad.vote")
	require.Empty(t, r.chain.submitted)
}

func TestStep_ignoresOtherFilesAndOtherAuthorities(t *testing.T) {
	r := newRig(t)
	r.archive(seq(0, 24), three()...)
	require.NoError(t, os.WriteFile(filepath.Join(r.votes, "consensus"), []byte("garbage"), 0o640))
	other := testVote("cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd", epochStart.Add(time.Hour), relaySpec{n: 1, flags: "Running", measured: "Measured=1"})
	require.NoError(t, os.WriteFile(filepath.Join(r.votes, "other"+VoteSuffix), []byte(other), 0o640))
	_, _ = r.step()
	r.closeEpoch()
	_, err := r.step()
	require.NoError(t, err)
	require.EqualValues(t, 100*NoramaPerWeight, r.chain.submitted[0].Entries[0].ConsensusWeight.Int64(),
		"another authority's vote does not move this authority's report")
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, v))
}

func TestNewRunner_validation(t *testing.T) {
	good := Config{Home: "h", VotesDir: "v", Reporter: account(t, 3), Operator: account(t, 2), Authority: testAuthority(), VoteInterval: time.Hour}
	_, err := NewRunner(good, &fakeChain{})
	require.NoError(t, err)

	for name, mutate := range map[string]func(*Config){
		"no home":          func(c *Config) { c.Home = "" },
		"no operator":      func(c *Config) { c.Operator = "" },
		"bad operator":     func(c *Config) { c.Operator = "nope" },
		"no reporter":      func(c *Config) { c.Reporter = "" },
		"no authority":     func(c *Config) { c.Authority = [fingerprintLen]byte{} },
		"no vote interval": func(c *Config) { c.VoteInterval = 0 },
		"negative chunk":   func(c *Config) { c.ChunkEntries = -1 },
		"oversized chunk":  func(c *Config) { c.ChunkEntries = relaytypes.MaxEntriesPerChunk + 1 },
	} {
		c := good
		mutate(&c)
		_, err := NewRunner(c, &fakeChain{})
		require.Error(t, err, name)
	}
	_, err = NewRunner(good, nil)
	require.Error(t, err)
}
