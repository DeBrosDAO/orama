package deployments

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"go.uber.org/zap"
)

const (
	replicaTestNodeID = "12D3KooWReplicaTestNode"
	replicaTestSecret = "a cluster secret for the replica coordination tests"
)

func replicaTestService() *DeploymentService {
	return &DeploymentService{
		db:                 &mockRQLiteClient{},
		logger:             zap.NewNop(),
		nodePeerID:         replicaTestNodeID,
		coordinationSecret: replicaTestSecret,
	}
}

var replicaRoutes = []string{"setup", "update", "rollback", "teardown"}

func replicaHandlers(h *ReplicaHandler) map[string]func(http.ResponseWriter, *http.Request) {
	return map[string]func(http.ResponseWriter, *http.Request){
		"setup": h.HandleSetup, "update": h.HandleUpdate, "rollback": h.HandleRollback, "teardown": h.HandleTeardown,
	}
}

// The body is a tear-down-style request with an invalid name: an authorised call
// gets past authentication and is refused as a bad request (400), so 403 means
// authentication refused it and nothing else can be mistaken for it.
const replicaProbeBody = `{"deployment_id":"d1","namespace":"acme","name":"web/../../x"}`

func replicaRequest(t *testing.T, svc *DeploymentService, route, audience string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/deployments/replica/"+route, strings.NewReader(replicaProbeBody))
	req.RemoteAddr = "10.0.0.2:4000"
	if audience != "" {
		if err := svc.signReplicaRequest(req, audience); err != nil {
			t.Fatal(err)
		}
	}
	return req
}

// The constant `X-Orama-Internal-Auth: replica-coordination` plus an overlay
// source address was the whole credential. Any tenant workload with an overlay
// source could tear down or replace any namespace's replica.
//
// Mutation check: accept the constant again and this fails.
func TestReplicaHandler_theConstantHeaderAloneIsRefused(t *testing.T) {
	base := t.TempDir()
	h := NewReplicaHandler(replicaTestService(), nil, nil, zap.NewNop(), base)
	for route, handle := range replicaHandlers(h) {
		req := replicaRequest(t, replicaTestService(), route, "")
		req.Header.Set("X-Orama-Internal-Auth", "replica-coordination")
		rr := httptest.NewRecorder()
		handle(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403 for the constant header alone", route, rr.Code)
		}
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Errorf("a refused replica request wrote %v", entries)
	}
}

func TestReplicaHandler_aStampForThisNodeIsAccepted(t *testing.T) {
	svc := replicaTestService()
	h := NewReplicaHandler(svc, nil, nil, zap.NewNop(), t.TempDir())
	for route, handle := range replicaHandlers(h) {
		rr := httptest.NewRecorder()
		handle(rr, replicaRequest(t, svc, route, replicaTestNodeID))
		if rr.Code == http.StatusForbidden {
			t.Errorf("%s: a stamp signed for this node was refused", route)
		}
	}
}

func TestReplicaHandler_aStampForAnotherNodeIsRefused(t *testing.T) {
	svc := replicaTestService()
	h := NewReplicaHandler(svc, nil, nil, zap.NewNop(), t.TempDir())
	for route, handle := range replicaHandlers(h) {
		rr := httptest.NewRecorder()
		handle(rr, replicaRequest(t, svc, route, "12D3KooWSomeOtherNode"))
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403 for a stamp signed for another node", route, rr.Code)
		}
	}
}

func TestReplicaHandler_refusesFromOffTheMeshAndWithAnotherClusterSecret(t *testing.T) {
	svc := replicaTestService()
	h := NewReplicaHandler(svc, nil, nil, zap.NewNop(), t.TempDir())

	req := replicaRequest(t, svc, "teardown", replicaTestNodeID)
	req.RemoteAddr = "203.0.113.9:4000"
	rr := httptest.NewRecorder()
	h.HandleTeardown(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("off-mesh: status %d, want 403", rr.Code)
	}

	other := replicaTestService()
	other.coordinationSecret = "another cluster entirely"
	rr = httptest.NewRecorder()
	h.HandleTeardown(rr, replicaRequest(t, other, "teardown", replicaTestNodeID))
	if rr.Code != http.StatusForbidden {
		t.Errorf("wrong secret: status %d, want 403", rr.Code)
	}

	none := replicaTestService()
	none.coordinationSecret = ""
	if err := none.signReplicaRequest(httptest.NewRequest(http.MethodPost, "/x", nil), replicaTestNodeID); err == nil {
		t.Error("signed with no cluster secret")
	}
}

func TestReplicaHandler_aNodeWithoutAnIdRefusesEverything(t *testing.T) {
	svc := replicaTestService()
	req := replicaRequest(t, svc, "teardown", replicaTestNodeID)
	svc.nodePeerID = ""
	rr := httptest.NewRecorder()
	NewReplicaHandler(svc, nil, nil, zap.NewNop(), t.TempDir()).HandleTeardown(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403 from a node that does not know its own id", rr.Code)
	}
}
