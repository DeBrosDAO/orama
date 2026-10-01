package deployments

import (
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
