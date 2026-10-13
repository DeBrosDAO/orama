package deployments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"go.uber.org/zap"
)

// The error a peer's kubo gave in the field, as the ipfs client wraps it.
func unavailableErr(t *testing.T) error {
	t.Helper()
	return fmt.Errorf("get: %w", ipfs.ErrStreamFailed)
}

func TestAwaitContent_succeedsOnceTheContentArrives(t *testing.T) {
	calls := 0
	get := func(context.Context) (io.ReadCloser, error) {
		calls++
		if calls < 3 {
			return nil, unavailableErr(t)
		}
		return io.NopCloser(strings.NewReader("tarball")), nil
	}
	r, err := awaitContent(context.Background(), get, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("awaitContent: %v", err)
	}
	defer r.Close()
	if calls != 3 {
		t.Errorf("fetched %d times, want 3", calls)
	}
}

func TestAwaitContent_firstTrySuccessDoesNotWait(t *testing.T) {
	calls := 0
	get := func(context.Context) (io.ReadCloser, error) {
		calls++
		return io.NopCloser(strings.NewReader("x")), nil
	}
	if _, err := awaitContent(context.Background(), get, time.Second, time.Hour); err != nil || calls != 1 {
		t.Fatalf("err=%v calls=%d, want one call and no error", err, calls)
	}
}

func TestAwaitContent_timesOutWithTheLastError(t *testing.T) {
	get := func(context.Context) (io.ReadCloser, error) { return nil, unavailableErr(t) }
	start := time.Now()
	_, err := awaitContent(context.Background(), get, 30*time.Millisecond, 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "still not retrievable") || !errors.Is(err, ipfs.ErrStreamFailed) {
		t.Errorf("error %q should say the content stayed unretrievable and wrap the cause", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("poll ran %s, far past its 30ms bound", time.Since(start))
	}
}

func TestAwaitContent_otherFailuresAreNotRetried(t *testing.T) {
	calls := 0
	boom := errors.New("kubo is down")
	get := func(context.Context) (io.ReadCloser, error) { calls++; return nil, boom }
	_, err := awaitContent(context.Background(), get, time.Second, time.Millisecond)
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err=%v calls=%d, want the original error after one call", err, calls)
	}
}

func TestAwaitContent_stopsWhenTheCallerCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	get := func(context.Context) (io.ReadCloser, error) { cancel(); return nil, unavailableErr(t) }
	if _, err := awaitContent(ctx, get, time.Hour, time.Hour); err == nil {
		t.Fatal("expected an error after cancellation")
	}
}

func TestWriteReplicaError_isJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	writeReplicaError(rr, http.StatusInternalServerError, "Failed to extract content: boom")
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q, want application/json", ct)
	}
	var body replicaErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body.Error != "Failed to extract content: boom" {
		t.Errorf("body %q did not decode to the error: %v", rr.Body.String(), err)
	}
}

// Every refusal an internal route makes, before and after authentication, is
// the JSON shape: a caller parsing it must never meet prose.
func TestReplicaHandler_refusalsAreJSON(t *testing.T) {
	svc := replicaTestService()
	h := NewReplicaHandler(svc, nil, nil, zap.NewNop(), t.TempDir())
	for route, handle := range replicaHandlers(h) {
		cases := map[string]*http.Request{
			"unauthenticated": replicaRequest(t, svc, route, ""),
			"bad name":        replicaRequest(t, svc, route, replicaTestNodeID),
		}
		for name, req := range cases {
			rr := httptest.NewRecorder()
			handle(rr, req)
			var body replicaErrorBody
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body.Error == "" {
				t.Errorf("%s/%s: body %q is not the JSON error shape (%v)", route, name, rr.Body.String(), err)
			}
		}
	}
}

func TestParseReplicaResponse(t *testing.T) {
	ok, err := parseReplicaResponse("n1", http.StatusOK, []byte(`{"port":10001}`))
	if err != nil || ok["port"] != float64(10001) {
		t.Fatalf("ok reply: %v %v", ok, err)
	}

	// The peer's own words, from a JSON refusal.
	_, err = parseReplicaResponse("n1", http.StatusInternalServerError, []byte(`{"error":"Failed to extract content: kubo stream failed"}`))
	if err == nil || !strings.Contains(err.Error(), "Failed to extract content: kubo stream failed") || !strings.Contains(err.Error(), "500") {
		t.Errorf("JSON refusal not surfaced: %v", err)
	}

	// A plain-text refusal from in front of the handler: the text, not a JSON parse error.
	_, err = parseReplicaResponse("n1", http.StatusInternalServerError, []byte("Failed to extract content\n"))
	if err == nil || !strings.Contains(err.Error(), "Failed to extract content") || strings.Contains(err.Error(), "invalid character") {
		t.Errorf("plain-text refusal must surface its text: %v", err)
	}

	// An empty refusal still reads as a refusal.
	if _, err = parseReplicaResponse("n1", http.StatusBadGateway, nil); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("empty refusal: %v", err)
	}

	// A success that is not JSON is an error, not an empty result.
	if _, err = parseReplicaResponse("n1", http.StatusOK, []byte("ok")); err == nil {
		t.Error("non-JSON success accepted")
	}
}

func TestRemoteErrorText_boundsLongText(t *testing.T) {
	if got := remoteErrorText([]byte(strings.Repeat("x", 10*maxReplicaErrorText))); len(got) != maxReplicaErrorText {
		t.Errorf("len %d, want %d", len(got), maxReplicaErrorText)
	}
}

// ---- a failed replica setup is recorded -----------------------------------

type recordingDB struct {
	mockRQLiteClient
	mu    sync.Mutex
	execs []execRecord
}

type execRecord struct {
	query string
	args  []interface{}
}

func (d *recordingDB) Exec(_ context.Context, query string, args ...interface{}) (sql.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.execs = append(d.execs, execRecord{query, args})
	return nil, nil
}

func (d *recordingDB) Query(context.Context, interface{}, string, ...interface{}) error {
	return errors.New("no such node")
}

func recordingService(db *recordingDB) *DeploymentService {
	return &DeploymentService{
		db:             db,
		logger:         zap.NewNop(),
		nodePeerID:     replicaTestNodeID,
		replicaManager: deployments.NewReplicaManager(db, nil, nil, zap.NewNop()),
	}
}

func TestRecordReplicaSetupFailure_writesFailedRowAndReason(t *testing.T) {
	db := &recordingDB{}
	s := recordingService(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the request that started the setup is long gone

	cause := &replicaStatusError{nodeID: "n2", status: 500, text: "Failed to extract content: kubo stream failed"}
	s.recordReplicaSetupFailure(ctx, &deployments.Deployment{ID: "dep-1"}, "n2", cause)

	var rowFailed, eventHasReason bool
	for _, e := range db.execs {
		if strings.Contains(e.query, "INSERT INTO deployment_replicas") && containsArg(e.args, deployments.ReplicaStatusFailed) {
			rowFailed = true
		}
		if strings.Contains(e.query, "deployment_events") && containsArg(e.args, replicaSetupFailedEvent) {
			for _, a := range e.args {
				msg, ok := a.(string)
				if ok && strings.Contains(msg, "kubo stream failed") {
					t.Errorf("the peer's own text reached the tenant-readable event: %q", msg)
				}
				if ok && strings.Contains(msg, "n2") && strings.Contains(msg, "refused the change (status 500)") {
					eventHasReason = true
				}
			}
		}
	}
	if !rowFailed {
		t.Error("the replica was not recorded as failed")
	}
	if !eventHasReason {
		t.Error("the failure and a generic reason were not recorded as an event")
	}
}

// A node whose overlay address cannot even be found still shows up as a
// failed replica rather than vanishing.
func TestSetupDynamicReplica_unreachableNodeIsRecordedFailed(t *testing.T) {
	db := &recordingDB{}
	s := recordingService(db)

	s.SetupDynamicReplica(context.Background(), &deployments.Deployment{ID: "dep-2"}, "n3")

	for _, e := range db.execs {
		if strings.Contains(e.query, "INSERT INTO deployment_replicas") && containsArg(e.args, deployments.ReplicaStatusFailed) {
			return
		}
	}
	t.Error("a replica whose node could not be reached was not recorded as failed")
}

func containsArg(args []interface{}, want interface{}) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
