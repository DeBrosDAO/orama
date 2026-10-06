package provider

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	gocid "github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// fakePins is the public Kubo as the runner sees it.
type fakePins struct {
	adds, pins, unpins []string
	cid                string
	addErr, pinErr     error
	unpinErr           error
	pinnedBytes        map[string][]byte
}

func (f *fakePins) Add(_ context.Context, data []byte) (string, error) {
	if f.addErr != nil {
		return "", f.addErr
	}
	f.adds = append(f.adds, f.cid)
	if f.pinnedBytes == nil {
		f.pinnedBytes = map[string][]byte{}
	}
	f.pinnedBytes[f.cid] = data
	return f.cid, nil
}

func (f *fakePins) Pin(_ context.Context, cid string) error {
	if f.pinErr != nil {
		return f.pinErr
	}
	f.pins = append(f.pins, cid)
	return nil
}

func (f *fakePins) Unpin(_ context.Context, cid string) error {
	if f.unpinErr != nil {
		return f.unpinErr
	}
	f.unpins = append(f.unpins, cid)
	return nil
}

// dealChain is the chain reads and writes a decision needs.
type dealChain struct {
	Chain
	class types.DealClass
	// classes overrides class for a deal id.
	classes   map[uint64]types.DealClass
	submitted []sdk.Msg
}

func (c *dealChain) Deal(_ context.Context, id uint64) (types.Deal, error) {
	if class, ok := c.classes[id]; ok {
		return types.Deal{Class: class}, nil
	}
	return types.Deal{Class: c.class}, nil
}

func (c *dealChain) Submit(_ context.Context, msgs ...sdk.Msg) error {
	c.submitted = append(c.submitted, msgs...)
	return nil
}

type publicFixture struct {
	store  *Store
	runner *Runner
	chain  *dealChain
	pins   *fakePins
	name   string
	root   []byte
	slot   types.Slot
	params types.Params
}

func newPublicFixture(t *testing.T, class types.DealClass, deny ...string) *publicFixture {
	t.Helper()
	store, err := Open(t.TempDir(), deny, nil)
	require.NoError(t, err)
	pins := &fakePins{cid: testCID}
	store.SetUnpinner(pins)
	chain := &dealChain{class: class}
	runner, err := NewRunner(store, chain, Config{
		NodeID: "n1", Signer: "orama1signer", StatePath: filepath.Join(t.TempDir(), "state.json"), StartHeight: 1, Pins: pins,
	})
	require.NoError(t, err)
	data := make([]byte, 3000)
	data[0] = 7
	name, root := rootHex(t, data)
	dec, err := store.Ingest(name, data, root)
	require.NoError(t, err)
	require.True(t, dec.Accept)
	return &publicFixture{
		store: store, runner: runner, chain: chain, pins: pins, name: name, root: root,
		slot:   types.Slot{DealId: 5, Index: 1, NodeId: "n1", PieceRoot: root, AssignHeight: 100},
		params: types.Params{AcceptWindowBlocks: 50},
	}
}

func (f *publicFixture) decide(t *testing.T, latest int64) error {
	t.Helper()
	return f.runner.decide(context.Background(), latest, f.params, f.slot)
}

func TestDecide_publicDealsArePinnedInKuboBeforeTheyAreAccepted(t *testing.T) {
	for _, class := range []types.DealClass{types.DealClass_DEAL_CLASS_PUBLIC_PIN, types.DealClass_DEAL_CLASS_ARCHIVE} {
		f := newPublicFixture(t, class)
		require.NoError(t, f.decide(t, 101))
		require.Len(t, f.pins.adds, 1, class.String())
		require.Equal(t, f.pins.pinnedBytes[testCID], mustRead(t, f.store, f.name), "Kubo got the verified piece bytes")
		require.Equal(t, []ipfsPin{{CID: testCID, Pinned: true}}, mustPins(t, f.store, f.name))
		require.Len(t, f.chain.submitted, 1)
		require.IsType(t, &types.MsgAcceptDeal{}, f.chain.submitted[0])
	}
}

func TestDecide_aPrivateDealNeverReachesThePublicKubo(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PRIVATE)
	require.NoError(t, f.decide(t, 101))
	require.Empty(t, f.pins.adds)
	require.Empty(t, f.pins.pins)
	require.Empty(t, mustPins(t, f.store, f.name))
	require.IsType(t, &types.MsgAcceptDeal{}, f.chain.submitted[0])
}

func TestDecide_aNodeWithoutKuboAcceptsFromTheStoreAlone(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	f.runner.pins = nil
	require.NoError(t, f.decide(t, 101))
	require.IsType(t, &types.MsgAcceptDeal{}, f.chain.submitted[0])
}

func TestDecide_aFetchedCIDIsPinnedNotReAdded(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	require.NoError(t, f.store.AddIPFS(f.name, testCID))
	require.NoError(t, f.decide(t, 101))
	require.Equal(t, []string{testCID}, f.pins.pins)
	require.Empty(t, f.pins.adds)
	require.Equal(t, []ipfsPin{{CID: testCID, Pinned: true}}, mustPins(t, f.store, f.name))
}

func TestDecide_aPinFailureRetriesUntilTheWindowIsAboutToClose(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	f.pins.addErr = errors.New("kubo down")
	require.ErrorContains(t, f.decide(t, 101), "kubo down")
	require.Empty(t, f.chain.submitted, "no accept without a pin, and no decline while there is time")
	require.True(t, f.store.Has(f.name))

	// The window closes at 150; the runner declines DeclineMarginBlocks before.
	require.NoError(t, f.decide(t, 150-DeclineMarginBlocks))
	require.Len(t, f.chain.submitted, 1)
	decline, ok := f.chain.submitted[0].(*types.MsgDeclineDeal)
	require.True(t, ok)
	require.Equal(t, ReasonPinFailed, decline.Reason)
	require.False(t, f.store.Has(f.name), "a declined piece is not kept")
}

func TestDecide_aDenylistedCIDIsUnpinnedAndDeclined(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_ARCHIVE, testCID)
	require.NoError(t, f.decide(t, 101))
	require.Equal(t, []string{testCID}, f.pins.unpins)
	decline, ok := f.chain.submitted[0].(*types.MsgDeclineDeal)
	require.True(t, ok)
	require.Equal(t, ReasonDenylist, decline.Reason)
	require.False(t, f.store.Has(f.name))
}

func TestRelease_unpinsOnlyWhenTheLastSlotGoes(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	require.NoError(t, f.decide(t, 101))
	require.NoError(t, f.store.Bind(f.name, 9, 0))

	require.NoError(t, f.store.Release(5, 1, nil))
	require.Empty(t, f.pins.unpins, "deal 9 still binds the piece")
	require.NoError(t, f.store.Release(9, 0, nil))
	require.Equal(t, []string{testCID}, f.pins.unpins)
	require.False(t, f.store.Has(f.name))
}

func TestRelease_aFailedUnpinKeepsTheBindingSoTheNextSweepRetries(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	require.NoError(t, f.decide(t, 101))
	f.pins.unpinErr = errors.New("kubo busy")
	require.ErrorContains(t, f.store.Release(5, 1, nil), "kubo busy")
	_, bound, err := f.store.Lookup(5, 1)
	require.NoError(t, err)
	require.True(t, bound)
	require.True(t, f.store.Has(f.name))

	f.pins.unpinErr = nil
	require.NoError(t, f.store.Release(5, 1, nil))
	require.Equal(t, []string{testCID}, f.pins.unpins)
}

func TestAddIPFS_isIdempotentAndBounded(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	require.NoError(t, f.store.AddIPFS(f.name, testCID))
	require.NoError(t, f.store.AddIPFS(f.name, testCID))
	require.Len(t, mustPins(t, f.store, f.name), 1)
	for i := 1; i < MaxPieceCIDs; i++ {
		require.NoError(t, f.store.AddIPFS(f.name, fmt.Sprintf("bafkrei%052d", i)))
	}
	require.ErrorContains(t, f.store.AddIPFS(f.name, "bafkrei"+strings.Repeat("a", 52)), "already has")
	require.Error(t, f.store.AddIPFS("missing", testCID))
}

func TestDecide_everyFetchedCIDIsPinned(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	other := "bafkrei" + strings.Repeat("b", 52)
	require.NoError(t, f.store.AddIPFS(f.name, testCID))
	require.NoError(t, f.store.AddIPFS(f.name, other))
	require.NoError(t, f.decide(t, 101))
	require.Equal(t, []string{testCID, other}, f.pins.pins)
	require.Empty(t, f.pins.adds)
	require.Equal(t, []ipfsPin{{CID: testCID, Pinned: true}, {CID: other, Pinned: true}}, mustPins(t, f.store, f.name))
}

func TestDecide_aPublicDealCannotPublishAPrivateDealsCiphertext(t *testing.T) {
	// A private slot holds the piece first; a public deal with the same root comes later.
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PRIVATE)
	require.NoError(t, f.decide(t, 101))
	require.Len(t, f.chain.submitted, 1)

	f.chain.classes = map[uint64]types.DealClass{5: types.DealClass_DEAL_CLASS_PRIVATE, 6: types.DealClass_DEAL_CLASS_PUBLIC_PIN}
	f.slot.DealId = 6
	require.NoError(t, f.decide(t, 101))
	require.Empty(t, f.pins.adds, "the private piece reached the public Kubo")
	decline, ok := f.chain.submitted[1].(*types.MsgDeclineDeal)
	require.True(t, ok)
	require.Equal(t, ReasonPrivateRoot, decline.Reason)
	require.True(t, f.store.Has(f.name), "the private slot still needs the piece")
}

func TestDecide_aPublicSlotDecidedBeforeAPrivateOneWaitingBesideItStillDoesNotPublish(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	name := hex.EncodeToString(f.root)
	f.runner.state.Pending = []pendingSlot{
		{DealID: 5, Slot: 1, Root: name, Class: int32(types.DealClass_DEAL_CLASS_PUBLIC_PIN)},
		{DealID: 9, Slot: 0, Root: name, Class: int32(types.DealClass_DEAL_CLASS_PRIVATE)},
	}
	require.NoError(t, f.decide(t, 101))
	require.Empty(t, f.pins.adds)
	decline, ok := f.chain.submitted[0].(*types.MsgDeclineDeal)
	require.True(t, ok)
	require.Equal(t, ReasonPrivateRoot, decline.Reason)
	require.False(t, f.runner.AssignedPublic(name), "POST /pins refuses a root a private slot waits for")

	require.Len(t, f.runner.state.Pending, 1, "the declined slot left the waiting list")
	f.runner.state.Pending[0].Class = 0
	f.chain.submitted = nil
	require.ErrorContains(t, f.decide(t, 101), "not read yet", "an unread class is retried, not guessed")
	require.Empty(t, f.chain.submitted)
}

func TestNoteClasses_readsEveryWaitingSlotsClassBeforeAnyIsDecided(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	f.chain.classes = map[uint64]types.DealClass{9: types.DealClass_DEAL_CLASS_PRIVATE}
	name := hex.EncodeToString(f.root)
	f.runner.state.Pending = []pendingSlot{{DealID: 5, Slot: 1, Root: name}, {DealID: 9, Slot: 0, Root: name}}
	require.NoError(t, f.runner.noteClasses(context.Background()))
	require.Equal(t, int32(types.DealClass_DEAL_CLASS_PUBLIC_PIN), f.runner.state.Pending[0].Class)
	require.Equal(t, int32(types.DealClass_DEAL_CLASS_PRIVATE), f.runner.state.Pending[1].Class)

	f.runner.pins = nil
	f.runner.state.Pending[0].Class = 0
	require.NoError(t, f.runner.noteClasses(context.Background()))
	require.Zero(t, f.runner.state.Pending[0].Class, "a node with no public Kubo reads no class")
}

func TestDecide_aPrivateSlotUnpublishesAPieceAnEarlierPublicDealPinned(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	require.NoError(t, f.decide(t, 101))
	require.Equal(t, []ipfsPin{{CID: testCID, Pinned: true}}, mustPins(t, f.store, f.name))

	f.chain.classes = map[uint64]types.DealClass{7: types.DealClass_DEAL_CLASS_PRIVATE}
	f.slot.DealId = 7
	f.slot.Index = 0
	require.NoError(t, f.decide(t, 101))
	require.Equal(t, []string{testCID}, f.pins.unpins, "the ciphertext left the public Kubo")
	require.Empty(t, mustPins(t, f.store, f.name))
}

func TestDenylist_aCIDv0AndTheCIDv1OfTheSameHashAreOneEntry(t *testing.T) {
	v0, err := gocid.V0Builder{}.Sum([]byte("denied content"))
	require.NoError(t, err)
	v1 := gocid.NewCidV1(gocid.DagProtobuf, v0.Hash())
	other, err := gocid.V0Builder{}.Sum([]byte("other content"))
	require.NoError(t, err)
	store, err := Open(t.TempDir(), []string{"# comment", v0.String(), "  ", "0123abcd"}, nil)
	require.NoError(t, err)
	require.True(t, store.Denied(v0.String()))
	require.True(t, store.Denied(v1.String()), "the other spelling of the same hash is denied too")
	require.False(t, store.Denied(other.String()))
	require.True(t, store.Denied("0123abcd"), "a piece root is compared as written")
	require.False(t, store.Denied(testCID))
}

func TestDecide_theCIDIsRecordedBeforeADenylistCheckCanFail(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_ARCHIVE, testCID)
	f.pins.unpinErr = errors.New("kubo busy")
	require.ErrorContains(t, f.decide(t, 101), "kubo busy")
	require.Equal(t, []ipfsPin{{CID: testCID}}, mustPins(t, f.store, f.name), "the pin is on record, so it can be removed later")

	// The retry must not accept a piece whose recorded CID is denied.
	f.pins.unpinErr = nil
	require.NoError(t, f.decide(t, 101))
	require.Empty(t, f.pins.pins, "a denied CID is not pinned again")
	decline, ok := f.chain.submitted[0].(*types.MsgDeclineDeal)
	require.True(t, ok)
	require.Equal(t, ReasonDenylist, decline.Reason)
	require.Equal(t, []string{testCID}, f.pins.unpins)
}

func TestDeclineStored_aFailedUnpinIsRetriedAndNothingIsDeclinedUntilItWorks(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN, testCID)
	f.pins.unpinErr = errors.New("kubo busy")
	require.ErrorContains(t, f.decide(t, 101), "kubo busy")
	require.Empty(t, f.chain.submitted)
	require.True(t, f.store.Has(f.name), "the piece and its record stay so the unpin can be retried")

	f.pins.unpinErr = nil
	require.NoError(t, f.decide(t, 101))
	require.Equal(t, []string{testCID}, f.pins.unpins)
	require.False(t, f.store.Has(f.name))
	require.IsType(t, &types.MsgDeclineDeal{}, f.chain.submitted[0])
}

func TestDeclineStored_keepsAPieceAnotherWaitingSlotNeeds(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	f.pins.addErr = errors.New("kubo down")
	f.runner.state.Pending = []pendingSlot{
		{DealID: 5, Slot: 1, Root: hex.EncodeToString(f.root)},
		{DealID: 8, Slot: 0, Root: hex.EncodeToString(f.root)},
	}
	require.NoError(t, f.decide(t, 150-DeclineMarginBlocks))
	require.IsType(t, &types.MsgDeclineDeal{}, f.chain.submitted[0])
	require.True(t, f.store.Has(f.name), "deal 8 still waits for this piece")
}

func TestDiscard_removesOnlyAnUnboundPieceAndItsPins(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	require.NoError(t, f.decide(t, 101))
	require.NoError(t, f.store.Discard(f.name))
	require.True(t, f.store.Has(f.name), "a bound piece is kept")
	require.Empty(t, f.pins.unpins)

	require.NoError(t, f.store.Release(5, 1, func(string) bool { return true }))
	require.NoError(t, f.store.Discard(f.name))
	require.False(t, f.store.Has(f.name))
	require.Equal(t, []string{testCID}, f.pins.unpins)
	require.NoError(t, f.store.Discard(f.name), "discarding a piece that is gone is a no-op")
}

func TestAssignedPublic_needsAPublicClassRead(t *testing.T) {
	f := newPublicFixture(t, types.DealClass_DEAL_CLASS_PUBLIC_PIN)
	name := hex.EncodeToString(f.root)
	f.runner.state.Pending = []pendingSlot{{DealID: 5, Slot: 1, Root: name}}
	require.True(t, f.runner.Assigned(name))
	require.False(t, f.runner.AssignedPublic(name), "class not read yet")
	require.NoError(t, f.runner.notePendingClass(5, 1, types.DealClass_DEAL_CLASS_PRIVATE))
	require.False(t, f.runner.AssignedPublic(name))
	require.NoError(t, f.runner.notePendingClass(5, 1, types.DealClass_DEAL_CLASS_ARCHIVE))
	require.True(t, f.runner.AssignedPublic(name))
	require.False(t, f.runner.AssignedPublic("other"))
}

// fakeCat serves fixed bytes for a CID.
type fakeCat struct {
	body []byte
	err  error
	max  int64
	cid  string
}

func (c *fakeCat) Cat(_ context.Context, cid string, max int64) ([]byte, error) {
	c.cid, c.max = cid, max
	return c.body, c.err
}

func postPin(h http.Handler, name, cid string) int {
	req := httptest.NewRequest(http.MethodPost, "/pins/"+name, nil)
	req.RemoteAddr = "203.0.113.7:4000"
	if cid != "" {
		req.Header.Set("X-Piece-CID", cid)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func mustPins(t *testing.T, s *Store, name string) []ipfsPin {
	t.Helper()
	pins, err := s.IPFSPins(name)
	require.NoError(t, err)
	return pins
}

func newPinHandler(t *testing.T, cat *fakeCat, deny []string, assigned func(string) bool) (*Retrieval, *Store) {
	t.Helper()
	store, err := Open(t.TempDir(), deny, nil)
	require.NoError(t, err)
	h, err := NewRetrieval(store, 100, 20, 8)
	require.NoError(t, err)
	require.Error(t, h.AcceptPins(cat, assigned), "pins need uploads configured first")
	require.NoError(t, h.AcceptUploads(4096, func(string) bool { return false }))
	require.Error(t, h.AcceptPins(nil, assigned))
	require.Error(t, h.AcceptPins(cat, nil))
	require.NoError(t, h.AcceptPins(cat, assigned))
	return h, store
}

func TestPin_fetchesByCIDChecksTheRootAndRecordsTheCID(t *testing.T) {
	data := make([]byte, 2500)
	data[3] = 9
	name, _ := rootHex(t, data)
	cat := &fakeCat{body: data}
	h, store := newPinHandler(t, cat, nil, func(n string) bool { return n == name })

	require.Equal(t, http.StatusNoContent, postPin(h, name, testCID))
	require.Equal(t, testCID, cat.cid)
	require.Equal(t, int64(4096), cat.max, "the fetch is bounded by the upload limit")
	require.True(t, store.Has(name))
	require.Equal(t, []ipfsPin{{CID: testCID}}, mustPins(t, store, name), "the runner pins after it reads the deal class")
	cat.cid = ""
	require.Equal(t, http.StatusNoContent, postPin(h, name, testCID), "the same pin again is a no-op")
	require.Empty(t, cat.cid, "a CID already recorded for the piece is not fetched again")
}

func TestPin_refusalsStoreNothing(t *testing.T) {
	data := make([]byte, 2500)
	name, _ := rootHex(t, data)
	otherName, _ := rootHex(t, []byte("other piece"))
	cat := &fakeCat{body: data}
	h, store := newPinHandler(t, cat, []string{"bafkreidenied"}, func(n string) bool { return n == name })

	require.Equal(t, http.StatusForbidden, postPin(h, otherName, testCID), "not assigned to a public deal")
	require.Equal(t, http.StatusBadRequest, postPin(h, name, ""), "no CID")
	require.Equal(t, http.StatusBadRequest, postPin(h, name, "../etc/passwd"), "not a CID")
	require.Equal(t, http.StatusBadRequest, postPin(h, "not-hex", testCID), "bad root")

	cat.body = []byte("different bytes")
	require.Equal(t, http.StatusConflict, postPin(h, name, testCID), "bytes that do not hash to the assigned root")
	require.False(t, store.Has(name))

	cat.body, cat.err = data, errors.New("bitswap timeout")
	require.Equal(t, http.StatusBadGateway, postPin(h, name, testCID))
	require.False(t, store.Has(name))
}

func TestPin_denylistedCIDIsRefusedBeforeAnyFetch(t *testing.T) {
	name, _ := rootHex(t, make([]byte, 2500))
	cat := &fakeCat{}
	h, _ := newPinHandler(t, cat, []string{testCID}, func(string) bool { return true })
	require.Equal(t, http.StatusConflict, postPin(h, name, testCID))
	require.Empty(t, cat.cid, "Kubo was not asked for a denied CID")
}

func TestPin_isNotServedUntilConfigured(t *testing.T) {
	store, err := Open(t.TempDir(), nil, nil)
	require.NoError(t, err)
	h, err := NewRetrieval(store, 100, 20, 8)
	require.NoError(t, err)
	require.NoError(t, h.AcceptUploads(10, func(string) bool { return true }))
	name, _ := rootHex(t, []byte("x"))
	require.Equal(t, http.StatusMethodNotAllowed, postPin(h, name, testCID))
}

func mustRead(t *testing.T, s *Store, name string) []byte {
	t.Helper()
	b, err := s.ReadPiece(name)
	require.NoError(t, err)
	return b
}

func TestPin_oneFetchPerRootAndPinsHaveTheirOwnSlots(t *testing.T) {
	name, _ := rootHex(t, make([]byte, 2500))
	cat := &fakeCat{}
	h, _ := newPinHandler(t, cat, nil, func(string) bool { return true })
	require.True(t, h.startPin(name))
	require.Equal(t, http.StatusTooManyRequests, postPin(h, name, testCID), "a fetch of this root is already running")
	h.endPin(name)

	for i := 0; i < MaxConcurrentPins; i++ {
		h.pins <- struct{}{}
	}
	require.Equal(t, http.StatusServiceUnavailable, postPin(h, name, testCID))
	require.Len(t, h.uploads, 0, "pins do not take upload slots")
	for i := 0; i < MaxConcurrentPins; i++ {
		<-h.pins
	}
}

func TestPin_aCIDPastTheCapIsRefused(t *testing.T) {
	data := make([]byte, 2500)
	name, _ := rootHex(t, data)
	h, store := newPinHandler(t, &fakeCat{body: data}, nil, func(string) bool { return true })
	require.Equal(t, http.StatusNoContent, postPin(h, name, testCID))
	for i := 1; i < MaxPieceCIDs; i++ {
		require.NoError(t, store.AddIPFS(name, fmt.Sprintf("bafkrei%052d", i)))
	}
	require.Equal(t, http.StatusConflict, postPin(h, name, "bafkrei"+strings.Repeat("c", 52)))
}

func TestStore_concurrentUploadsAndRecordUpdatesLoseNothing(t *testing.T) {
	store, err := Open(t.TempDir(), nil, nil)
	require.NoError(t, err)
	data := make([]byte, 3000)
	data[1] = 3
	name, root := rootHex(t, data)
	_, err = store.Ingest(name, data, root)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = store.Ingest(name, data, root)
			_ = store.AddIPFS(name, fmt.Sprintf("bafkrei%052d", i%MaxPieceCIDs))
			_ = store.MarkPinned(name, fmt.Sprintf("bafkrei%052d", i%MaxPieceCIDs))
			_, _ = store.IPFSPins(name)
		}(i)
	}
	wg.Wait()
	pins := mustPins(t, store, name)
	require.Len(t, pins, MaxPieceCIDs, "every CID survived, none was overwritten by a racing writer")
	for _, p := range pins {
		require.True(t, p.Pinned)
	}
	require.True(t, store.Has(name))
}
