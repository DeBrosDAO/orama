package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// downHomeDB is a registry that cannot say where the home node is.
type downHomeDB struct{ client.DatabaseClient }

func (downHomeDB) Query(context.Context, string, ...interface{}) (*client.QueryResult, error) {
	return nil, errors.New("no leader")
}

func homeRouteGateway(t *testing.T) *Gateway {
	t.Helper()
	logger, err := logging.NewColoredLogger(logging.ComponentGateway, false)
	if err != nil {
		t.Fatal(err)
	}
	return &Gateway{
		logger:     logger,
		nodePeerID: "this-node",
		client:     &fakeNetworkClient{db: downHomeDB{}},
	}
}

// The bug: when the home node could not be reached, a change to the deployment
// ran on whichever node took the request, outside the home node's lock and
// version stamps. It must be refused so the caller retries.
func TestRouteToHomeNode_aChangeIsRefusedWhenTheHomeNodeCannotBeReached(t *testing.T) {
	g := homeRouteGateway(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/deployments/rollback?name=api", nil)

	handled := g.routeToHomeNode(w, r, &deployments.Deployment{Name: "api", HomeNodeID: "other-node"}, true)

	if !handled || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("handled %v, status %d; a change must be answered 503, not run here", handled, w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("a retryable refusal should say when to retry")
	}
}

func TestRouteToHomeNode_aReadStillRunsHereWhenTheHomeNodeCannotBeReached(t *testing.T) {
	g := homeRouteGateway(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/deployments/logs?name=api", nil)

	if g.routeToHomeNode(w, r, &deployments.Deployment{Name: "api", HomeNodeID: "other-node"}, false) {
		t.Fatal("a read was refused instead of falling through to the handler")
	}
	if w.Body.Len() != 0 {
		t.Errorf("a fall-through wrote %q", w.Body)
	}
}
