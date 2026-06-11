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
//
// TargetProvider (bugboard #408) is the dispatcher-side device filter.
// Empty = fan out to every registered device for the user (back-compat
// default). Set to a provider name ("apns", "apns_voip", "ntfy",
// "expo") = dispatcher only attempts devices whose Provider field
// matches. Required by call-push-handler (set to "apns_voip") to avoid
// CallKit-ring on every chat message, and by message-push-handler (set
// to "apns") so VoIP-only pushes don't show as a silent alert.
type PushSendArgs struct {
	Title          string                 `json:"title,omitempty"`
	Body           string                 `json:"body,omitempty"`
	Channel        string                 `json:"channel,omitempty"`
	Priority       string                 `json:"priority,omitempty"` // "high" | "normal" | ""
	Badge          int                    `json:"badge,omitempty"`
	Sound          string                 `json:"sound,omitempty"`
	Data           map[string]interface{} `json:"data,omitempty"`
	TargetProvider string                 `json:"target_provider,omitempty"`
	// ExcludeProvider is the inverse of TargetProvider — drops devices
	// whose provider equals this value. Cleaner semantic than listing
	// every included provider for the "fan out to everyone EXCEPT VoIP"
	// pattern (chat-handler wants ntfy+apns+expo but never apns_voip).
	// If both are set, TargetProvider wins. Bugboard feat-10.
	ExcludeProvider string `json:"exclude_provider,omitempty"`
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
// If push is not configured on this gateway (no dispatcher AND no
// manager), this returns nil (silent no-op) so functions remain portable
// across environments.
//
// When the manager is present (bug #220 follow-up), it routes through
// per-namespace config — tenants who have set their own ntfy / expo via
// PUT /v1/push/config get their providers; namespaces with no config
// fall back to the gateway YAML defaults via the manager's resolution.
func (h *HostFunctions) PushSend(ctx context.Context, userID string, msgJSON []byte) error {
	if h.pushManager == nil && h.pushDispatcher == nil {
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
	// ctx-attached invCtx wins over singleton; see invocation_context.go.
	var namespace string
	if cur := h.currentInvocationContext(ctx); cur != nil {
		namespace = cur.Namespace
	}

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
		Title:           args.Title,
		Body:            args.Body,
		Channel:         args.Channel,
		Priority:        priority,
		Badge:           args.Badge,
		Sound:           args.Sound,
		Data:            args.Data,
		TargetProvider:  args.TargetProvider,
		ExcludeProvider: args.ExcludeProvider,
	}

	// Route through Manager when present so per-namespace push config
	// (set via PUT /v1/push/config) takes effect. The Manager itself
	// falls back to YAML defaults if the namespace hasn't set anything.
	var sendErr error
	if h.pushManager != nil {
		sendErr = h.pushManager.SendToUser(ctx, namespace, userID, msg)
		// ErrPushNotConfigured here would mean: no per-namespace config
		// AND no YAML defaults. Treat as silent no-op for portability —
		// same contract as "no dispatcher at all". Functions can't depend
		// on push being available.
		if sendErr != nil && sendErr.Error() == push.ErrPushNotConfigured.Error() {
			return nil
		}
	} else {
		sendErr = h.pushDispatcher.SendToUser(ctx, namespace, userID, msg)
	}
	if sendErr != nil {
		return &serverless.HostFunctionError{Function: "push_send", Cause: sendErr}
	}
	return nil
}

// PushSendV2 implements serverless.HostServices.PushSendV2 — the
// rich-result version of PushSend. Returns a JSON envelope describing
// every device the dispatcher attempted, with HTTP status / reason /
// unregistered-flag per device, so WASM callers can react granularly
// (delete stale tokens on Unregistered, retry on 5xx, etc.).
//
// Bugboard #348: PushSend's binary success/fail return discarded
// Apple's HTTP status — silent-drop bugs (Apple 200 + no delivery,
// empty-content payloads, etc.) all looked like success. PushSendV2
// surfaces the full per-device truth.
//
// The Go error return is ONLY for setup/validation failures (no
// manager wired, no namespace in context, invalid JSON). Per-device
// failures go into the JSON `results[]` array.
func (h *HostFunctions) PushSendV2(ctx context.Context, userID string, msgJSON []byte) ([]byte, error) {
	if h.pushManager == nil && h.pushDispatcher == nil {
		// Silent no-op shape: empty result envelope. WASM caller sees
		// ok=true, attempted=0, succeeded=0. Same semantic as legacy
		// PushSend's silent no-op for portability across environments.
		return []byte(`{"ok":true,"devices_attempted":0,"devices_succeeded":0,"results":[]}`), nil
	}
	if userID == "" {
		return nil, &serverless.HostFunctionError{
			Function: "push_send_v2",
			Cause:    fmt.Errorf("user_id required"),
		}
	}
	if len(msgJSON) > MaxPushSendArgsBytes {
		return nil, &serverless.HostFunctionError{
			Function: "push_send_v2",
			Cause:    fmt.Errorf("msg too large: max %d bytes", MaxPushSendArgsBytes),
		}
	}

	var args PushSendArgs
	if err := json.Unmarshal(msgJSON, &args); err != nil {
		return nil, &serverless.HostFunctionError{
			Function: "push_send_v2",
			Cause:    fmt.Errorf("invalid json: %w", err),
		}
	}

	// Same namespace resolution as PushSend — invCtx-trusted, never the
	// WASM caller's claim.
	var namespace string
	if cur := h.currentInvocationContext(ctx); cur != nil {
		namespace = cur.Namespace
	}
	if namespace == "" {
		return nil, &serverless.HostFunctionError{
			Function: "push_send_v2",
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
		Title:           args.Title,
		Body:            args.Body,
		Channel:         args.Channel,
		Priority:        priority,
		Badge:           args.Badge,
		Sound:           args.Sound,
		Data:            args.Data,
		TargetProvider:  args.TargetProvider,
		ExcludeProvider: args.ExcludeProvider,
	}

	// Prefer the Manager (per-namespace config); fall back to the legacy
	// dispatcher. Same precedence as PushSend so v1 and v2 stay
	// behaviorally equivalent at the dispatch level.
	var (
		result *push.SendDetailedResult
		err    error
	)
	if h.pushManager != nil {
		result, err = h.pushManager.SendToUserDetailed(ctx, namespace, userID, msg)
		// ErrPushNotConfigured = no per-namespace config AND no YAML
		// defaults. Treat as silent no-op (same shape as legacy PushSend).
		if err != nil && err.Error() == push.ErrPushNotConfigured.Error() {
			return []byte(`{"ok":true,"devices_attempted":0,"devices_succeeded":0,"results":[]}`), nil
		}
	} else {
		result, err = h.pushDispatcher.SendToUserDetailed(ctx, namespace, userID, msg)
	}
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "push_send_v2", Cause: err}
	}

	out, mErr := json.Marshal(result)
	if mErr != nil {
		return nil, &serverless.HostFunctionError{
			Function: "push_send_v2",
			Cause:    fmt.Errorf("marshal result: %w", mErr),
		}
	}
	return out, nil
}
