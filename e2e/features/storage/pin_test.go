//go:build e2e_fleet

package storage

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// TestPin_statusReachesReplicationFactor: an upload is pinned on the cluster's
// replication factor (3) and status reports it (website/src/docs/contributor/architecture-reference.mdx; RF=3).
func TestPin_statusReachesReplicationFactor(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	u := upload(t, n.Client, tenancy.Owner(n), "rf.bin", randomBytes(t, smallBytes))
	s := waitPinned(t, n.Client, tenancy.Owner(n), u.Cid)
	if s.Cid != u.Cid || s.ReplicationFactor != replicationFactor {
		t.Errorf("status %+v, want cid %s rf %d", s, u.Cid, replicationFactor)
	}
}

// TestPin_explicitPinOwnedOnly: POST /v1/storage/pin pins an owned CID and
// refuses one the namespace does not own (403) or a body without a cid (400).
func TestPin_explicitPinOwnedOnly(t *testing.T) {
	t.Parallel()
	nss := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	a, b := nss[0], nss[1]
	u := upload(t, a.Client, tenancy.Owner(a), "pin.bin", randomBytes(t, smallBytes))
	var pinned struct{ Cid, Name string }
	if err := tenancy.Post(t, a.Client, pathPin, tenancy.Owner(a), map[string]string{"cid": u.Cid, "name": "pin.bin"}).
		Expect(t, http.StatusOK).Decode(&pinned); err != nil {
		t.Fatal(err)
	}
	if pinned.Cid != u.Cid {
		t.Errorf("pin answered %q, want %s", pinned.Cid, u.Cid)
	}
	if r := tenancy.Post(t, b.Client, pathPin, tenancy.Owner(b), map[string]string{"cid": u.Cid}); r.Status != http.StatusForbidden {
		t.Errorf("pinning another namespace's CID: want 403, got %d: %.200s", r.Status, r.Body)
	}
	for name, body := range map[string][]byte{"no cid": []byte(`{}`), "not json": []byte(`cid=x`), "cid wrong type": []byte(`{"cid":1}`)} {
		if r := tenancy.Post(t, a.Client, pathPin, tenancy.Owner(a), body); r.Status != http.StatusBadRequest {
			t.Errorf("pin %s: want 400, got %d", name, r.Status)
		}
	}
}

// TestGet_otherNamespaceRefused: a namespace reads only CIDs it owns
// (docs/whitepaper/technical-reference/vol1/23-webrtc.md#authentication; download_handler.go): 403, and never
// the content.
func TestGet_otherNamespaceRefused(t *testing.T) {
	t.Parallel()
	nss := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	a, b := nss[0], nss[1]
	data := randomBytes(t, smallBytes)
	u := upload(t, a.Client, tenancy.Owner(a), "secret.bin", data)
	waitContent(t, a.Client, tenancy.Owner(a), u.Cid, data)
	r := get(t, b.Client, tenancy.Owner(b), u.Cid)
	if r.Status != http.StatusForbidden || strings.Contains(string(r.Body), string(data[:16])) {
		t.Fatalf("namespace %s read %s's CID: %d", b.Name, a.Name, r.Status)
	}
	tenancy.ExpectDenied(t, get(t, a.Client, tenancy.Owner(b), u.Cid), "another namespace's token on this namespace's gateway")
}

// TestStatus_otherNamespaceCIDNotDisclosed: the pin status of a CID another
// namespace uploaded must not disclose its name or its peers. The status
// handler checks no ownership (status handler in download_handler.go), so
// this is expected to fail until it does.
func TestStatus_otherNamespaceCIDNotDisclosed(t *testing.T) {
	t.Parallel()
	nss := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	a, b := nss[0], nss[1]
	u := upload(t, a.Client, tenancy.Owner(a), "private/salary-2026.xlsx", randomBytes(t, smallBytes))
	waitPinned(t, a.Client, tenancy.Owner(a), u.Cid)
	r, s := status(t, b.Client, tenancy.Owner(b), u.Cid)
	if r.Status == http.StatusOK && (s.Name != "" || len(s.Peers) > 0) {
		t.Errorf("namespace %s read the status of %s's CID: name %q, %d peers", b.Name, a.Name, s.Name, len(s.Peers))
	}
}

// TestUnpin_idempotentAndReclaimed: unpinning twice is 200 both times, the
// second saying already_unpinned (bugboard #140); immediate=true reclaims the
// blob everywhere and a later download says it is not stored (bugboard #153).
func TestUnpin_idempotentAndReclaimed(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	who := tenancy.Owner(n)
	data := randomBytes(t, smallBytes)
	u := upload(t, n.Client, who, "gone.bin", data)
	waitPinned(t, n.Client, who, u.Cid)
	r, body := unpin(t, n.Client, who, u.Cid, true)
	r.Expect(t, http.StatusOK)
	if body["status"] != "ok" || body["evicted"] != "true" {
		t.Errorf("immediate unpin answered %v, want status ok evicted true", body)
	}
	r, body = unpin(t, n.Client, who, u.Cid, false)
	r.Expect(t, http.StatusOK)
	if body["already_unpinned"] != true || body["evicted"] != "skipped" {
		t.Errorf("second unpin answered %v, want already_unpinned and evicted skipped", body)
	}
	eventually.Require(t, pollEvery, pinPropagation, "reclaimed content reported not stored", func() (bool, error) {
		g := get(t, n.Client, who, u.Cid)
		if g.Status == http.StatusOK {
			return false, nil
		}
		return g.Status == http.StatusNotFound && strings.Contains(string(g.Body), "not stored"), nil
	})
}

// TestUnpin_sharedContentKept: a CID two namespaces hold (unwrapped .tar.gz
// content has one CID) stays for the other when one unpins it: "shared".
func TestUnpin_sharedContentKept(t *testing.T) {
	t.Parallel()
	nss := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	a, b := nss[0], nss[1]
	data := randomBytes(t, smallBytes)
	ua := upload(t, a.Client, tenancy.Owner(a), "site.tar.gz", data)
	ub := upload(t, b.Client, tenancy.Owner(b), "site.tar.gz", data)
	if ua.Cid != ub.Cid {
		t.Fatalf("unwrapped tarballs of the same bytes got CIDs %s and %s", ua.Cid, ub.Cid)
	}
	waitPinned(t, a.Client, tenancy.Owner(a), ua.Cid)
	r, body := unpin(t, a.Client, tenancy.Owner(a), ua.Cid, true)
	r.Expect(t, http.StatusOK)
	if body["shared"] != true || body["evicted"] != "shared" {
		t.Errorf("unpinning shared content answered %v, want shared", body)
	}
	waitContent(t, b.Client, tenancy.Owner(b), ub.Cid, data)
}

// TestUnpin_notOwnedAndMalformed: another namespace's CID is 403; an empty
// path is 400; any method but DELETE is not an unpin.
func TestUnpin_notOwnedAndMalformed(t *testing.T) {
	t.Parallel()
	nss := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	a, b := nss[0], nss[1]
	u := upload(t, a.Client, tenancy.Owner(a), "keep.bin", randomBytes(t, smallBytes))
	if r, _ := unpin(t, b.Client, tenancy.Owner(b), u.Cid, true); r.Status != http.StatusForbidden {
		t.Errorf("unpinning another namespace's CID: want 403, got %d", r.Status)
	}
	if r, _ := unpin(t, a.Client, tenancy.Owner(a), "", false); r.Status != http.StatusBadRequest {
		t.Errorf("unpin without a CID: want 400, got %d", r.Status)
	}
	r := a.Client.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathUnpin + u.Cid, Bearer: a.Owner.Token()})
	if r.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST unpin: want 405, got %d", r.Status)
	}
	waitPinned(t, a.Client, tenancy.Owner(a), u.Cid)
}
