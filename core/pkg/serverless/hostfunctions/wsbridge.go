package hostfunctions

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// WSPubSubBridge wires a WS client to a PubSub topic in the function's
// own namespace. Returns an error if:
//
//   - bridge is not configured on this gateway
//   - the function has no namespace in its invocation context
//   - the client's namespace (set at WS upgrade) doesn't match the function's
//   - the bridge itself returns an error (e.g. per-client topic cap exceeded)
//
// Idempotent: re-bridging the same (client, topic) is a no-op.
func (h *HostFunctions) WSPubSubBridge(ctx context.Context, clientID, topic string) error {
	if h.wsBridge == nil {
		return &serverless.HostFunctionError{
			Function: "ws_pubsub_bridge",
			Cause:    fmt.Errorf("bridge not configured on this gateway"),
		}
	}
	fnNS := h.namespaceFromCtx(ctx)
	if fnNS == "" {
		return &serverless.HostFunctionError{
			Function: "ws_pubsub_bridge",
			Cause:    fmt.Errorf("no namespace in invocation context"),
		}
	}
	cliNS, ok := h.wsBridge.GetClientNamespace(clientID)
	if !ok {
		return &serverless.HostFunctionError{
			Function: "ws_pubsub_bridge",
			Cause:    fmt.Errorf("unknown client_id %q", clientID),
		}
	}
	if cliNS != fnNS {
		return &serverless.HostFunctionError{
			Function: "ws_pubsub_bridge",
			Cause:    fmt.Errorf("namespace mismatch: function=%q client=%q", fnNS, cliNS),
		}
	}
	if err := h.wsBridge.Add(ctx, fnNS, clientID, topic); err != nil {
		return &serverless.HostFunctionError{Function: "ws_pubsub_bridge", Cause: err}
	}
	return nil
}

// WSPubSubUnbridge removes a (client, topic) bridge. Idempotent.
func (h *HostFunctions) WSPubSubUnbridge(ctx context.Context, clientID, topic string) error {
	if h.wsBridge == nil {
		return &serverless.HostFunctionError{
			Function: "ws_pubsub_unbridge",
			Cause:    fmt.Errorf("bridge not configured on this gateway"),
		}
	}
	fnNS := h.namespaceFromCtx(ctx)
	if fnNS == "" {
		return &serverless.HostFunctionError{
			Function: "ws_pubsub_unbridge",
			Cause:    fmt.Errorf("no namespace in invocation context"),
		}
	}
	if err := h.wsBridge.Remove(ctx, fnNS, clientID, topic); err != nil {
		return &serverless.HostFunctionError{Function: "ws_pubsub_unbridge", Cause: err}
	}
	return nil
}

// namespaceFromCtx returns the current invocation's namespace, or "" if
// no context is set. ctx-attached invCtx wins over the singleton (see
// invocation_context.go).
func (h *HostFunctions) namespaceFromCtx(ctx context.Context) string {
	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return ""
	}
	return cur.Namespace
}
