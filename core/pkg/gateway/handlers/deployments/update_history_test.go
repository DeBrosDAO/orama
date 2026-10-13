package deployments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"go.uber.org/zap"
)

type historyRow struct {
	Version    int    `db:"version"`
	ContentCID string `db:"content_cid"`
}

func historyOf(t *testing.T, svc *DeploymentService, id string) []historyRow {
	t.Helper()
	var rows []historyRow
	if err := svc.db.Query(context.Background(), &rows,
		`SELECT version, content_cid FROM deployment_history WHERE deployment_id = ? ORDER BY version`, id); err != nil {
		t.Fatal(err)
	}
	return rows
}

// Bug: an update recorded the deployment as it was before the update, so
// version 1 was listed twice and version 2 never.
func TestUpdateHandler_staticUpdateRecordsTheNewVersion(t *testing.T) {
	svc, _ := refsService(t)
	svc.baseDomain = "example.test"
	svc.nodePeerID = "peer-1"
	d := &deployments.Deployment{ID: "d1", Namespace: "acme", Name: "site", Type: deployments.DeploymentTypeStatic,
		Version: 1, Status: deployments.DeploymentStatusActive, Environment: map[string]string{}, DeployedBy: "acme",
		ContentCID: "QmOld", Subdomain: "site-abc123"}
	if err := svc.CreateDeployment(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	var sawIndex string
	addedFile := false
	static := NewStaticDeploymentHandler(svc, siteIPFS(t, &sawIndex, &addedFile), zap.NewNop())
	h := NewUpdateHandler(svc, static, nil, nil, zap.NewNop())

	rr := httptest.NewRecorder()
	h.HandleUpdate(rr, siteUpdateRequest(t, "acme", "site", siteArchive(t, "v2")))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}

	got := historyOf(t, svc, "d1")
	if len(got) != 2 || got[0].Version != 1 || got[0].ContentCID != "QmOld" || got[1].Version != 2 || got[1].ContentCID != "QmSiteDir" {
		t.Fatalf("history after one update = %+v, want version 1 (QmOld) then version 2 (QmSiteDir)", got)
	}
}
