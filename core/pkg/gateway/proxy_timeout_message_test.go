package gateway

import (
	"strings"
	"testing"
	"time"
)

// bugboard #414: a storage read that timed out was told to edit function.yaml.
func TestProxyTimeoutMessage(t *testing.T) {
	for _, path := range []string{"/v1/invoke/anchat/rpc-router", "/v1/functions/rpc-router/invoke", "/v1/functions/rpc-router/ws"} {
		if msg := proxyTimeoutMessage(path, 30*time.Second); !strings.Contains(msg, "function.yaml") {
			t.Errorf("%s: %q does not tell a function caller how to raise the timeout", path, msg)
		}
	}
	for _, path := range []string{"/v1/storage/get/Qm", "/v1/cache/get", "/v1/functions/rpc-router"} {
		msg := proxyTimeoutMessage(path, 30*time.Second)
		if strings.Contains(msg, "function.yaml") {
			t.Errorf("%s: %q points a non-function caller at function.yaml", path, msg)
		}
		if !strings.Contains(msg, "30s") {
			t.Errorf("%s: %q does not state the budget", path, msg)
		}
	}
}
