package hostfunctions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// registryNamespace is the cluster gateway's client_namespace: its database is
// the cluster registry.
const registryNamespace = "default"

// dbHostCalls runs every database host function once with ctx and returns
// each one's error by name. A call that reaches refusingDB panics, which is
// the test failing: the refusal has to come before the database.
func dbHostCalls(h *HostFunctions, ctx context.Context) map[string]error {
	const sql = "UPDATE deployment_domains SET deployment_id = 'mine' WHERE domain = 'victim.example'"
	const ops = `{"ops":[{"kind":"exec","sql":"UPDATE deployments SET namespace = 'mine'"}]}`
	errs := map[string]error{}
	_, errs["db_query"] = h.DBQuery(ctx, sql, nil)
	_, errs["db_execute"] = h.DBExecute(ctx, sql, nil)
	_, errs["db_execute_v2"] = h.DBExecuteV2(ctx, sql, nil)
	_, errs["db_query_v2"] = h.DBQueryV2(ctx, sql, nil)
	_, errs["db_transaction"] = h.DBTransaction(ctx, []byte(ops))
	_, errs["db_query_batch"] = h.DBQueryBatch(ctx, []byte(`{"ops":[{"sql":"SELECT * FROM functions"}]}`))
	_, errs["exec_and_publish"] = h.ExecAndPublish(ctx, []byte(ops), "wake", []byte("{}"))
	return errs
}

// bugboard #427: the cluster gateway handed a tenant function the cluster
// registry, where the namespace-placed tables hold every tenant's rows. None
// of the database host functions may serve a function from another namespace.
func TestCheckDatabaseAccess_foreignNamespaceOnRegistryIsRefused(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop(), db: refusingDB{}, dbNamespace: registryNamespace}
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "tenant-a"})

	for fn, err := range dbHostCalls(h, ctx) {
		if !errors.Is(err, serverless.ErrDatabaseOfAnotherNamespace) {
			t.Errorf("%s: err = %v, want ErrDatabaseOfAnotherNamespace", fn, err)
			continue
		}
		var hfe *serverless.HostFunctionError
		if !errors.As(err, &hfe) || hfe.Function != fn {
			t.Errorf("%s: the refusal does not name the host function: %v", fn, err)
		}
		if !strings.Contains(err.Error(), "ns-tenant-a.") {
			t.Errorf("%s: the refusal does not say where the function belongs: %v", fn, err)
		}
	}
}

// The same rule on a namespace gateway: its database is one tenant's, and a
// function row naming another namespace does not get it.
func TestCheckDatabaseAccess_foreignNamespaceOnNamespaceGatewayIsRefused(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop(), db: refusingDB{}, dbNamespace: "tenant-a"}
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "tenant-b"})

	for fn, err := range dbHostCalls(h, ctx) {
		if !errors.Is(err, serverless.ErrDatabaseOfAnotherNamespace) {
			t.Errorf("%s: err = %v, want ErrDatabaseOfAnotherNamespace", fn, err)
		}
	}
}

// A host call outside an invocation — a warm-pool module's _initialize — has
// no namespace to check, so it is refused rather than trusted.
func TestCheckDatabaseAccess_noInvocationIsRefused(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop(), db: refusingDB{}, dbNamespace: testNamespace}

	for _, ctx := range []context.Context{
		context.Background(),
		invocationCtx(&serverless.InvocationContext{}),
	} {
		for fn, err := range dbHostCalls(h, ctx) {
			if !errors.Is(err, serverless.ErrDatabaseOfAnotherNamespace) {
				t.Errorf("%s: err = %v, want ErrDatabaseOfAnotherNamespace", fn, err)
			}
		}
	}
}

// A HostFunctions built without its database's namespace serves nobody.
func TestCheckDatabaseAccess_unconfiguredNamespaceIsRefused(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop(), db: refusingDB{}}

	for fn, err := range dbHostCalls(h, nsCtx()) {
		if !errors.Is(err, serverless.ErrDatabaseOfAnotherNamespace) {
			t.Errorf("%s: err = %v, want ErrDatabaseOfAnotherNamespace", fn, err)
		}
	}
}

// The gateway's own namespace reaches its database — on the cluster gateway
// that is the `default` namespace's functions.
func TestCheckDatabaseAccess_ownNamespaceIsServed(t *testing.T) {
	fake := &fakeBatchClient{}
	h := &HostFunctions{db: fake, dbNamespace: registryNamespace}
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: registryNamespace})

	if _, err := h.DBQueryBatch(ctx, []byte(`{"ops":[{"sql":"SELECT 1"}]}`)); err != nil {
		t.Fatalf("db_query_batch in the gateway's own namespace: %v", err)
	}
	if fake.queryCalls != 1 {
		t.Errorf("the query did not reach the database: %d calls", fake.queryCalls)
	}
}

// No database is still reported as no database, not as a namespace mismatch.
func TestCheckDatabaseAccess_nilDatabaseIsUnavailable(t *testing.T) {
	h := &HostFunctions{dbNamespace: testNamespace}
	for fn, err := range dbHostCalls(h, nsCtx()) {
		if !errors.Is(err, serverless.ErrDatabaseUnavailable) {
			t.Errorf("%s: err = %v, want ErrDatabaseUnavailable", fn, err)
		}
	}
}

// The gateway sets the namespace through the config; a padded value is the
// same namespace.
func TestNewHostFunctions_takesTheDatabaseNamespaceFromConfig(t *testing.T) {
	h := NewHostFunctions(nil, nil, nil, nil, nil, nil, nil, nil, nil,
		HostFunctionsConfig{DatabaseNamespace: " tenant-a "}, zap.NewNop())
	if h.dbNamespace != "tenant-a" {
		t.Errorf("dbNamespace = %q, want %q", h.dbNamespace, "tenant-a")
	}
}
