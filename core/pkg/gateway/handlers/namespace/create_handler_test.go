package namespace

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	namespacepkg "github.com/DeBrosOfficial/network/pkg/namespace"
	"github.com/DeBrosOfficial/network/pkg/nodenames"
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
	failOperators      bool
	failCreators       bool
	// pendingTeardown names the nodes still owed a cleanup for any name;
	// failPendingQuery fails that read alone.
	pendingTeardown  []string
	failPendingQuery bool
	// lostRace makes the namespace insert write nothing, as when another
	// create of the same name committed after the existence check.
	lostRace bool
	// fillOnInsert sets a wallet's owned count when a namespace row is
	// inserted, as other creates by that wallet committing between the
	// handler's count and its owner grant.
	fillOnInsert map[string]int

	// mode is the stored namespace_creation value. Empty means no row, which
	// the handler reads as operators. newRegistry sets open: these tests
	// describe an upgraded cluster, which keeps today's behaviour.
	mode string
	// walletCap is the stored cap. Empty means no row, so the default applies.
	walletCap string
	operators map[string]bool
	creators  map[string]bool
}

func newRegistry() *registry {
	return &registry{
		existing: map[string]int64{},
		owned:    map[string]int{},
		nextID:   100,
		mode:     operator.CreationOpen,
	}
}

func (r *registry) Query(_ context.Context, dest any, query string, args ...any) error {
	if r.failQuery {
		return errString("registry unreachable")
	}
	rows := reflect.ValueOf(dest).Elem()

	switch {
	case strings.Contains(query, "cluster_settings"):
		rows := reflect.ValueOf(dest).Elem()
		elem := rows.Type().Elem()
		add := func(key, value string) {
			row := reflect.New(elem).Elem()
			row.Field(0).SetString(key)
			row.Field(1).SetString(value)
			rows.Set(reflect.Append(rows, row))
		}
		if r.mode != "" {
			add(operator.SettingNamespaceCreation, r.mode)
		}
		if r.walletCap != "" {
			add(operator.SettingMaxNamespacesPerWallet, r.walletCap)
		}
		return nil
	case strings.Contains(query, "FROM namespace_pending_cleanup"):
		if r.failPendingQuery {
			return errString("registry unreachable")
		}
		for _, node := range r.pendingTeardown {
			row := reflect.New(rows.Type().Elem()).Elem()
			row.Field(0).SetString(node)
			rows.Set(reflect.Append(rows, row))
		}
		return nil
	case strings.Contains(query, "FROM operators"):
		if r.failOperators {
			return errString("registry unreachable")
		}
		return allowListed(dest, r.operators, args)
	case strings.Contains(query, "FROM namespace_creators"):
		if r.failCreators {
			return errString("registry unreachable")
		}
		return allowListed(dest, r.creators, args)
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
	case strings.Contains(query, "FROM grants"):
		wallet, _ := args[0].(string)
		row := reflect.New(rows.Type().Elem()).Elem()
		row.Field(0).SetInt(int64(r.owned[wallet]))
		rows.Set(reflect.Append(rows, row))
		return nil
	}
	return errString("unexpected query: " + query)
}

func allowListed(dest any, members map[string]bool, args []any) error {
	rows := reflect.ValueOf(dest).Elem()
	if len(args) == 0 {
		return nil
	}
	wallet, _ := args[0].(string)
	if members[strings.ToLower(wallet)] {
		row := reflect.New(rows.Type().Elem()).Elem()
		row.Field(0).SetString(strings.ToLower(wallet))
		rows.Set(reflect.Append(rows, row))
	}
	return nil
}

func (r *registry) Exec(_ context.Context, query string, args ...any) (sql.Result, error) {
	r.writes = append(r.writes, query)
	if strings.Contains(query, "INTO namespaces") {
		if r.lostRace {
			return createExecResult{affected: 0}, nil
		}
		name, _ := args[0].(string)
		r.existing[name] = r.nextID
		r.nextID++
		for wallet, n := range r.fillOnInsert {
			r.owned[wallet] = n
		}
	}
	if strings.Contains(query, "DELETE FROM namespaces") {
		for name, id := range r.existing {
			if any(id) == args[0] {
				delete(r.existing, name)
			}
		}
	}
	if strings.Contains(query, "DELETE FROM grants") {
		for wallet := range r.owned {
			if r.owned[wallet] > 0 {
				r.owned[wallet]--
			}
		}
	}
	if strings.Contains(query, "INSERT INTO grants") {
		wallet, _ := args[1].(string)
		// The statement writes only while the wallet is under its cap.
		if walletCap, ok := args[len(args)-1].(int); ok && r.owned[wallet] >= walletCap {
			return createExecResult{affected: 0}, nil
		}
		r.owned[wallet]++
	}
	return createExecResult{affected: 1}, nil
}

type createExecResult struct{ affected int64 }

func (createExecResult) LastInsertId() (int64, error)   { return 1, nil }
func (r createExecResult) RowsAffected() (int64, error) { return r.affected, nil }

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
	h := NewCreateHandler(db, prov, nil, zap.NewNop())

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
	h := NewCreateHandler(newRegistry(), &recordingProvisioner{}, nil, zap.NewNop())

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
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

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
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

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
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

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

// Two creates of one name both pass the existence check; the one whose insert
// writes nothing must answer 409 like the check, not 500, and must not write
// an owner grant for a namespace it did not create.
func TestCreate_lostInsertRaceIsAConflict(t *testing.T) {
	db := newRegistry()
	db.lostRace = true
	prov := &recordingProvisioner{}
	h := NewCreateHandler(db, prov, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xsomeoneelse", "myapp"))

	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", w.Code, w.Body.String())
	}
	if decodeCreate(t, w)["code"] != ErrCodeNamespaceTaken {
		t.Error("no machine-readable code on a lost race")
	}
	for _, q := range db.writes {
		if strings.Contains(q, "grants") || strings.Contains(q, "principals") {
			t.Errorf("the losing create wrote an owner: %s", q)
		}
	}
	if prov.called {
		t.Error("the losing create started provisioning")
	}
}

// Each namespace is a cluster: rqlite, Olric, a gateway, a share of the mesh.
// There was no limit at all and no cost.
func TestCreate_appliesTheQuota(t *testing.T) {
	db := newRegistry()
	db.owned["0xowner"] = operator.DefaultMaxNamespacesPerWallet
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

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
		h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

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

// The sync that serves node names owns the dns_records rows tagged with this name and removes the
// ones it does not expect, so a tenant namespace may not be called it.
func TestCreate_refusesTheNodeNamesOwnerTag(t *testing.T) {
	if !reservedNamespaces[nodenames.RecordNamespace] {
		t.Fatalf("%q is not reserved", nodenames.RecordNamespace)
	}
	db := newRegistry()
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", nodenames.RecordNamespace))
	if w.Code != http.StatusBadRequest || len(db.writes) != 0 {
		t.Fatalf("answered %d, wrote %d", w.Code, len(db.writes))
	}
}

func TestCreate_refusesReservedNames(t *testing.T) {
	for name := range reservedNamespaces {
		db := newRegistry()
		h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

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
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

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
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", w.Code)
	}
	if len(db.writes) != 0 {
		t.Error("a namespace was created without checking whether the name was free")
	}
}

// A namespace whose cluster could not be started is not left behind: the row
// and the owner grant are removed and the answer says nothing was created.
// Left behind they could not be used, counted against the wallet's cap, and
// kept the name taken.
func TestCreate_provisioningThatDoesNotStartUndoesTheCreate(t *testing.T) {
	db := newRegistry()
	h := NewCreateHandler(db, &recordingProvisioner{err: errString("no capacity")}, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503: %s", w.Code, w.Body.String())
	}
	body := decodeCreate(t, w)
	if body["code"] != ErrCodeNamespaceProvision {
		t.Errorf("code %v, want %s", body["code"], ErrCodeNamespaceProvision)
	}
	if reason, _ := body["error"].(string); strings.Contains(reason, "no capacity") {
		t.Errorf("the provisioner's error reached the client: %q", reason)
	}
	if _, ok := db.existing["myapp"]; ok {
		t.Error("the namespace row was left behind")
	}
	if db.owned["0xowner"] != 0 {
		t.Errorf("the wallet still owns %d namespaces", db.owned["0xowner"])
	}
}

// A fleet with no node that has room says so, with a code of its own: "try
// again" is wrong advice when every node is full.
func TestCreate_noNodeWithRoomIsACapacityRefusal(t *testing.T) {
	db := newRegistry()
	h := NewCreateHandler(db, &recordingProvisioner{err: fmt.Errorf("select: %w", namespacepkg.ErrInsufficientNodes)}, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503: %s", w.Code, w.Body.String())
	}
	body := decodeCreate(t, w)
	if body["code"] != ErrCodeNamespaceCapacity {
		t.Errorf("code %v, want %s", body["code"], ErrCodeNamespaceCapacity)
	}
	if reason, _ := body["error"].(string); strings.Contains(reason, "try again") {
		t.Errorf("a full fleet was told to try again: %q", reason)
	}
	if _, ok := db.existing["myapp"]; ok {
		t.Error("the namespace row was left behind")
	}
}

func TestCreate_normalisesTheName(t *testing.T) {
	db := newRegistry()
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

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

// Not knowing whether the caller is an operator is not permission to create.
func TestCreate_deniesWhenOperatorMembershipCannotBeRead(t *testing.T) {
	db := newRegistry()
	db.mode = operator.CreationOperators
	db.failOperators = true
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503: %s", w.Code, w.Body.String())
	}
	if len(db.writes) != 0 {
		t.Error("a namespace was created without knowing whether the wallet is an operator")
	}
}

func TestCreate_refusesAnUnrecognisedCreationMode(t *testing.T) {
	db := newRegistry()
	db.mode = "public"
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503: %s", w.Code, w.Body.String())
	}
	if len(db.writes) != 0 {
		t.Error("an unrecognised creation mode was treated as permission")
	}
}

// The real schema, not the fake. A missing row is operators; each stored mode
// allows one caller and denies the other; the cap is 10 until an operator
// raises it.
func TestCreate_modesAllowAndDenyTheRightCaller(t *testing.T) {
	const (
		op  = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		who = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	cases := []struct {
		mode    string
		caller  string
		asOp    bool
		asMaker bool
		want    int
	}{
		{operator.CreationOperators, op, true, false, http.StatusCreated},
		{operator.CreationOperators, who, false, true, http.StatusForbidden},
		{operator.CreationAllowlist, who, false, true, http.StatusCreated},
		{operator.CreationAllowlist, op, true, false, http.StatusForbidden},
		{operator.CreationOpen, who, false, false, http.StatusCreated},
		// The same address in the other case still matches the stored row.
		{operator.CreationAllowlist, "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", false, true, http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.mode+"/"+tc.caller[:6], func(t *testing.T) {
			db := migratedDB(t)
			if _, err := db.Exec(
				`INSERT INTO cluster_settings (key, value, updated_by) VALUES (?, ?, 'test')`,
				operator.SettingNamespaceCreation, tc.mode); err != nil {
				t.Fatal(err)
			}
			if tc.asOp {
				if _, err := db.Exec(
					`INSERT INTO operators (wallet, added_by) VALUES (?, 'test')`, strings.ToLower(op)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.asMaker {
				if _, err := db.Exec(
					`INSERT INTO namespace_creators (wallet, added_by) VALUES (?, 'test')`, strings.ToLower(who)); err != nil {
					t.Fatal(err)
				}
			}
			h := NewCreateHandler(rqlite.NewClient(db), nil, nil, zap.NewNop())
			w := httptest.NewRecorder()
			h.ServeHTTP(w, createRequest(tc.caller, "myapp"))
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusForbidden && decodeCreate(t, w)["code"] != ErrCodeNamespaceCreation {
				t.Fatalf("code %v, want %s", decodeCreate(t, w)["code"], ErrCodeNamespaceCreation)
			}
			got := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = 'myapp'`)
			if tc.want == http.StatusCreated && got != 1 {
				t.Fatalf("namespace rows = %d, want 1", got)
			}
			if tc.want != http.StatusCreated && got != 0 {
				t.Fatalf("a denied caller created the namespace")
			}
		})
	}
}

// No stored setting. Migration 063 leaves a new registry that way, and the
// handler resolves it to operators rather than to open.
func TestCreate_newClusterDefaultsToOperators(t *testing.T) {
	db := migratedDB(t)
	if n := countRows(t, db, `SELECT COUNT(*) FROM cluster_settings WHERE key = ?`, operator.SettingNamespaceCreation); n != 0 {
		t.Fatalf("a fresh registry stored namespace_creation (%d rows)", n)
	}
	h := NewCreateHandler(rqlite.NewClient(db), nil, nil, zap.NewNop())

	denied := httptest.NewRecorder()
	h.ServeHTTP(denied, createRequest("0xcccccccccccccccccccccccccccccccccccccccc", "myapp"))
	if denied.Code != http.StatusForbidden || decodeCreate(t, denied)["code"] != ErrCodeNamespaceCreation {
		t.Fatalf("stranger: %d %s", denied.Code, denied.Body.String())
	}

	const op = "0xdddddddddddddddddddddddddddddddddddddddd"
	if _, err := db.Exec(`INSERT INTO operators (wallet, added_by) VALUES (?, 'test')`, op); err != nil {
		t.Fatal(err)
	}
	allowed := httptest.NewRecorder()
	h.ServeHTTP(allowed, createRequest(op, "myapp"))
	if allowed.Code != http.StatusCreated {
		t.Fatalf("operator: %d %s", allowed.Code, allowed.Body.String())
	}
}

// The default cap still stops the 11th namespace. A stored cap of 11 lets
// that one through and stops the 12th.
func TestCreate_walletCapDeniesTheEleventhUntilRaised(t *testing.T) {
	db := migratedDB(t)
	if _, err := db.Exec(
		`INSERT INTO cluster_settings (key, value, updated_by) VALUES (?, ?, 'test')`,
		operator.SettingNamespaceCreation, operator.CreationOpen); err != nil {
		t.Fatal(err)
	}
	h := NewCreateHandler(rqlite.NewClient(db), nil, nil, zap.NewNop())
	const wallet = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

	for i := 1; i <= operator.DefaultMaxNamespacesPerWallet; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, createRequest(wallet, fmt.Sprintf("app%02d", i)))
		if w.Code != http.StatusCreated {
			t.Fatalf("namespace %d: %d %s", i, w.Code, w.Body.String())
		}
	}

	eleventh := httptest.NewRecorder()
	h.ServeHTTP(eleventh, createRequest(wallet, "app11"))
	if eleventh.Code != http.StatusForbidden || decodeCreate(t, eleventh)["code"] != ErrCodeNamespaceQuota {
		t.Fatalf("11th: %d %s", eleventh.Code, eleventh.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = 'app11'`); n != 0 {
		t.Fatal("the 11th namespace was created at the default cap")
	}

	if _, err := db.Exec(
		`INSERT INTO cluster_settings (key, value, updated_by) VALUES (?, '11', 'test')`,
		operator.SettingMaxNamespacesPerWallet); err != nil {
		t.Fatal(err)
	}
	raised := httptest.NewRecorder()
	h.ServeHTTP(raised, createRequest(wallet, "app11"))
	if raised.Code != http.StatusCreated {
		t.Fatalf("11th at cap 11: %d %s", raised.Code, raised.Body.String())
	}
	twelfth := httptest.NewRecorder()
	h.ServeHTTP(twelfth, createRequest(wallet, "app12"))
	if twelfth.Code != http.StatusForbidden || decodeCreate(t, twelfth)["code"] != ErrCodeNamespaceQuota {
		t.Fatalf("12th: %d %s", twelfth.Code, twelfth.Body.String())
	}
}

// The real schema: creates of one name racing through the handler at once
// leave exactly one namespace and one owner, every loser answers 409, and
// none answers 500 (stagenet e2e, 2026-09-30: five of six racers got 500).
func TestCreate_concurrentSameNameOneWinner(t *testing.T) {
	db := migratedDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`INSERT INTO cluster_settings (key, value, updated_by) VALUES (?, ?, 'test')`,
		operator.SettingNamespaceCreation, operator.CreationOpen); err != nil {
		t.Fatal(err)
	}
	h := NewCreateHandler(rqlite.NewClient(db), nil, nil, zap.NewNop())
	const racers = 6
	codes := make(chan int, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, createRequest(fmt.Sprintf("0x%040d", i+1), "myapp"))
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	won := 0
	for c := range codes {
		switch c {
		case http.StatusCreated:
			won++
		case http.StatusConflict:
		default:
			t.Errorf("a racer got %d, want 201 or 409", c)
		}
	}
	if won != 1 {
		t.Errorf("%d racers won, want 1", won)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = 'myapp'`); n != 1 {
		t.Errorf("namespace rows = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM grants WHERE role = 'owner'`); n != 1 {
		t.Errorf("owner grants = %d, want 1", n)
	}
}

// Concurrent creates by one wallet all passed the handler's count: a wallet
// capped at ten was seen owning twelve. The owner grant decides now, and a
// create that loses removes the namespace row it wrote.
func TestCreate_theCapHoldsWhenOtherCreatesCommitFirst(t *testing.T) {
	db := newRegistry()
	db.owned["0xowner"] = operator.DefaultMaxNamespacesPerWallet - 1
	db.fillOnInsert = map[string]int{"0xowner": operator.DefaultMaxNamespacesPerWallet}
	prov := &recordingProvisioner{}
	h := NewCreateHandler(db, prov, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "onetoomany"))

	if w.Code != http.StatusForbidden || decodeCreate(t, w)["code"] != ErrCodeNamespaceQuota {
		t.Fatalf("status %d, want 403 %s: %s", w.Code, ErrCodeNamespaceQuota, w.Body.String())
	}
	if db.owned["0xowner"] != operator.DefaultMaxNamespacesPerWallet {
		t.Errorf("the wallet owns %d, past its cap of %d", db.owned["0xowner"], operator.DefaultMaxNamespacesPerWallet)
	}
	if _, left := db.existing["onetoomany"]; left {
		t.Error("the refused create left its namespace row behind, unowned")
	}
	if prov.called {
		t.Error("the refused create started provisioning")
	}
}

// The refusal names the cap, not the wallet's count.
func TestCreate_theQuotaRefusalNamesTheCap(t *testing.T) {
	db := newRegistry()
	db.owned["0xowner"] = operator.DefaultMaxNamespacesPerWallet + 2
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "onemore"))
	msg, _ := decodeCreate(t, w)["error"].(string)
	if !strings.Contains(msg, fmt.Sprintf("(%d)", operator.DefaultMaxNamespacesPerWallet)) {
		t.Errorf("the refusal %q does not name the cap of %d", msg, operator.DefaultMaxNamespacesPerWallet)
	}
}
