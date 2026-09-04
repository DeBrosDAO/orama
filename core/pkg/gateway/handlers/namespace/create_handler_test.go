package namespace

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// registry answers the questions the create handler asks and records what it
// writes.
type registry struct {
	rqlite.Client

	existing map[string]int64
	owned    map[string]int
	nextID   int64

	writes []string
	// failQuery fails every read; failNamespaceQuery fails only the
	// does-this-name-exist read, so a test can tell which check refused.
	failQuery          bool
	failNamespaceQuery bool
}

func newRegistry() *registry {
	return &registry{existing: map[string]int64{}, owned: map[string]int{}, nextID: 100}
}

func (r *registry) Query(_ context.Context, dest any, query string, args ...any) error {
	if r.failQuery {
		return errString("registry unreachable")
	}
	rows := reflect.ValueOf(dest).Elem()

	switch {
	case strings.Contains(query, "FROM namespaces"):
		if r.failNamespaceQuery {
			return errString("registry unreachable")
		}
		name, _ := args[0].(string)
		if id, ok := r.existing[name]; ok {
			row := reflect.New(rows.Type().Elem()).Elem()
			row.Field(0).SetInt(id)
			rows.Set(reflect.Append(rows, row))
		}
		return nil
	case strings.Contains(query, "FROM namespace_ownership"):
		wallet, _ := args[0].(string)
		row := reflect.New(rows.Type().Elem()).Elem()
		row.Field(0).SetInt(int64(r.owned[wallet]))
		rows.Set(reflect.Append(rows, row))
		return nil
	}
	return errString("unexpected query: " + query)
}

func (r *registry) Exec(_ context.Context, query string, args ...any) (sql.Result, error) {
	r.writes = append(r.writes, query)
	if strings.Contains(query, "INSERT INTO namespaces") {
		name, _ := args[0].(string)
		r.existing[name] = r.nextID
		r.nextID++
	}
	if strings.Contains(query, "INSERT INTO namespace_ownership") {
		wallet, _ := args[1].(string)
		r.owned[wallet]++
	}
	return createExecResult{}, nil
}

type createExecResult struct{}

func (createExecResult) LastInsertId() (int64, error) { return 1, nil }
func (createExecResult) RowsAffected() (int64, error) { return 1, nil }

type errString string

func (e errString) Error() string { return string(e) }

// recordingProvisioner notes whether provisioning was asked for.
type recordingProvisioner struct {
	called    bool
	namespace string
	wallet    string
	err       error
}

func (p *recordingProvisioner) ProvisionNamespaceCluster(_ context.Context, _ int, namespace, wallet string) (string, string, error) {
	p.called = true
	p.namespace = namespace
	p.wallet = wallet
	if p.err != nil {
		return "", "", p.err
	}
	return "cluster-1", "/v1/namespace/status?id=cluster-1", nil
}

func createRequest(wallet, name string) *http.Request {
	body, _ := json.Marshal(CreateRequest{Name: name})
	r := httptest.NewRequest(http.MethodPost, "/v1/namespaces", strings.NewReader(string(body)))
	if wallet != "" {
		r = r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT, &auth.JWTClaims{Sub: wallet}))
	}
	return r
}

func decodeCreate(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return body
}

// The namespace and its owner grant are written together. A namespace with no
// owner is claimable by whoever signs in to it next, which is the shape of the
// bug this endpoint replaces.
func TestCreate_writesTheNamespaceAndItsOwner(t *testing.T) {
	db := newRegistry()
	prov := &recordingProvisioner{}
	h := NewCreateHandler(db, prov, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202: %s", w.Code, w.Body.String())
	}
	if _, ok := db.existing["myapp"]; !ok {
		t.Error("the namespace was not written")
	}
	if db.owned["0xowner"] != 1 {
		t.Error("no owner grant was written, so the namespace is claimable")
	}
	if !prov.called || prov.namespace != "myapp" || prov.wallet != "0xowner" {
		t.Errorf("provisioning was not started for the new namespace (called=%v ns=%q)",
			prov.called, prov.namespace)
	}
}

// Signing in used to create the namespace and provision it. Creating one is
// the only thing that provisions now, so this is where it has to happen.
func TestCreate_startsProvisioning(t *testing.T) {
	h := NewCreateHandler(newRegistry(), &recordingProvisioner{}, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	body := decodeCreate(t, w)
	if body["status"] != "provisioning" {
		t.Errorf("status %v, want provisioning", body["status"])
	}
	if body["poll_url"] == nil {
		t.Error("no poll URL, so a client cannot follow the cluster coming up")
	}
}

func TestCreate_requiresAWallet(t *testing.T) {
	db := newRegistry()
	h := NewCreateHandler(db, &recordingProvisioner{}, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("", "myapp"))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
	if len(db.writes) != 0 {
		t.Errorf("%d writes from an unauthenticated caller", len(db.writes))
	}
}

// A JWT whose subject is an API key is not a wallet, and a namespace's owner
// is a wallet. Accepting one would create a namespace nobody owns.
func TestCreate_refusesAnAPIKeySubject(t *testing.T) {
	db := newRegistry()
	h := NewCreateHandler(db, &recordingProvisioner{}, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("ak_something:myapp", "myapp"))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
	if len(db.writes) != 0 {
		t.Error("a key-authenticated caller created a namespace with no owner")
	}
}

func TestCreate_refusesATakenName(t *testing.T) {
	db := newRegistry()
	db.existing["myapp"] = 1
	h := NewCreateHandler(db, &recordingProvisioner{}, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xsomeoneelse", "myapp"))

	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", w.Code)
	}
	if decodeCreate(t, w)["code"] != ErrCodeNamespaceTaken {
		t.Error("no machine-readable code on a taken name")
	}
	if len(db.writes) != 0 {
		t.Error("a taken name was written over")
	}
}

// Each namespace is a cluster: rqlite, Olric, a gateway, a share of the mesh.
// There was no limit at all and no cost.
func TestCreate_appliesTheQuota(t *testing.T) {
	db := newRegistry()
	db.owned["0xowner"] = maxNamespacesPerWallet
	h := NewCreateHandler(db, &recordingProvisioner{}, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "onemore"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", w.Code)
	}
	if decodeCreate(t, w)["code"] != ErrCodeNamespaceQuota {
		t.Error("no machine-readable code on the quota refusal")
	}
	if len(db.writes) != 0 {
		t.Error("a namespace was created past the quota")
	}
}

// The name becomes a DNS label, a systemd instance name and a directory.
func TestCreate_refusesNamesThatCannotBeUsed(t *testing.T) {
	for _, name := range []string{
		"", "a", "-leading", "trailing-", "Upper case", "with space", "with_underscore",
		"with.dot", "with/slash", strings.Repeat("x", 41), "../escape", "ns$(id)",
	} {
		db := newRegistry()
		h := NewCreateHandler(db, &recordingProvisioner{}, zap.NewNop())

		w := httptest.NewRecorder()
		h.ServeHTTP(w, createRequest("0xowner", name))

		if w.Code != http.StatusBadRequest {
			t.Errorf("%q answered %d, want 400", name, w.Code)
		}
		if len(db.writes) != 0 {
			t.Errorf("%q was created", name)
		}
	}
}

func TestCreate_refusesReservedNames(t *testing.T) {
	for name := range reservedNamespaces {
		db := newRegistry()
		h := NewCreateHandler(db, &recordingProvisioner{}, zap.NewNop())

		w := httptest.NewRecorder()
		h.ServeHTTP(w, createRequest("0xowner", name))

		if w.Code != http.StatusBadRequest {
			t.Errorf("the reserved name %q answered %d, want 400", name, w.Code)
		}
	}
}

// Not being able to read the registry is not permission to create a namespace
// that may already exist or may be past the quota.
//
// Each read is checked separately: a name-collision check that cannot run must
// refuse on its own, not lean on the quota check happening to fail too.
func TestCreate_deniesWhenTheNameCannotBeChecked(t *testing.T) {
	db := newRegistry()
	db.failNamespaceQuery = true
	h := NewCreateHandler(db, &recordingProvisioner{}, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", w.Code)
	}
	if len(db.writes) != 0 {
		t.Error("a namespace was created without checking whether the name was free")
	}
}

func TestCreate_deniesWhenTheRegistryCannotBeRead(t *testing.T) {
	db := newRegistry()
	db.failQuery = true
	h := NewCreateHandler(db, &recordingProvisioner{}, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", w.Code)
	}
	if len(db.writes) != 0 {
		t.Error("a namespace was created without checking whether the name was free")
	}
}

// A namespace whose cluster did not start is reported as created but not
// provisioned, rather than as a cluster that will never appear.
func TestCreate_saysSoWhenProvisioningDoesNotStart(t *testing.T) {
	h := NewCreateHandler(newRegistry(), &recordingProvisioner{err: errString("no capacity")}, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	body := decodeCreate(t, w)
	if body["status"] != "created" {
		t.Errorf("status %v, want created", body["status"])
	}
	reason, _ := body["cluster"].(string)
	if !strings.Contains(reason, "no capacity") {
		t.Errorf("the reason provisioning did not start is not reported: %q", reason)
	}
}

func TestCreate_normalisesTheName(t *testing.T) {
	db := newRegistry()
	h := NewCreateHandler(db, &recordingProvisioner{}, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xOwner", "  MyApp  "))

	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if _, ok := db.existing["myapp"]; !ok {
		t.Errorf("the namespace was stored as something other than myapp: %v", db.existing)
	}
	if db.owned["0xowner"] != 1 {
		t.Errorf("the owner was stored unnormalised: %v", db.owned)
	}
}
