package serverless

import (
	"context"
	"testing"

	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// feat-422: get_caller_device_id is part of the host ABI; its signature is a
// contract with deployed WASM, so pin it under every module name. It takes
// nothing and returns the packed ptr<<32|len of the device id, exactly as
// get_caller_jwt_subject does for the account.
func TestEngine_HostModule_GetCallerDeviceIDExported(t *testing.T) {
	engine, err := NewEngine(nil, NewMockRegistry(), NewMockHostServices(), zap.NewNop())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = engine.runtime.Close(context.Background()) }()

	for _, name := range []string{"env", "host", "orama"} {
		fns := engine.runtime.Module(name).ExportedFunctionDefinitions()
		def, ok := fns["get_caller_device_id"]
		if !ok {
			t.Fatalf("%s.get_caller_device_id is not exported", name)
		}
		subject := fns["get_caller_jwt_subject"]
		if len(def.ParamTypes()) != 0 {
			t.Errorf("%s.get_caller_device_id params = %v, want none", name, def.ParamTypes())
		}
		results := def.ResultTypes()
		if len(results) != 1 || results[0] != api.ValueTypeI64 {
			t.Errorf("%s.get_caller_device_id results = %v, want (i64 ptr<<32|len)", name, results)
		}
		if subject != nil && len(subject.ResultTypes()) == 1 && subject.ResultTypes()[0] != results[0] {
			t.Errorf("%s.get_caller_device_id does not return what get_caller_jwt_subject does", name)
		}
	}
}
