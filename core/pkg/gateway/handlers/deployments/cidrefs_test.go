package deployments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/storage"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

// refsService is a service whose database is the real schema and which is its
// own registry, as the index gateway is.
func refsService(t *testing.T, rows ...[2]string) (*DeploymentService, *storage.CIDRefs) {
	t.Helper()
	svc := registryWith(t, rows...)
	refs := storage.NewCIDRefs(svc.db)
	svc.SetCIDRefs(refs)
	// Every live namespace has loaded its existing references.
	if _, err := svc.db.Exec(context.Background(),
		`INSERT INTO ipfs_cid_refs (cid, namespace, kind) SELECT '', name, 'backfilled' FROM namespaces`); err != nil {
		t.Fatal(err)
	}
	return svc, refs
}

func refCount(t *testing.T, refs *storage.CIDRefs, cid string) int {
	t.Helper()
	n, err := refs.Count(context.Background(), cid)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

type unpinRecorder struct{ cids []string }

func (u *unpinRecorder) Pin(context.Context, string, string, int) (*ipfs.PinResponse, error) {
	return &ipfs.PinResponse{}, nil
}

func (u *unpinRecorder) PinStatus(context.Context, string) (*ipfs.PinStatus, error) {
	return nil, nil
}

func (u *unpinRecorder) Unpin(_ context.Context, cid string) error {
	u.cids = append(u.cids, cid)
	return nil
}

// Bug: a deployment's content was counted only in the tenant's own database, so
// a namespace whose deployment served a CID another namespace held (or the
// reverse) lost the pin when either let go.
func TestReleaseCID_anotherNamespacesReferenceKeepsThePin(t *testing.T) {
	svc, refs := refsService(t)
	ctx := context.Background()
	if _, err := svc.registerCIDs(ctx, "acme", "QmShared"); err != nil {
		t.Fatal(err)
	}
	if err := refs.Register(ctx, "QmShared", "other", storage.KindStorage); err != nil {
		t.Fatal(err)
	}
	rec := &unpinRecorder{}
	if err := svc.releaseCID(ctx, rec, "d1", "acme", "QmShared"); err != nil {
		t.Fatal(err)
	}
	if len(rec.cids) != 0 {
		t.Fatalf("the pin another namespace holds was removed: %v", rec.cids)
	}
	if _, err := refs.Release(ctx, "QmShared", "other", storage.KindStorage); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseCID_lastReferenceUnpins(t *testing.T) {
	svc, _ := refsService(t)
	ctx := context.Background()
	if _, err := svc.registerCIDs(ctx, "acme", "QmOnly"); err != nil {
		t.Fatal(err)
	}
	rec := &unpinRecorder{}
	if err := svc.releaseCID(ctx, rec, "d1", "acme", "QmOnly"); err != nil {
		t.Fatal(err)
	}
	if len(rec.cids) != 1 || rec.cids[0] != "QmOnly" {
		t.Fatalf("unpinned %v, want QmOnly", rec.cids)
	}
}

// Two deployments of one namespace serving the same CID share the index's one
// (cid, namespace, kind) row, so letting go of one must not drop it.
func TestReleaseCID_anotherDeploymentOfTheNamespaceKeepsIt(t *testing.T) {
	svc, refs := refsService(t)
	ctx := context.Background()
	for _, id := range []string{"d1", "d2"} {
		if _, err := svc.db.Exec(ctx,
			`INSERT INTO deployments (id, namespace, name, type, content_cid, deployed_by) VALUES (?, 'acme', ?, 'static', 'QmSame', 'x')`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.registerCIDs(ctx, "acme", "QmSame"); err != nil {
		t.Fatal(err)
	}
	rec := &unpinRecorder{}
	if err := svc.releaseCID(ctx, rec, "d1", "acme", "QmSame"); err != nil {
		t.Fatal(err)
	}
	if len(rec.cids) != 0 || refCount(t, refs, "QmSame") != 1 {
		t.Fatalf("unpinned %v, references %d; d2 still serves it", rec.cids, refCount(t, refs, "QmSame"))
	}
}

func TestReleaseCID_edgeCases(t *testing.T) {
	ctx := context.Background()
	svc, _ := refsService(t)
	rec := &unpinRecorder{}
	if err := svc.releaseCID(ctx, rec, "d1", "acme", ""); err != nil || len(rec.cids) != 0 {
		t.Fatalf("empty cid: %v %v", err, rec.cids)
	}
	bare := registryWith(t)
	if err := bare.releaseCID(ctx, rec, "d1", "acme", "QmX"); err != nil || len(rec.cids) != 0 {
		t.Fatalf("a service with no index: %v %v", err, rec.cids)
	}
}

func TestRegisterCIDs_returnsWhatItRegisteredWithoutDuplicates(t *testing.T) {
	svc, _ := refsService(t)
	registered, err := svc.registerCIDs(context.Background(), "acme", "QmA", "", "QmB", "QmA")
	if err != nil || len(registered) != 2 {
		t.Fatalf("registered = %v, %v; want QmA and QmB once", registered, err)
	}
}

func TestRegisterCIDs_failureRollsBackWhatItRegistered(t *testing.T) {
	svc, refs := refsService(t)
	ctx := context.Background()
	if _, err := svc.db.Exec(ctx, `CREATE TRIGGER refuse_second BEFORE INSERT ON ipfs_cid_refs WHEN NEW.cid = 'QmBad' BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.registerCIDs(ctx, "acme", "QmGood", "QmBad"); err == nil {
		t.Fatal("registration succeeded")
	}
	if n := refCount(t, refs, "QmGood"); n != 0 {
		t.Fatalf("QmGood was left registered (%d) by a registration that failed", n)
	}
}

// Create registers content and build; a create that loses the insert race drops
// the references it added.
func TestCreateDeployment_registersItsCIDs(t *testing.T) {
	svc, refs := refsService(t)
	svc.nodePeerID = "peer-1"
	d := &deployments.Deployment{ID: "d9", Namespace: "acme", Name: "app", Type: deployments.DeploymentTypeStatic,
		Version: 1, Status: deployments.DeploymentStatusActive, Environment: map[string]string{}, DeployedBy: "acme",
		ContentCID: "QmContent", BuildCID: "QmBuild"}
	if err := svc.CreateDeployment(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if refCount(t, refs, "QmContent") != 1 || refCount(t, refs, "QmBuild") != 1 {
		t.Fatal("a created deployment's content and build are not in the index")
	}
}

func TestCreateDeployment_lostRaceDropsItsReferences(t *testing.T) {
	svc, refs := refsService(t, [2]string{"acme", "site"})
	svc.nodePeerID = "peer-1"
	d := &deployments.Deployment{ID: "d2", Namespace: "acme", Name: "site", Type: deployments.DeploymentTypeStatic,
		Version: 1, Status: deployments.DeploymentStatusActive, Environment: map[string]string{}, DeployedBy: "acme",
		ContentCID: "QmLoser"}
	if err := svc.CreateDeployment(context.Background(), d); err == nil {
		t.Fatal("the losing create succeeded")
	}
	if n := refCount(t, refs, "QmLoser"); n != 0 {
		t.Fatalf("a create that did not happen left %d references", n)
	}
}

// A static update registers the new CID and lets go of the old one, unpinning
// it only when nothing else references it.
func TestUpdateHandler_staticUpdateMovesTheReference(t *testing.T) {
	for _, tc := range []struct {
		name      string
		otherHold bool
		wantUnpin int
	}{
		{"last reference", false, 1},
		{"another namespace holds the old content", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, refs := refsService(t)
			svc.baseDomain = "example.test"
			ctx := context.Background()
			if _, err := svc.db.Exec(ctx,
				`INSERT INTO deployments (id, namespace, name, type, version, content_cid, build_cid, home_node_id, port, subdomain, environment, deployed_by)
				 VALUES ('d1', 'acme', 'site', 'static', 1, 'QmOld', '', '', 0, 'site-abc123', '', 'test')`); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.registerCIDs(ctx, "acme", "QmOld"); err != nil {
				t.Fatal(err)
			}
			if tc.otherHold {
				if err := refs.Register(ctx, "QmOld", "other", storage.KindStorage); err != nil {
					t.Fatal(err)
				}
			}
			var sawIndex string
			addedFile := false
			ipfsMock := siteIPFS(t, &sawIndex, &addedFile)
			unpins := 0
			ipfsMock.UnpinFunc = func(context.Context, string) error { unpins++; return nil }
			static := NewStaticDeploymentHandler(svc, ipfsMock, zap.NewNop())
			h := NewUpdateHandler(svc, static, nil, nil, zap.NewNop())

			rr := httptest.NewRecorder()
			h.HandleUpdate(rr, siteUpdateRequest(t, "acme", "site", siteArchive(t, "v2")))
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			if refCount(t, refs, "QmSiteDir") != 1 {
				t.Error("the new content is not in the index")
			}
			if unpins != tc.wantUnpin {
				t.Errorf("unpins = %d, want %d", unpins, tc.wantUnpin)
			}
			wantOld := 0
			if tc.otherHold {
				wantOld = 1
			}
			if got := refCount(t, refs, "QmOld"); got != wantOld {
				t.Errorf("references to the old content = %d, want %d", got, wantOld)
			}
		})
	}
}

// An update whose database write fails must not leave the new CID referenced.
func TestUpdateHandler_staticUpdateFailure_dropsTheNewReference(t *testing.T) {
	svc, refs := refsService(t)
	svc.baseDomain = "example.test"
	ctx := context.Background()
	if _, err := svc.db.Exec(ctx,
		`INSERT INTO deployments (id, namespace, name, type, version, content_cid, build_cid, home_node_id, port, subdomain, environment, deployed_by)
		 VALUES ('d1', 'acme', 'site', 'static', 1, 'QmOld', '', '', 0, 'site-abc123', '', 'test')`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(ctx, `CREATE TRIGGER refuse_update BEFORE UPDATE ON deployments BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	var sawIndex string
	addedFile := false
	static := NewStaticDeploymentHandler(svc, siteIPFS(t, &sawIndex, &addedFile), zap.NewNop())
	h := NewUpdateHandler(svc, static, nil, nil, zap.NewNop())
	rr := httptest.NewRecorder()
	h.HandleUpdate(rr, siteUpdateRequest(t, "acme", "site", siteArchive(t, "v2")))
	if rr.Code == http.StatusOK {
		t.Fatal("the update succeeded")
	}
	if n := refCount(t, refs, "QmSiteDir"); n != 0 {
		t.Fatalf("an update that failed left %d references", n)
	}
}
