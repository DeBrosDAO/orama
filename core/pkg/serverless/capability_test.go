package serverless

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// ws_auth must survive the round trip through the registry, on every read
// path, against the migrated schema: dropped on one, a function declaring
// `ws_auth: capability` would refuse every capability read through it.
func TestRegistry_everyReadPathReturnsWSAuth(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewRegistry(rqlite.NewClient(db), NewMockIPFSClient(), RegistryConfig{}, zap.NewNop())
	ctx := context.Background()
	if _, err := r.Register(ctx, &FunctionDefinition{Name: "rpc-router", Namespace: "anchat",
		WSPersistent: true, WSAuth: WSAuthCapability}, []byte("wasm")); err != nil {
		t.Fatalf("register: %v", err)
	}
	registered, err := r.Get(ctx, "anchat", "rpc-router", 0)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	read := map[string]func() (*Function, error){
		"Get latest":        func() (*Function, error) { return r.Get(ctx, "anchat", "rpc-router", 0) },
		"Get version":       func() (*Function, error) { return r.Get(ctx, "anchat", "rpc-router", registered.Version) },
		"GetByID":           func() (*Function, error) { return r.GetByID(ctx, registered.ID) },
		"getByNameInternal": func() (*Function, error) { return r.getByNameInternal(ctx, "anchat", "rpc-router") },
		"List": func() (*Function, error) {
			fns, err := r.List(ctx, "anchat")
			return firstOf(fns, err)
		},
		"ListVersions": func() (*Function, error) {
			fns, err := r.ListVersions(ctx, "anchat", "rpc-router")
			return firstOf(fns, err)
		},
	}
	for name, get := range read {
		fn, err := get()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if fn.WSAuth != WSAuthCapability {
			t.Errorf("%s returned ws_auth %q", name, fn.WSAuth)
		}
	}
}

func firstOf(fns []*Function, err error) (*Function, error) {
	if err != nil {
		return nil, err
	}
	if len(fns) == 0 {
		return nil, errors.New("no function listed")
	}
	return fns[0], nil
}

func TestRegistry_Register_refusesAnUnknownOrInternalWSAuth(t *testing.T) {
	r := &Registry{}
	for name, def := range map[string]*FunctionDefinition{
		"a misspelt ws_auth":              {Name: "f", Namespace: "ns", WSAuth: "capabilty"},
		"an internal capability function": {Name: "f", Namespace: "ns", WSAuth: WSAuthCapability, IsInternal: true},
	} {
		var verr *ValidationError
		if _, err := r.Register(context.Background(), def, []byte{0}); !errors.As(err, &verr) || verr.Field != "ws_auth" {
			t.Errorf("%s: Register = %v, want a ws_auth validation error", name, err)
		}
	}
}

func TestValidateWSAuth(t *testing.T) {
	for _, ok := range []string{"", WSAuthCapability} {
		if err := ValidateWSAuth(ok); err != nil {
			t.Errorf("ValidateWSAuth(%q) = %v", ok, err)
		}
	}
	var verr *ValidationError
	if err := ValidateWSAuth("capabilty"); !errors.As(err, &verr) || verr.Field != "ws_auth" {
		t.Errorf("a misspelt ws_auth was accepted: %v", err)
	}
}

// The invoker's half of the capability decision: a capability opens only a
// function that accepts one, only over its WebSocket, never an internal one,
// and a caller without one is decided as before.
func TestCapabilityOpens(t *testing.T) {
	grant := &CapabilityGrant{ID: "cap-1"}
	accepting := &Function{Name: "rpc-router", WSAuth: WSAuthCapability}
	ws := &InvokeRequest{TriggerType: TriggerTypeWebSocket, CallerCapability: grant}

	if !capabilityOpens(accepting, ws) {
		t.Error("a capability was refused by the function that accepts it")
	}
	for name, tc := range map[string]struct {
		fn  *Function
		req *InvokeRequest
	}{
		"no capability":                {accepting, &InvokeRequest{TriggerType: TriggerTypeWebSocket}},
		"a nested call":                {accepting, &InvokeRequest{TriggerType: TriggerTypeInternal, CallerCapability: grant}},
		"over HTTP":                    {accepting, &InvokeRequest{TriggerType: TriggerTypeHTTP, CallerCapability: grant}},
		"a function that accepts none": {&Function{Name: "other"}, ws},
		"an internal function":         {&Function{Name: "m", WSAuth: WSAuthCapability, IsInternal: true}, ws},
	} {
		if capabilityOpens(tc.fn, tc.req) {
			t.Errorf("%s: a capability opened it", name)
		}
	}
}
