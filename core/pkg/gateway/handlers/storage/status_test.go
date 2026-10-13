package storage

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

func statusAs(h *Handlers, ns, cid string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/storage/status/"+cid, nil)
	if ns != "" {
		req = withNamespace(req, ns)
	}
	rec := httptest.NewRecorder()
	h.StatusHandler(rec, req)
	return rec
}

// Bug: the status of a CID another namespace uploaded (its name, its peers) was
// answered to anyone. e2e: TestStatus_otherNamespaceCIDNotDisclosed.
//
// Mutation check: drop the ownership check from StatusHandler and this fails.
func TestStatus_otherNamespaceCID_isAnsweredAsUnknown(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{
		pinStatus: &ipfs.PinStatus{Cid: sharedCID, Name: "private/salary-2026.xlsx", Status: "pinned", Peers: []string{"peer1"}},
	}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	if err := a.recordCIDOwnership(t.Context(), sharedCID, "ns-a", "private/salary-2026.xlsx", "ns-a", 1); err != nil {
		t.Fatal(err)
	}

	owner := statusAs(a, "ns-a", sharedCID)
	if owner.Code != http.StatusOK {
		t.Fatalf("the owner's status = %d, want 200", owner.Code)
	}
	if body := decodeBody(t, owner); body["name"] != "private/salary-2026.xlsx" {
		t.Fatalf("the owner did not get the name: %v", body)
	}

	// Another namespace's gateway, with its own database: no row for the CID.
	b := gatewayFor(t, mock, namespaceSchema(t), registry)
	stranger := statusAs(b, "ns-b", sharedCID)

	// The answer must be the one for a CID nobody pinned.
	unknown := &mockIPFSClient{pinStatErr: errors.New("404 not found")}
	c := gatewayFor(t, unknown, namespaceSchema(t), registry)
	if err := c.recordCIDOwnership(t.Context(), sharedCID, "ns-c", "x", "ns-c", 1); err != nil {
		t.Fatal(err)
	}
	want := statusAs(c, "ns-c", sharedCID)

	if stranger.Code != http.StatusNotFound || stranger.Code != want.Code {
		t.Fatalf("stranger got %d, an unknown CID gets %d; both must be 404", stranger.Code, want.Code)
	}
	if stranger.Body.String() != want.Body.String() {
		t.Errorf("stranger's body %q differs from an unknown CID's %q: the endpoint is an oracle", stranger.Body.String(), want.Body.String())
	}
}

func TestStatus_requiresNamespace(t *testing.T) {
	h := gatewayFor(t, &mockIPFSClient{}, namespaceSchema(t), registrySchema(t))
	if rec := statusAs(h, "", sharedCID); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestStatus_ownershipLookupError_isNotAnAnswer(t *testing.T) {
	h := newHandlersWithDB(&mockIPFSClient{pinStatus: &ipfs.PinStatus{Name: "secret"}}, &mockStorageDB{queryErr: errStorageTest})
	rec := statusAs(h, "ns-a", sharedCID)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("a failed ownership check leaked status: %s", rec.Body.String())
	}
}
