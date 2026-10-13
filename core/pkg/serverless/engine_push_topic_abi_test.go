package serverless

import (
	"context"
	"testing"

	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// FEAT-265: push_send_topic is part of the host ABI. Its signature is a
// contract with deployed WASM — the same shape as push_send_v2 — so pin it.
func TestEngine_HostModule_PushSendTopicExported(t *testing.T) {
	engine, err := NewEngine(nil, NewMockRegistry(), NewMockHostServices(), zap.NewNop())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = engine.runtime.Close(context.Background()) }()

	i32 := api.ValueTypeI32
	for _, name := range []string{"env", "host", "orama"} {
		defs := engine.runtime.Module(name).ExportedFunctionDefinitions()
		def, ok := defs["push_send_topic"]
		if !ok {
			t.Fatalf("%s.push_send_topic is not exported", name)
		}
		params, results := def.ParamTypes(), def.ResultTypes()
		if len(params) != 4 || params[0] != i32 || params[1] != i32 || params[2] != i32 || params[3] != i32 {
			t.Errorf("%s.push_send_topic params = %v, want (i32 topic_ptr, i32 topic_len, i32 msg_ptr, i32 msg_len)", name, params)
		}
		if len(results) != 1 || results[0] != api.ValueTypeI64 {
			t.Errorf("%s.push_send_topic results = %v, want (i64 packed ptr<<32|len)", name, results)
		}

		// Same result convention as push_send_v2.
		v2 := defs["push_send_v2"]
		if v2 == nil || len(v2.ResultTypes()) != 1 || v2.ResultTypes()[0] != results[0] {
			t.Errorf("%s.push_send_topic does not share push_send_v2's result convention", name)
		}
	}
}
