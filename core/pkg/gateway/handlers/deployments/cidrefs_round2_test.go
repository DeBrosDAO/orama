package deployments

import (
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
)

// A: two same-name static creates with identical content share one index row.
// The loser registered, then lost the insert, and used to delete the winner's
// reference when it took its own back.
func TestCreateDeployment_lostRaceKeepsTheWinnersReference(t *testing.T) {
	svc, refs := refsService(t, [2]string{"acme", "site"})
	svc.nodePeerID = "peer-1"
	ctx := context.Background()
	if _, err := svc.registerCIDs(ctx, "acme", "QmSame"); err != nil { // the winner's
		t.Fatal(err)
	}
	d := &deployments.Deployment{ID: "d2", Namespace: "acme", Name: "site", Type: deployments.DeploymentTypeStatic,
		Version: 1, Status: deployments.DeploymentStatusActive, Environment: map[string]string{}, DeployedBy: "acme",
		ContentCID: "QmSame"}
	if err := svc.CreateDeployment(ctx, d); err == nil {
		t.Fatal("the losing create succeeded")
	}
	if n := refCount(t, refs, "QmSame"); n != 1 {
		t.Fatalf("references = %d, want the winner's 1", n)
	}
}

// D: a create whose registration fails releases the subdomain it registered.
func TestCreateDeployment_registrationFailureReleasesTheSubdomain(t *testing.T) {
	svc, _ := refsService(t)
	svc.nodePeerID = "peer-1"
	ctx := context.Background()
	if _, err := svc.db.Exec(ctx, `CREATE TRIGGER refuse_refs BEFORE INSERT ON ipfs_cid_refs BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	d := &deployments.Deployment{ID: "d7", Namespace: "acme", Name: "app", Type: deployments.DeploymentTypeStatic,
		Version: 1, Status: deployments.DeploymentStatusActive, Environment: map[string]string{}, DeployedBy: "acme",
		ContentCID: "QmContent"}
	if err := svc.CreateDeployment(ctx, d); err == nil {
		t.Fatal("the create succeeded although its content could not be registered")
	}
	var rows []struct {
		N int `db:"n"`
	}
	if err := svc.db.Query(ctx, &rows, `SELECT COUNT(*) AS n FROM global_deployment_subdomains WHERE deployment_id = 'd7'`); err != nil {
		t.Fatal(err)
	}
	if rows[0].N != 0 {
		t.Fatalf("%d subdomain rows leaked", rows[0].N)
	}
	var made []struct {
		N int `db:"n"`
	}
	if err := svc.db.Query(ctx, &made, `SELECT COUNT(*) AS n FROM deployments WHERE id = 'd7'`); err != nil || made[0].N != 0 {
		t.Fatalf("the deployment was created: %v %v", made, err)
	}
}
