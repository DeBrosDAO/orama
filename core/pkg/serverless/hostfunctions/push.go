package hostfunctions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// PushSendArgs is the JSON payload format the WASM caller marshals into
// the `msgJSON` argument of PushSend. Mirrors push.PushMessage minus the
// device-token (which is filled in per-device by the dispatcher).
type PushSendArgs struct {
	Title    string                 `json:"title,omitempty"`
	Body     string                 `json:"body,omitempty"`
	Channel  string                 `json:"channel,omitempty"`
	Priority string                 `json:"priority,omitempty"` // "high" | "normal" | ""
	Badge    int                    `json:"badge,omitempty"`
	Sound    string                 `json:"sound,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// MaxPushSendArgsBytes caps the JSON arg size to a few KB. Push payloads
// are small by construction (APNs caps at 4KB, ntfy/Expo similar).
const MaxPushSendArgsBytes = 16 * 1024

// PushSend implements serverless.HostServices.PushSend.
//
// Sends a push notification to all devices the user has registered in the
// function's namespace. The caller can only target users in their own
// namespace — the dispatcher reads the namespace from the invocation
// context (set by the engine before invoking).
//
// If push is not configured on this gateway (no dispatcher), this returns
// nil (silent no-op) so functions remain portable across environments.
func (h *HostFunctions) PushSend(ctx context.Context, userID string, msgJSON []byte) error {
	if h.pushDispatcher == nil {
		// Silent no-op — push isn't configured on this gateway.
		return nil
	}
	if userID == "" {
		return &serverless.HostFunctionError{
			Function: "push_send",
			Cause:    fmt.Errorf("user_id required"),
		}
	}
	if len(msgJSON) > MaxPushSendArgsBytes {
		return &serverless.HostFunctionError{
			Function: "push_send",
			Cause:    fmt.Errorf("msg too large: max %d bytes", MaxPushSendArgsBytes),
		}
	}

	var args PushSendArgs
	if err := json.Unmarshal(msgJSON, &args); err != nil {
		return &serverless.HostFunctionError{
			Function: "push_send",
			Cause:    fmt.Errorf("invalid json: %w", err),
		}
	}

	// Resolve namespace from the current invocation context. A function
	// can NEVER push to another namespace's users — the namespace is
	// trusted server-side, not from the WASM input.
	h.invCtxLock.RLock()
	var namespace string
	if h.invCtx != nil {
		namespace = h.invCtx.Namespace
	}
	h.invCtxLock.RUnlock()

	if namespace == "" {
		return &serverless.HostFunctionError{
			Function: "push_send",
			Cause:    fmt.Errorf("no namespace in invocation context"),
		}
	}

	priority := push.PriorityNormal
	switch args.Priority {
	case "high":
		priority = push.PriorityHigh
	case "normal", "":
		priority = push.PriorityNormal
	}

	msg := push.PushMessage{
		Title:    args.Title,
		Body:     args.Body,
		Channel:  args.Channel,
		Priority: priority,
		Badge:    args.Badge,
		Sound:    args.Sound,
		Data:     args.Data,
	}

	if err := h.pushDispatcher.SendToUser(ctx, namespace, userID, msg); err != nil {
		return &serverless.HostFunctionError{Function: "push_send", Cause: err}
	}
	return nil
}
