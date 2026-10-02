package deployments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"go.uber.org/zap"
)

const (
	envTestHome    = "12D3KooWHomeNode"
	envTestNodeB   = "12D3KooWNodeB"
	envTestNodeC   = "12D3KooWNodeC"
	envTestSecret  = "VERY-SECRET-VALUE"
	envTestDeploy  = "a" // the id registryWith gives its first row
	envTestOther   = "b" // ... and its second
	envTestAppName = "api"
	envTestOtherNm = "other"
)

type fakeReconfigurer struct {
	mu    sync.Mutex
	calls []*deployments.Deployment
	err   error
}

func (f *fakeReconfigurer) Reconfigure(_ context.Context, d *deployments.Deployment, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, d)
	return f.err
}

func (f *fakeReconfigurer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type replicaEnvCalls struct {
	mu       sync.Mutex
	nodes    []string
	paths    []string
	payloads []map[string]interface{}
}

// replicaEnvService is a registry holding the deployment "api" (and an
// unrelated "other") replicated to the given nodes, with a stand-in for the
// peer call that records what it was sent.
func replicaEnvService(t *testing.T, failing map[string]error, replicaNodes ...string) (*DeploymentService, *replicaEnvCalls) {
	t.Helper()
	svc := registryWith(t, [2]string{"acme", envTestAppName}, [2]string{"acme", envTestOtherNm})
	svc.nodePeerID = envTestHome
	svc.coordinationSecret = replicaTestSecret
	svc.replicaManager = deployments.NewReplicaManager(svc.db, nil, nil, zap.NewNop())
	ctx := context.Background()
	for i, node := range append([]string{envTestHome}, replicaNodes...) {
		if _, err := svc.db.Exec(ctx, `INSERT INTO dns_nodes (id, ip_address, internal_ip) VALUES (?, ?, ?)`,
			node, "203.0.113.1", "10.0.0."+string(rune('1'+i))); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(ctx,
			`INSERT INTO deployment_replicas (deployment_id, node_id, port, status) VALUES (?, ?, ?, 'active')`,
			envTestDeploy, node, 10000+i); err != nil {
			t.Fatal(err)
		}
	}
	calls := &replicaEnvCalls{}
	svc.callReplica = func(ctx context.Context, nodeID, _ string, path string, payload map[string]interface{}) (map[string]interface{}, error) {
		calls.mu.Lock()
		calls.nodes = append(calls.nodes, nodeID)
		calls.paths = append(calls.paths, path)
		calls.payloads = append(calls.payloads, payload)
		calls.mu.Unlock()
		if err := failing[nodeID]; err != nil {
			return nil, err
		}
		return map[string]interface{}{"status": "ok"}, nil
	}
	return svc, calls
}

func envTestDeployment() *deployments.Deployment {
	return &deployments.Deployment{
		ID: envTestDeploy, Namespace: "acme", Name: envTestAppName, Version: 7,
		Type: deployments.DeploymentTypeNodeJSBackend, Environment: map[string]string{"APP_VERSION": "api-v2", "KEY": envTestSecret},
	}
}

// The bug: `app env set` rewrote the env file on the home node only, so a
// replica kept serving the old value indefinitely.
func TestReconfigureReplicas_everyOtherNodeIsToldAndTheHomeNodeIsSkipped(t *testing.T) {
	svc, calls := replicaEnvService(t, nil, envTestNodeB, envTestNodeC)

	applied, err := svc.ReconfigureReplicas(context.Background(), envTestDeployment(), 1)
	if err != nil {
		t.Fatalf("ReconfigureReplicas: %v", err)
	}
	if applied != 2 || len(calls.nodes) != 2 {
		t.Fatalf("applied %d, called %v, want both replicas and not the home node", applied, calls.nodes)
	}
	for i, n := range calls.nodes {
		if n == envTestHome {
			t.Error("the home node was called as its own replica")
		}
		if calls.paths[i] != replicaEnvPath {
			t.Errorf("called %s, want %s", calls.paths[i], replicaEnvPath)
		}
	}
}

// The environment is where secrets live: it goes sealed, never in the clear.
func TestReconfigureReplicas_theEnvironmentTravelsSealedWithItsVersion(t *testing.T) {
	svc, calls := replicaEnvService(t, nil, envTestNodeB)

	if _, err := svc.ReconfigureReplicas(context.Background(), envTestDeployment(), 42); err != nil {
		t.Fatal(err)
	}
	sealed, _ := calls.payloads[0]["environment"].(string)
	if sealed == "" || strings.Contains(sealed, envTestSecret) {
		t.Fatalf("environment on the wire = %q, want a sealed value", sealed)
	}
	if calls.payloads[0]["env_version"] != int64(42) {
		t.Errorf("env_version = %v, want 42", calls.payloads[0]["env_version"])
	}
	got, err := svc.decodeEnvironment("acme", envTestAppName, sealed)
	if err != nil || got["APP_VERSION"] != "api-v2" {
		t.Fatalf("decoded %v, %v; want APP_VERSION=api-v2", got, err)
	}
}

func TestReconfigureReplicas_aFailedReplicaIsNamedCountedAndRecordedAndTheOthersStillApply(t *testing.T) {
	svc, calls := replicaEnvService(t, map[string]error{envTestNodeB: errors.New("node returned status 500: boom")}, envTestNodeB, envTestNodeC)

	applied, err := svc.ReconfigureReplicas(context.Background(), envTestDeployment(), 1)

	var failure *replicaFanOutError
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v, want a *replicaFanOutError", err)
	}
	if !strings.Contains(err.Error(), envTestNodeB) || strings.Contains(err.Error(), "node "+envTestNodeC) {
		t.Errorf("error %q must name the failed node and only it", err)
	}
	if !strings.Contains(err.Error(), "reached 1 of 2 replicas") {
		t.Errorf("error %q must say how many replicas applied it", err)
	}
	if applied != 1 || len(calls.nodes) != 2 {
		t.Errorf("applied %d, called %v; the healthy replica must still be updated", applied, calls.nodes)
	}
	events := deploymentEvents(t, svc, replicaEnvFailedEvent)
	if len(events) != 1 || !strings.Contains(events[0], envTestNodeB) {
		t.Errorf("events %v, want one naming %s", events, envTestNodeB)
	}
}

func deploymentEvents(t *testing.T, svc *DeploymentService, eventType string) []string {
	t.Helper()
	var rows []struct {
		Message string `db:"message"`
	}
	if err := svc.db.Query(context.Background(), &rows,
		`SELECT message FROM deployment_events WHERE deployment_id = ? AND event_type = ?`, envTestDeploy, eventType); err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Message
	}
	return out
}

// A peer's own text and the addresses in a transport error name the cluster's
// internals; the tenant gets the node and a generic reason, in the response and
// in the events it can read.
func TestReconfigureReplicas_aTenantNeverSeesAPeersTextOrOverlayAddress(t *testing.T) {
	secretDetail := "Post http://10.0.0.7:6001/v1/internal/deployments/replica/env: dial tcp 10.0.0.7:6001: connect: refused /opt/orama/secret/path"
	svc, _ := replicaEnvService(t, map[string]error{
		envTestNodeB: fmt.Errorf("request to node %s failed: %s", envTestNodeB, secretDetail),
		envTestNodeC: &replicaStatusError{nodeID: envTestNodeC, status: 404, text: "no such route on 10.0.0.8:6001"},
	}, envTestNodeB, envTestNodeC)

	_, err := svc.ReconfigureReplicas(context.Background(), envTestDeployment(), 1)
	if err == nil {
		t.Fatal("expected a failure")
	}
	texts := append(deploymentEvents(t, svc, replicaEnvFailedEvent), err.Error())
	for _, text := range texts {
		for _, leak := range []string{"10.0.0.", "6001", "/opt/orama", "dial tcp", "no such route"} {
			if strings.Contains(text, leak) {
				t.Errorf("%q leaked into %q", leak, text)
			}
		}
		for _, node := range []string{envTestNodeB, envTestNodeC} {
			if !strings.Contains(text, node) {
				t.Errorf("%q does not name %s", text, node)
			}
		}
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Errorf("a refusal should say its status: %q", err)
	}
}

func TestPublicReplicaReason(t *testing.T) {
	cases := map[string]error{
		"refused the change (status 500)": &replicaStatusError{status: 500, text: "boom"},
		"did not answer in time":          fmt.Errorf("x: %w", context.DeadlineExceeded),
		"could not be reached":            errors.New("dial tcp 10.0.0.1:1: refused"),
	}
	for want, err := range cases {
		if got := publicReplicaReason(err); got != want {
			t.Errorf("publicReplicaReason(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestReconfigureReplicas_aReplicaWithNoOverlayAddressFailsLoudly(t *testing.T) {
	svc, _ := replicaEnvService(t, nil, envTestNodeB)
	if _, err := svc.db.Exec(context.Background(), `DELETE FROM dns_nodes WHERE id = ?`, envTestNodeB); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReconfigureReplicas(context.Background(), envTestDeployment(), 1); err == nil {
		t.Fatal("a replica that cannot be addressed was reported as updated")
	}
}

func TestReconfigureReplicas_noReplicasIsNotAnError(t *testing.T) {
	svc, calls := replicaEnvService(t, nil)
	applied, err := svc.ReconfigureReplicas(context.Background(), envTestDeployment(), 1)
	if err != nil || applied != 0 || len(calls.nodes) != 0 {
		t.Fatalf("applied %d, err %v, calls %v", applied, err, calls.nodes)
	}
}

// deploy --update used to fan out in goroutines and only log: the CLI got a 200
// while a replica kept serving the old version.
func TestUpdateReplicas_aFailedReplicaSurfacesAndCarriesTheVersion(t *testing.T) {
	svc, calls := replicaEnvService(t, map[string]error{envTestNodeB: &replicaStatusError{nodeID: envTestNodeB, status: 500, text: "Health check failed after update"}}, envTestNodeB, envTestNodeC)

	applied, err := svc.UpdateReplicas(context.Background(), envTestDeployment(), replicaUpdatePath)

	if err == nil || !strings.Contains(err.Error(), envTestNodeB) || !strings.Contains(err.Error(), "refused the change (status 500)") ||
		strings.Contains(err.Error(), "Health check failed") {
		t.Fatalf("err = %v, want the failed node and a generic reason, not the peer's text", err)
	}
	if applied != 1 || len(calls.nodes) != 2 {
		t.Errorf("applied %d, called %v", applied, calls.nodes)
	}
	for _, p := range calls.payloads {
		if p["new_version"] != 7 {
			t.Errorf("new_version = %v, want 7", p["new_version"])
		}
	}
	if events := deploymentEvents(t, svc, replicaUpdateFailedEvent); len(events) != 1 {
		t.Errorf("update failure events %v, want one", events)
	}
}

func TestUpdateReplicas_succeedsWhenEveryReplicaApplied(t *testing.T) {
	svc, _ := replicaEnvService(t, nil, envTestNodeB)
	if applied, err := svc.UpdateReplicas(context.Background(), envTestDeployment(), replicaRollbackPath); err != nil || applied != 1 {
		t.Fatalf("applied %d, err %v", applied, err)
	}
}

func setEnvRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/deployments/env/set?name="+envTestAppName, strings.NewReader(body))
	return r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "acme"))
}

// The whole path: the user is told success only when the replica applied it.
func TestHandleSetEnv_reportsSuccessOnlyWhenEveryReplicaApplied(t *testing.T) {
	svc, calls := replicaEnvService(t, nil, envTestNodeB)
	local := &fakeReconfigurer{}
	h := NewEnvHandler(svc, local, zap.NewNop(), t.TempDir())

	w := httptest.NewRecorder()
	h.HandleSetEnv(w, setEnvRequest(`{"set":{"APP_VERSION":"api-v2"}}`))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Restarted bool `json:"restarted"`
		Replicas  int  `json:"replicas"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || !resp.Restarted || resp.Replicas != 1 {
		t.Fatalf("response %s (%v), want restarted with 1 replica", w.Body, err)
	}
	if local.count() != 1 || len(calls.nodes) != 1 {
		t.Errorf("home reconfigured %d times, replicas called %d times, want 1 and 1", local.count(), len(calls.nodes))
	}
}

func TestHandleSetEnv_aReplicaThatFailedIsAnErrorWithACountAndARetryPath(t *testing.T) {
	svc, _ := replicaEnvService(t, map[string]error{envTestNodeB: errors.New("request to node failed: connection refused")}, envTestNodeB, envTestNodeC)
	h := NewEnvHandler(svc, &fakeReconfigurer{}, zap.NewNop(), t.TempDir())

	w := httptest.NewRecorder()
	h.HandleSetEnv(w, setEnvRequest(`{"set":{"APP_VERSION":"api-v2"}}`))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502 when a replica did not apply the change: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{envTestNodeB, "reached 1 of 2 replicas", "run the same command again"} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q must contain %q", body, want)
		}
	}
	got, err := svc.GetDeployment(context.Background(), "acme", envTestAppName)
	if err != nil || got.Environment["APP_VERSION"] != "api-v2" {
		t.Errorf("stored environment %v, %v; the change must be saved so the retry is the same command", got, err)
	}
}

// A replica that hangs must produce the 502, inside the handler's budget, not a
// client that gave up waiting.
func TestHandleSetEnv_aSlowReplicaIsAnAnsweredErrorNotATimeout(t *testing.T) {
	old := replicaEnvCallTimeout
	replicaEnvCallTimeout = 100 * time.Millisecond
	t.Cleanup(func() { replicaEnvCallTimeout = old })

	svc, _ := replicaEnvService(t, nil, envTestNodeB)
	svc.callReplica = func(ctx context.Context, _, _, _ string, _ map[string]interface{}) (map[string]interface{}, error) {
		<-ctx.Done() // never answers; only the per-call timeout ends it
		return nil, ctx.Err()
	}
	h := NewEnvHandler(svc, &fakeReconfigurer{}, zap.NewNop(), t.TempDir())

	start := time.Now()
	w := httptest.NewRecorder()
	h.HandleSetEnv(w, setEnvRequest(`{"set":{"A":"1"}}`))

	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), envTestNodeB) {
		t.Fatalf("status %d body %q, want a 502 naming %s", w.Code, w.Body, envTestNodeB)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %v for a 100ms call budget", took)
	}
}

func TestHandleSetEnv_unsetReachesTheReplicasToo(t *testing.T) {
	svc, calls := replicaEnvService(t, nil, envTestNodeB)
	h := NewEnvHandler(svc, &fakeReconfigurer{}, zap.NewNop(), t.TempDir())

	w := httptest.NewRecorder()
	h.HandleSetEnv(w, setEnvRequest(`{"unset":["OLD"]}`))

	if w.Code != http.StatusOK || len(calls.nodes) != 1 {
		t.Fatalf("status %d, replica calls %v: %s", w.Code, calls.nodes, w.Body)
	}
}

func TestHandleSetEnv_aStaticSiteHasNoReplicaProcessToTell(t *testing.T) {
	svc, calls := replicaEnvService(t, nil, envTestNodeB)
	if _, err := svc.db.Exec(context.Background(), `UPDATE deployments SET type = 'static' WHERE id = ?`, envTestDeploy); err != nil {
		t.Fatal(err)
	}
	h := NewEnvHandler(svc, &fakeReconfigurer{}, zap.NewNop(), t.TempDir())

	w := httptest.NewRecorder()
	h.HandleSetEnv(w, setEnvRequest(`{"set":{"A":"1"}}`))

	if w.Code != http.StatusOK || len(calls.nodes) != 0 {
		t.Fatalf("status %d, replica calls %v", w.Code, calls.nodes)
	}
}

// Two changes made at once read the same row; the second used to overwrite the
// first. Under the lock each is applied to what the other left.
func TestHandleSetEnv_concurrentChangesDoNotLoseEachOther(t *testing.T) {
	svc, _ := replicaEnvService(t, nil, envTestNodeB)
	h := NewEnvHandler(svc, &fakeReconfigurer{}, zap.NewNop(), t.TempDir())

	var wg sync.WaitGroup
	for _, key := range []string{"ONE", "TWO", "THREE", "FOUR"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			w := httptest.NewRecorder()
			h.HandleSetEnv(w, setEnvRequest(`{"set":{"`+key+`":"x"}}`))
			if w.Code != http.StatusOK {
				t.Errorf("%s: status %d: %s", key, w.Code, w.Body)
			}
		}(key)
	}
	wg.Wait()

	got, err := svc.GetDeployment(context.Background(), "acme", envTestAppName)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ONE", "TWO", "THREE", "FOUR"} {
		if got.Environment[key] != "x" {
			t.Errorf("%s was lost: %v", key, got.Environment)
		}
	}
}

// Whatever got past the lock (another gateway) is stopped by the compare-and-swap.
func TestPersistEnv_refusesToOverwriteARowThatMoved(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	h := &EnvHandler{service: svc, logger: zap.NewNop()}
	d := envTestDeployment()

	_, stored, err := svc.getDeploymentSealed(context.Background(), "acme", envTestAppName)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.persistEnv(context.Background(), d, map[string]string{"A": "1"}, stored); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// A second writer still holding the old token is refused.
	if err := h.persistEnv(context.Background(), d, map[string]string{"B": "2"}, stored); !errors.Is(err, errEnvChangedConcurrently) {
		t.Fatalf("stale write: err = %v, want errEnvChangedConcurrently", err)
	}
}

func TestLockDeployment_serializesAndGivesUpWhenItsContextEnds(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	unlock, err := svc.lockDeployment(context.Background(), "acme", "api")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := svc.lockDeployment(ctx, "acme", "api"); err == nil {
		t.Fatal("a held lock was taken twice")
	}
	if other, err := svc.lockDeployment(context.Background(), "acme", "other"); err != nil {
		t.Fatalf("another deployment's lock was blocked: %v", err)
	} else {
		other()
	}
	unlock()
	again, err := svc.lockDeployment(context.Background(), "acme", "api")
	if err != nil {
		t.Fatalf("a released lock could not be taken: %v", err)
	}
	again()
}

func TestNextEnvVersion_isStrictlyIncreasing(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	last := int64(0)
	for i := 0; i < 1000; i++ {
		v := svc.nextEnvVersion("acme", "api")
		if v <= last {
			t.Fatalf("version %d after %d", v, last)
		}
		last = v
	}
}

// envReplicaBase is a deploy root where the replica's directory already exists,
// as it does on a node that was set up.
func envReplicaBase(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	if err := os.MkdirAll(process.DeployDir(base, "acme", envTestAppName), 0o755); err != nil {
		t.Fatal(err)
	}
	return base
}

func envReplicaHandler(t *testing.T, svc *DeploymentService, local envReconfigurer) *ReplicaHandler {
	t.Helper()
	h := NewReplicaHandler(svc, nil, nil, zap.NewNop(), envReplicaBase(t))
	h.reconfigurer = local
	return h
}

func signedEnvRequest(t *testing.T, svc *DeploymentService, fields map[string]interface{}) *http.Request {
	t.Helper()
	sealed, err := svc.EncodeEnvironment(map[string]string{"APP_VERSION": "api-v2"})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]interface{}{
		"deployment_id": envTestDeploy, "namespace": "acme", "name": envTestAppName,
		"environment": sealed, "env_version": 10,
	}
	for k, v := range fields {
		body[k] = v
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/deployments/replica/env", strings.NewReader(string(raw)))
	req.RemoteAddr = "10.0.0.2:4000"
	if err := svc.signReplicaRequest(req, envTestHome); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestReplicaHandleEnv_appliesTheEnvironmentOnThisNodesPortWithTheRegistrysLimits(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	if _, err := svc.db.Exec(context.Background(), `UPDATE deployments SET memory_limit_mb = 512 WHERE id = ?`, envTestDeploy); err != nil {
		t.Fatal(err)
	}
	local := &fakeReconfigurer{}
	h := envReplicaHandler(t, svc, local)

	// The caller's claims about type and limits are not read.
	w := httptest.NewRecorder()
	h.HandleEnv(w, signedEnvRequest(t, svc, map[string]interface{}{"type": "static", "memory_limit_mb": 1}))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if local.count() != 1 {
		t.Fatalf("reconfigured %d times, want 1", local.count())
	}
	got := local.calls[0]
	if got.Environment["APP_VERSION"] != "api-v2" || got.Port != 10000 || got.MemoryLimitMB != 512 || got.Type != deployments.DeploymentTypeGoBackend {
		t.Errorf("reconfigured env %v port %d mem %d type %s; want APP_VERSION=api-v2 on this node's port 10000 with the registry's 512 MB go-backend",
			got.Environment, got.Port, got.MemoryLimitMB, got.Type)
	}
}

// The id of one deployment with the name of another used to be trusted: the
// replica reconfigured whatever the name said with whatever the id's row held.
func TestReplicaHandleEnv_aMismatchedIdAndNameIsRefused(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	local := &fakeReconfigurer{}
	h := envReplicaHandler(t, svc, local)
	// "other" exists in the registry but has no replica on this node.
	cases := []map[string]interface{}{
		{"name": envTestOtherNm},                // id of api, name of other
		{"deployment_id": envTestOther},         // id of other, name of api
		{"namespace": "someone-else"},           // right id and name, wrong namespace
		{"deployment_id": "no-such-deployment"}, // unknown id
	}
	for _, fields := range cases {
		w := httptest.NewRecorder()
		h.HandleEnv(w, signedEnvRequest(t, svc, fields))
		if w.Code != http.StatusNotFound {
			t.Errorf("%v: status %d, want 404: %s", fields, w.Code, w.Body)
		}
	}
	if local.count() != 0 {
		t.Errorf("a replica was reconfigured %d times for requests that name no replica of this node", local.count())
	}
}

func TestReplicaHandleEnv_aReplicaOnAnotherNodeIsNotThisNodes(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	svc.nodePeerID = envTestNodeB // the registry has a replica on the home node only
	local := &fakeReconfigurer{}
	h := envReplicaHandler(t, svc, local)

	r := signedEnvRequestFor(t, svc, envTestNodeB)
	w := httptest.NewRecorder()
	h.HandleEnv(w, r)
	if w.Code != http.StatusNotFound || local.count() != 0 {
		t.Fatalf("status %d, reconfigured %d: %s", w.Code, local.count(), w.Body)
	}
}

func signedEnvRequestFor(t *testing.T, svc *DeploymentService, audience string) *http.Request {
	t.Helper()
	req := signedEnvRequest(t, svc, nil)
	req.Header = http.Header{}
	if err := svc.signReplicaRequest(req, audience); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestReplicaHandleEnv_aDeploymentWithNoProcessIsRefused(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	if _, err := svc.db.Exec(context.Background(), `UPDATE deployments SET type = 'static' WHERE id = ?`, envTestDeploy); err != nil {
		t.Fatal(err)
	}
	local := &fakeReconfigurer{}
	w := httptest.NewRecorder()
	envReplicaHandler(t, svc, local).HandleEnv(w, signedEnvRequest(t, svc, nil))
	if w.Code != http.StatusBadRequest || local.count() != 0 {
		t.Fatalf("status %d, reconfigured %d: %s", w.Code, local.count(), w.Body)
	}
}

// A retry that overtook a slow earlier call: the older environment must not
// land last.
func TestReplicaHandleEnv_anOlderVersionThanTheAppliedIsRefused(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	local := &fakeReconfigurer{}
	h := envReplicaHandler(t, svc, local)

	for _, step := range []struct {
		version int
		want    int
	}{
		{20, http.StatusOK},
		{10, http.StatusConflict}, // older: refused
		{20, http.StatusOK},       // the same one again is idempotent
		{30, http.StatusOK},
		{25, http.StatusConflict},
	} {
		w := httptest.NewRecorder()
		h.HandleEnv(w, signedEnvRequest(t, svc, map[string]interface{}{"env_version": step.version}))
		if w.Code != step.want {
			t.Errorf("version %d: status %d, want %d: %s", step.version, w.Code, step.want, w.Body)
		}
	}
	if local.count() != 3 {
		t.Errorf("reconfigured %d times, want 3 (the two stale ones must not restart anything)", local.count())
	}
}

// The applied version survives a restart of the gateway: it is a file, not memory.
func TestReplicaHandleEnv_theAppliedVersionSurvivesARestart(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	base := envReplicaBase(t)
	first := NewReplicaHandler(svc, nil, nil, zap.NewNop(), base)
	first.reconfigurer = &fakeReconfigurer{}
	w := httptest.NewRecorder()
	first.HandleEnv(w, signedEnvRequest(t, svc, map[string]interface{}{"env_version": 50}))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}

	restarted := NewReplicaHandler(svc, nil, nil, zap.NewNop(), base)
	restarted.reconfigurer = &fakeReconfigurer{}
	w = httptest.NewRecorder()
	restarted.HandleEnv(w, signedEnvRequest(t, svc, map[string]interface{}{"env_version": 49}))
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409 after a restart: %s", w.Code, w.Body)
	}
}

// A coordination stamp is single-use, so the new route cannot be replayed.
func TestReplicaHandleEnv_aReplayedRequestIsRefused(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	local := &fakeReconfigurer{}
	h := envReplicaHandler(t, svc, local)

	signed := signedEnvRequest(t, svc, nil)
	body := readBody(t, signed)
	send := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, signed.URL.String(), strings.NewReader(body))
		r.RemoteAddr = signed.RemoteAddr
		r.Header = signed.Header.Clone()
		return r
	}
	first2, replay := send(), send()

	w := httptest.NewRecorder()
	h.HandleEnv(w, first2)
	if w.Code != http.StatusOK {
		t.Fatalf("the original: status %d: %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	h.HandleEnv(w, replay)
	if w.Code != http.StatusForbidden {
		t.Fatalf("the replay: status %d, want 403", w.Code)
	}
	if local.count() != 1 {
		t.Errorf("reconfigured %d times, want 1", local.count())
	}
}

func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

func TestReplicaHandleEnv_aFailedRestartIsAnErrorWithTheReasonAndRecordsNoVersion(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	h := envReplicaHandler(t, svc, &fakeReconfigurer{err: errors.New("systemd said no")})

	w := httptest.NewRecorder()
	h.HandleEnv(w, signedEnvRequest(t, svc, nil))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "systemd said no") {
		t.Fatalf("status %d body %s, want a 500 carrying the reason", w.Code, w.Body)
	}
	if got, err := readAppliedVersion(process.DeployDir(h.baseDeployPath, "acme", envTestAppName), envVersion); err != nil || got != 0 {
		t.Errorf("applied version %d (%v) recorded for a restart that failed", got, err)
	}
}

func TestReplicaHandleEnv_isAuthenticatedLikeTheOtherReplicaRoutes(t *testing.T) {
	svc := replicaTestService()
	h := NewReplicaHandler(svc, nil, nil, zap.NewNop(), t.TempDir())
	h.reconfigurer = &fakeReconfigurer{}
	w := httptest.NewRecorder()
	h.HandleEnv(w, replicaRequest(t, svc, "env", ""))
	if w.Code != http.StatusForbidden {
		t.Errorf("unsigned: status %d, want 403", w.Code)
	}
	w = httptest.NewRecorder()
	h.HandleEnv(w, replicaRequest(t, svc, "env", "12D3KooWSomeOtherNode"))
	if w.Code != http.StatusForbidden {
		t.Errorf("stamped for another node: status %d, want 403", w.Code)
	}
}

// deploy --update's replica side: an older version than the applied is refused
// before anything is extracted or swapped.
func TestReplicaHandleUpdate_anOlderVersionThanTheAppliedIsRefused(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	base := envReplicaBase(t)
	if err := recordAppliedVersion(process.DeployDir(base, "acme", envTestAppName), deployVersion, 5); err != nil {
		t.Fatal(err)
	}
	h := NewReplicaHandler(svc, nil, nil, zap.NewNop(), base)

	req := signedEnvRequest(t, svc, map[string]interface{}{"new_version": 4, "build_cid": "QmOld"})
	w := httptest.NewRecorder()
	h.HandleUpdate(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", w.Code, w.Body)
	}
}

func TestReplicaHandleUpdate_aMismatchedIdAndNameIsRefused(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	h := NewReplicaHandler(svc, nil, nil, zap.NewNop(), envReplicaBase(t))

	w := httptest.NewRecorder()
	h.HandleUpdate(w, signedEnvRequest(t, svc, map[string]interface{}{"name": envTestOtherNm, "new_version": 1}))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", w.Code, w.Body)
	}
}
