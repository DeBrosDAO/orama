package serverless

import (
	"context"
	"slices"
	"testing"

	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// anon_fetch is the canonical export; anyone_fetch is its deprecated alias and
// must stay, because deployed AnChat functions import orama.anyone_fetch and a
// missing import fails module instantiation. Both are a contract with deployed
// WASM, so pin the names and the shared signature in every module alias.
func TestEngine_HostModule_AnonFetchAndDeprecatedAliasExported(t *testing.T) {
	engine, err := NewEngine(nil, NewMockRegistry(), NewMockHostServices(), zap.NewNop())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = engine.runtime.Close(context.Background()) }()

	if AnonFetchExport != "anon_fetch" || AnyoneFetchDeprecatedExport != "anyone_fetch" {
		t.Fatalf("export names changed: %q, %q", AnonFetchExport, AnyoneFetchDeprecatedExport)
	}
	wantParams := slices.Repeat([]api.ValueType{api.ValueTypeI32}, 8)
	wantResults := []api.ValueType{api.ValueTypeI64}

	for _, module := range []string{"env", "host", "orama"} {
		defs := engine.runtime.Module(module).ExportedFunctionDefinitions()
		httpFetch, ok := defs["http_fetch"]
		if !ok {
			t.Fatalf("%s.http_fetch is not exported", module)
		}
		for _, name := range []string{AnonFetchExport, AnyoneFetchDeprecatedExport} {
			def, ok := defs[name]
			if !ok {
				t.Fatalf("%s.%s is not exported", module, name)
			}
			if !slices.Equal(def.ParamTypes(), wantParams) || !slices.Equal(def.ResultTypes(), wantResults) {
				t.Errorf("%s.%s signature = %v -> %v, want %v -> %v",
					module, name, def.ParamTypes(), def.ResultTypes(), wantParams, wantResults)
			}
			// A function can swap http_fetch for anon_fetch without touching
			// its ABI glue.
			if !slices.Equal(def.ParamTypes(), httpFetch.ParamTypes()) || !slices.Equal(def.ResultTypes(), httpFetch.ResultTypes()) {
				t.Errorf("%s.%s signature differs from http_fetch", module, name)
			}
		}
	}
}
