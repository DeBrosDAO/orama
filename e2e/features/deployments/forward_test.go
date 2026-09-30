//go:build e2e_fleet

package deployments

import (
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// TestDeployForward_updateThroughANodeWithoutTheApp: an update sent to a
// gateway on a node that does not run the app is carried out on the app's
// nodes, and the new version serves everywhere (docs/DEPLOYMENT_GUIDE.md
// "Cross-Node Routing"; mutating operations go to the home node).
func TestDeployForward_updateThroughANodeWithoutTheApp(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "nodejs", nodeApp(t, "fwd-v1", plainPackage), "fwd")
	serving(t, tn.app(u), "/version", "fwd-v1")
	running := unitNodes(t, tn.f, "orama-deploy-node@"+tn.instance("fwd")+".service")
	var outsider *fleet.Node
	for _, node := range tn.f.State.Nodes {
		if !nodeIn(node, running) {
			outsider = &node
			break
		}
	}
	if outsider == nil {
		t.Fatalf("every node runs the app (%d); DefaultReplicaCount is %d", len(running), replicas)
	}
	req, err := multipartReq(map[string]string{"name": "fwd"}, "app.tar.gz",
		tarball(t, map[string]string{"package.json": plainPackage, "index.js": replaceVersion(nodeServer, "fwd-v2")}))
	if err != nil {
		t.Fatal(err)
	}
	req.Path, req.Bearer = "/v1/deployments/nodejs/update?name=fwd", tn.admin.Bearer
	tn.n.Client.PinTo(outsider.PublicIP).MustSend(t, req).Expect(t, http.StatusOK)
	for _, nc := range tenancy.PerNode(t, tn.f, tn.app(u)) {
		serving(t, nc.Client, "/version", "fwd-v2")
	}
}

func nodeIn(n fleet.Node, list []fleet.Node) bool {
	for _, x := range list {
		if x.Name == n.Name {
			return true
		}
	}
	return false
}
