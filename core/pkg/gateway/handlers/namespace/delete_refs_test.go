package namespace

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// unpinRecorder is an IPFS client of which only Unpin is ever called; any other
// method panics through the nil embedded interface.
type unpinRecorder struct {
	ipfs.IPFSClient
	cids []string
	err  error
}

func (u *unpinRecorder) Pin(context.Context, string, string, int) (*ipfs.PinResponse, error) {
	return &ipfs.PinResponse{}, nil
}

func (u *unpinRecorder) PinStatus(context.Context, string) (*ipfs.PinStatus, error) {
	return nil, nil
}

func (u *unpinRecorder) Unpin(_ context.Context, cid string) error {
	u.cids = append(u.cids, cid)
	return u.err
}

func addRefs(t *testing.T, h *DeleteHandler, ns string, cids ...string) {
	t.Helper()
	for _, cid := range cids {
		if err := h.refs.Register(context.Background(), cid, ns, "storage"); err != nil {
			t.Fatal(err)
		}
	}
}

// Bug: namespace delete decided "last pinner" from the registry's own
// ipfs_content_ownership and deployments rows, which hold nothing of a tenant
// namespace, so it removed no pin it should and could remove one another
// namespace still holds. The index says who holds a CID.
func TestUnpinNamespaceContent_removesOnlyWhatNoOtherNamespaceHolds(t *testing.T) {
	db := migratedDB(t)
	rec := &unpinRecorder{}
	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), rec, nil, zap.NewNop())
	addRefs(t, h, "gone", "QmOnlyGone", "QmShared")
	addRefs(t, h, "keep", "QmShared", "QmKeepOnly")

	h.unpinNamespaceContent(context.Background(), "gone")

	if len(rec.cids) != 1 || rec.cids[0] != "QmOnlyGone" {
		t.Fatalf("unpinned %v, want only QmOnlyGone", rec.cids)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM ipfs_cid_refs WHERE namespace = 'gone'`); n != 0 {
		t.Errorf("the deleted namespace still holds %d references", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM ipfs_cid_refs WHERE namespace = 'keep'`); n != 2 {
		t.Errorf("the other namespace holds %d references, want 2", n)
	}
}

func TestUnpinNamespaceContent_everyOrphanedCIDIsUnpinned(t *testing.T) {
	db := migratedDB(t)
	rec := &unpinRecorder{err: errors.New("cluster down")}
	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), rec, nil, zap.NewNop())
	addRefs(t, h, "gone", "QmA", "QmB")

	h.unpinNamespaceContent(context.Background(), "gone")

	sort.Strings(rec.cids)
	if len(rec.cids) != 2 {
		t.Fatalf("a failing unpin stopped the rest: %v", rec.cids)
	}
}

func TestUnpinNamespaceContent_noContentAndNoIPFS(t *testing.T) {
	db := migratedDB(t)
	rec := &unpinRecorder{}
	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), rec, nil, zap.NewNop())
	h.unpinNamespaceContent(context.Background(), "empty")
	if len(rec.cids) != 0 {
		t.Fatalf("unpinned %v for a namespace with nothing", rec.cids)
	}

	noIPFS := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), nil, nil, zap.NewNop())
	addRefs(t, noIPFS, "gone", "QmA")
	noIPFS.unpinNamespaceContent(context.Background(), "gone")
	if n := countRows(t, db, `SELECT COUNT(*) FROM ipfs_cid_refs WHERE namespace = 'gone'`); n != 0 {
		t.Errorf("references left: %d", n)
	}
}
