package archiver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/provider"
	"github.com/DeBrosOfficial/network/chain/repair"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// fakeProvider is a real provider piece handler on an httptest server. ready reports whether its
// runner has read the assignment of a root yet.
type fakeProvider struct {
	store *provider.Store
	url   string
	posts atomic.Int32
}

func newFakeProvider(t *testing.T, ready func(string) bool) *fakeProvider {
	t.Helper()
	store, err := provider.Open(t.TempDir(), nil, nil)
	require.NoError(t, err)
	rt, err := provider.NewRetrieval(store, 1000, 1000, 8)
	require.NoError(t, err)
	require.NoError(t, rt.AcceptUploads(1<<30, ready))
	p := &fakeProvider{store: store}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			p.posts.Add(1)
		}
		rt.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	p.url = srv.URL
	return p
}

func readMonitor(t *testing.T, dir string) Monitor {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "monitor.json"))
	require.NoError(t, err)
	var m Monitor
	require.NoError(t, json.Unmarshal(body, &m))
	return m
}

func always(string) bool { return true }

// assignSlots gives every deal of the chain one ASSIGNED slot held by node, as x/storage does,
// with the piece root root.
func assignSlots(chain *fakeChain, node string, root []byte) {
	chain.slots = map[uint64][]storagetypes.Slot{}
	for id := range chain.deals {
		chain.slots[id] = []storagetypes.Slot{{
			DealId: id, Index: 0, NodeId: node, PieceRoot: root, Status: storagetypes.SlotStatus_SLOT_STATUS_ASSIGNED,
		}}
	}
	chain.assign()
}

func newUploadRunner(t *testing.T, chain *fakeChain, dir string) *Runner {
	t.Helper()
	r, err := NewRunner(chain, repair.HTTP{Client: http.DefaultClient}, "orama1archiver", "node-1", dir, 10)
	require.NoError(t, err)
	r.sleep = func(context.Context, time.Duration) error { return nil }
	return r
}

func bundleRoot(t *testing.T, dir string) []byte {
	t.Helper()
	body, err := os.ReadFile(BundlePath(dir, 1, 10))
	require.NoError(t, err)
	c, err := piece.Commit(body)
	require.NoError(t, err)
	return c.Root
}

func TestRunner_uploadsTheBundleToTheAssignedProviderOnlyOnceAssigned(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	r := newUploadRunner(t, chain, dir)
	prov := newFakeProvider(t, always)
	chain.urls = map[string]string{"prov-1": prov.url}

	stepOnce(t, r)
	require.Zero(t, prov.posts.Load(), "an OPEN deal has no provider to upload to")

	root := bundleRoot(t, dir)
	assignSlots(chain, "prov-1", root)
	stepOnce(t, r)
	require.Equal(t, int32(3), prov.posts.Load(), "one upload per deal slot")
	body, err := os.ReadFile(BundlePath(dir, 1, 10))
	require.NoError(t, err)
	name := hex.EncodeToString(root)
	require.True(t, prov.store.Has(name))
	gotRoot, err := prov.store.ReadRoot(name)
	require.NoError(t, err)
	require.Equal(t, root, gotRoot)
	require.NotEmpty(t, body)
	require.Equal(t, uint64(3), r.piecesUploaded)
	require.Equal(t, 1, chain.attaches, "the deals are recorded after the upload")

	m := readMonitor(t, dir)
	require.Equal(t, uint64(3), m.PiecesUploaded)
	require.Zero(t, m.UploadFailures)
}

func TestRunner_uploadIsIdempotentAcrossPassesAndARestart(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	r := newUploadRunner(t, chain, dir)
	prov := newFakeProvider(t, always)
	chain.urls = map[string]string{"prov-1": prov.url}
	stepOnce(t, r)
	assignSlots(chain, "prov-1", bundleRoot(t, dir))
	// A block later the deals are ACTIVE but the archiver's attach fails, so the deals stay tracked.
	chain.fail = errors.New("not included")
	_, err := r.Step(context.Background())
	require.Error(t, err)
	require.Equal(t, int32(3), prov.posts.Load())

	restarted := newUploadRunner(t, chain, dir)
	stepOnce(t, restarted)
	require.Equal(t, int32(3), prov.posts.Load(), "a restarted archiver does not upload the bundle again")
	require.Zero(t, restarted.piecesUploaded)
}

func TestRunner_skipsSlotsTheProviderAlreadyAccepted(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	r := newUploadRunner(t, chain, dir)
	prov := newFakeProvider(t, always)
	chain.urls = map[string]string{"prov-1": prov.url}
	stepOnce(t, r)
	assignSlots(chain, "prov-1", bundleRoot(t, dir))
	for id := range chain.slots {
		chain.slots[id][0].Accepted = true
	}
	stepOnce(t, r)
	require.Zero(t, prov.posts.Load())
}

func TestRunner_refusesToSendBytesThatAreNotTheAssignedRoot(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	r := newUploadRunner(t, chain, dir)
	prov := newFakeProvider(t, always)
	chain.urls = map[string]string{"prov-1": prov.url}
	stepOnce(t, r)
	assignSlots(chain, "prov-1", make([]byte, 32))

	_, err := r.Step(context.Background())
	require.ErrorContains(t, err, "nothing was sent")
	require.Zero(t, prov.posts.Load())
	require.Equal(t, uint64(3), r.uploadFailures)
	require.Equal(t, uint64(3), readMonitor(t, dir).UploadFailures)
}

func TestRunner_retriesAProviderThatHasNotReadTheAssignmentYet(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	r := newUploadRunner(t, chain, dir)
	var asks atomic.Int32
	prov := newFakeProvider(t, func(string) bool { return asks.Add(1) > 2 })
	chain.urls = map[string]string{"prov-1": prov.url}
	stepOnce(t, r)
	root := bundleRoot(t, dir)
	assignSlots(chain, "prov-1", root)
	delete(chain.slots, 2)
	delete(chain.slots, 3)

	stepOnce(t, r)
	require.Equal(t, int32(3), prov.posts.Load(), "two refusals, then the accepted upload")
	require.True(t, prov.store.Has(hex.EncodeToString(root)))
}

func TestRunner_aProviderThatNeverAcceptsFailsThePassAndIsTriedAgainNextPass(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	r := newUploadRunner(t, chain, dir)
	var open atomic.Bool
	prov := newFakeProvider(t, func(string) bool { return open.Load() })
	chain.urls = map[string]string{"prov-1": prov.url}
	stepOnce(t, r)
	assignSlots(chain, "prov-1", bundleRoot(t, dir))
	delete(chain.slots, 2)
	delete(chain.slots, 3)

	_, err := r.Step(context.Background())
	require.Error(t, err)
	require.Equal(t, int32(uploadAttempts), prov.posts.Load())
	require.Equal(t, uint64(1), r.uploadFailures)

	open.Store(true)
	stepOnce(t, r)
	require.Equal(t, uint64(1), r.piecesUploaded)
}

func TestRunner_aSlotMovedToAnotherNodeGetsTheBundleToo(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	r := newUploadRunner(t, chain, dir)
	a, b := newFakeProvider(t, always), newFakeProvider(t, always)
	chain.urls = map[string]string{"prov-1": a.url, "prov-2": b.url}
	stepOnce(t, r)
	root := bundleRoot(t, dir)
	assignSlots(chain, "prov-1", root)
	chain.fail = errors.New("not included")
	_, _ = r.Step(context.Background())
	require.Equal(t, int32(3), a.posts.Load())

	for id := range chain.slots {
		chain.slots[id][0].NodeId = "prov-2"
	}
	stepOnce(t, r)
	require.Equal(t, int32(3), b.posts.Load())
}

func TestNewRunner_needsAnUploader(t *testing.T) {
	_, err := NewRunner(newFakeChain(t, 1), nil, "orama1a", "node-1", t.TempDir(), 10)
	require.ErrorContains(t, err, "uploader")
}
