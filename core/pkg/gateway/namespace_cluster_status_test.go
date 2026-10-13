package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/namespace"
)

type statusProvisioner struct {
	status interface{}
	err    error
}

func (statusProvisioner) CheckNamespaceCluster(context.Context, string) (string, string, bool, error) {
	return "", "", false, nil
}

func (statusProvisioner) ProvisionNamespaceCluster(context.Context, int, string, string) (string, string, error) {
	return "", "", nil
}

func (p statusProvisioner) GetClusterStatusByID(context.Context, string) (interface{}, error) {
	return p.status, p.err
}

func clusterStatusRequest(t *testing.T, p statusProvisioner) *httptest.ResponseRecorder {
	t.Helper()
	log, err := logging.NewColoredLogger(logging.ComponentGeneral, false)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{logger: log, clusterProvisioner: p}
	w := httptest.NewRecorder()
	g.namespaceClusterStatusHandler(w, httptest.NewRequest(http.MethodGet, "/v1/namespace/status?id=c1", nil))
	return w
}

func TestNamespaceClusterStatusHandler_missingClusterIs404(t *testing.T) {
	w := clusterStatusRequest(t, statusProvisioner{err: namespace.ErrClusterNotFound})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}

func TestNamespaceClusterStatusHandler_registryFailureIs503WithoutInternals(t *testing.T) {
	cause := errors.New("rqlite: dial tcp 10.0.0.9:4001: connection refused")
	w := clusterStatusRequest(t, statusProvisioner{err: fmt.Errorf("failed to read the nodes of namespace cluster c1: %w", cause)})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	for _, leak := range []string{"rqlite", "10.0.0.9", "4001", "c1"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("the response gives the client %q: %s", leak, w.Body.String())
		}
	}
	if !strings.Contains(w.Body.String(), "retry") {
		t.Errorf("the 503 says nothing the client can act on: %s", w.Body.String())
	}
}

func TestNamespaceClusterStatusHandler_existingClusterIs200(t *testing.T) {
	w := clusterStatusRequest(t, statusProvisioner{status: map[string]string{"cluster_id": "c1"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "c1") {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
}

func TestClusterStatusFailure_wrappedNotFoundStaysNotFound(t *testing.T) {
	if code, _ := clusterStatusFailure(fmt.Errorf("x: %w", namespace.ErrClusterNotFound)); code != http.StatusNotFound {
		t.Errorf("code = %d", code)
	}
	if code, _ := clusterStatusFailure(errors.New("boom")); code != http.StatusServiceUnavailable {
		t.Errorf("code = %d", code)
	}
}
