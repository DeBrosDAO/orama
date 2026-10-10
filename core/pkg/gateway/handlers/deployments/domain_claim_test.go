package deployments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A pending row proves nothing, so it must not hold a name against the
// domain's real owner. Only a verified row, or this namespace's own live
// pending row, blocks an add.

func addDomain(h *DomainHandler, domain string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.HandleAddDomain(w, domainRequest(http.MethodPost, "/v1/deployments/domains/add",
		`{"deployment_name":"web","domain":"`+domain+`"}`))
	return w
}

func domainOwner(t *testing.T, exec func(string, ...any), h *DomainHandler, domain string) (ns string, verified bool) {
	t.Helper()
	var rows []struct {
		Namespace  string  `db:"namespace"`
		VerifiedAt *string `db:"verified_at"`
	}
	if err := h.service.db.Query(t.Context(), &rows, `SELECT namespace, verified_at FROM deployment_domains WHERE domain = ?`, domain); err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows for %s = %d, want exactly 1", domain, len(rows))
	}
	return rows[0].Namespace, rows[0].VerifiedAt != nil
}

func TestHandleAddDomain_supersedesAnotherNamespacesPendingRow(t *testing.T) {
	h, exec := newDomainHandlerOnSchema(t)
	exec(`INSERT INTO deployment_domains (id, deployment_id, namespace, domain, is_custom, verification_token) VALUES ('x', 'd2', 'ns-b', 'victim.example.org', TRUE, 'squat')`)

	if w := addDomain(h, "victim.example.org"); w.Code != http.StatusCreated {
		t.Fatalf("add over a squatter's pending row: %d %q, want 201", w.Code, w.Body.String())
	}
	if ns, verified := domainOwner(t, exec, h, "victim.example.org"); ns != testDomainNamespace || verified {
		t.Errorf("row = ns %q verified %v, want %q pending", ns, verified, testDomainNamespace)
	}
}

func TestHandleAddDomain_verifiedRowOfAnotherNamespaceStill409s(t *testing.T) {
	h, exec := newDomainHandlerOnSchema(t)
	exec(`INSERT INTO deployment_domains (id, deployment_id, namespace, domain, is_custom, verified_at) VALUES ('x', 'd2', 'ns-b', 'taken.example.org', TRUE, CURRENT_TIMESTAMP)`)

	w := addDomain(h, "taken.example.org")
	if w.Code != http.StatusConflict {
		t.Fatalf("add of a verified domain: %d, want 409", w.Code)
	}
	if strings.Contains(w.Body.String(), "ns-b") {
		t.Errorf("the refusal names the other namespace: %q", w.Body.String())
	}
	if ns, verified := domainOwner(t, exec, h, "taken.example.org"); ns != "ns-b" || !verified {
		t.Errorf("verified row changed: ns %q verified %v", ns, verified)
	}
}

func TestHandleAddDomain_sameNamespaceDuplicateStill409s(t *testing.T) {
	h, exec := newDomainHandlerOnSchema(t)
	if w := addDomain(h, "mine.example.org"); w.Code != http.StatusCreated {
		t.Fatalf("first add: %d %q", w.Code, w.Body.String())
	}
	if w := addDomain(h, "mine.example.org"); w.Code != http.StatusConflict {
		t.Fatalf("second add: %d, want 409", w.Code)
	}
	exec(`UPDATE deployment_domains SET verified_at = CURRENT_TIMESTAMP`)
	if w := addDomain(h, "mine.example.org"); w.Code != http.StatusConflict {
		t.Fatalf("add of own verified domain: %d, want 409", w.Code)
	}
}

func TestHandleAddDomain_expiredPendingRowIsSuperseded(t *testing.T) {
	h, exec := newDomainHandlerOnSchema(t)
	old := time.Now().Add(-pendingDomainTTL - time.Hour).UTC().Format("2006-01-02 15:04:05")
	// Both a squatter's row and this namespace's own stale row expire.
	exec(`INSERT INTO deployment_domains (id, deployment_id, namespace, domain, is_custom, created_at) VALUES ('x', 'd2', 'ns-b', 'stale-b.example.org', TRUE, ?)`, old)
	exec(`INSERT INTO deployment_domains (id, deployment_id, namespace, domain, is_custom, created_at) VALUES ('y', 'd1', 'ns-a', 'stale-a.example.org', TRUE, ?)`, old)

	for _, d := range []string{"stale-b.example.org", "stale-a.example.org"} {
		if w := addDomain(h, d); w.Code != http.StatusCreated {
			t.Fatalf("add over expired row %s: %d %q, want 201", d, w.Code, w.Body.String())
		}
		if ns, _ := domainOwner(t, exec, h, d); ns != testDomainNamespace {
			t.Errorf("%s owner = %q, want %q", d, ns, testDomainNamespace)
		}
	}
}

func TestHandleAddDomain_freshPendingRowIsNotExpired(t *testing.T) {
	h, exec := newDomainHandlerOnSchema(t)
	recent := time.Now().Add(-pendingDomainTTL + time.Hour).UTC().Format("2006-01-02 15:04:05")
	exec(`INSERT INTO deployment_domains (id, deployment_id, namespace, domain, is_custom, created_at) VALUES ('y', 'd1', 'ns-a', 'fresh.example.org', TRUE, ?)`, recent)
	if w := addDomain(h, "fresh.example.org"); w.Code != http.StatusConflict {
		t.Fatalf("add over own unexpired pending row: %d, want 409", w.Code)
	}
}

// TestHandleVerifyDomain_aClaimSupersededDuringTheLookupIsNotVerified: the
// TXT lookup can take seconds, and another namespace's add can replace the
// claim meanwhile; the old claim answered "verified" and got a DNS record.
func TestHandleVerifyDomain_aClaimSupersededDuringTheLookupIsNotVerified(t *testing.T) {
	h, exec := newDomainHandlerOnSchema(t)
	exec(`INSERT INTO deployment_domains (id, deployment_id, namespace, domain, is_custom, verification_token) VALUES ('x', 'd1', 'ns-a', 'race.example.org', TRUE, 'tok-a')`)
	h.lookupTXT = func(context.Context, string) ([]string, error) {
		exec(`UPDATE deployment_domains SET deployment_id = 'd2', namespace = 'ns-b', verification_token = 'tok-b' WHERE domain = 'race.example.org'`)
		return []string{"tok-a"}, nil
	}
	w := httptest.NewRecorder()
	h.HandleVerifyDomain(w, domainRequest(http.MethodPost, "/v1/deployments/domains/verify", `{"domain":"race.example.org"}`))
	if w.Code != http.StatusConflict {
		t.Fatalf("verify of a superseded claim: status %d %q, want 409", w.Code, w.Body.String())
	}
	var verified int
	rows := []struct {
		N int `db:"n"`
	}{}
	if err := h.service.db.Query(t.Context(), &rows, `SELECT COUNT(*) AS n FROM deployment_domains WHERE domain = 'race.example.org' AND verified_at IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 1 {
		verified = rows[0].N
	}
	if verified != 0 {
		t.Error("the superseding claim was marked verified by the old claim's token")
	}
}

// TestHandleAddDomain_aSupersedeClearsTheOldClaimsRouting: routing hints and a
// TLS certificate of the claim it replaces must not carry over to the new one.
func TestHandleAddDomain_aSupersedeClearsTheOldClaimsRouting(t *testing.T) {
	h, exec := newDomainHandlerOnSchema(t)
	exec(`INSERT INTO deployment_domains (id, deployment_id, namespace, domain, is_custom, verification_token, routing_type, node_id, tls_cert_cid) VALUES ('x', 'd2', 'ns-b', 'carry.example.org', TRUE, 'squat', 'node_specific', 'n9', 'QmOld')`)
	w := httptest.NewRecorder()
	h.HandleAddDomain(w, domainRequest(http.MethodPost, "/v1/deployments/domains/add", `{"deployment_name":"web","domain":"carry.example.org"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("add over another namespace's pending claim: %d %q", w.Code, w.Body.String())
	}
	var rows []struct {
		Routing string  `db:"routing_type"`
		Node    *string `db:"node_id"`
		Cert    *string `db:"tls_cert_cid"`
	}
	if err := h.service.db.Query(t.Context(), &rows, `SELECT routing_type, node_id, tls_cert_cid FROM deployment_domains WHERE domain = 'carry.example.org'`); err != nil || len(rows) != 1 {
		t.Fatalf("read the claim: %v %v", rows, err)
	}
	if rows[0].Routing != "balanced" || rows[0].Node != nil || rows[0].Cert != nil {
		t.Errorf("the old claim's routing carried over: %+v", rows[0])
	}
}
