package serverless

import "testing"

// The route policy and the dispatcher read a /v1/functions/ path with this one
// parser; a path means the same operation to both.
func TestSplitFunctionPath(t *testing.T) {
	for path, want := range map[string][2]string{
		"/v1/functions/rpc/ws":              {"rpc", "ws"},
		"/v1/functions/rpc@2/ws":            {"rpc@2", "ws"},
		"/v1/functions/rpc":                 {"rpc", ""},
		"/v1/functions/rpc/triggers/ws":     {"rpc", "triggers/ws"},
		"/v1/functions/secrets/ws":          {"secrets", "ws"},
		"/v1/functions/":                    {"", ""},
		"/v1/functions/rpc/triggers/invoke": {"rpc", "triggers/invoke"},
	} {
		if name, action := SplitFunctionPath(path); name != want[0] || action != want[1] {
			t.Errorf("SplitFunctionPath(%q) = %q, %q; want %q, %q", path, name, action, want[0], want[1])
		}
	}
}

func TestIsFunctionAction(t *testing.T) {
	if !IsFunctionAction("/v1/functions/rpc/ws", "ws") || !IsFunctionAction("/v1/functions/rpc@3/invoke", "invoke") {
		t.Error("a function's own action was not recognised")
	}
	for _, path := range []string{
		"/v1/functions/rpc/triggers/ws", "/v1/functions/secrets/ws", "/v1/functions//ws", "/v1/pubsub/ws", "/v1/functions/rpc",
	} {
		if IsFunctionAction(path, "ws") {
			t.Errorf("%s was taken for a function's ws", path)
		}
	}
}
