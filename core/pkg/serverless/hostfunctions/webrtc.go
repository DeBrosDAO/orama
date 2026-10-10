package hostfunctions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// WebRTC admission and moderation (bugboard #726): a namespace that requires
// admission lets a user into a room only after one of its functions admitted
// them; a function can later remove or mute them. The admissions live in the
// namespace's own database and the kick and mute reach the SFU that holds the
// room (pkg/gateway/handlers/webrtc/control.go).

// maxWebRTCCallsPerInvocation caps how many webrtc_admit, webrtc_kick and
// webrtc_mute calls one function invocation (or persistent WS frame) may make.
// Like maxPublishesPerInvocation it is a safety bound and not a normal-path
// limit: each call writes the namespace's database and, for a kick or a mute,
// calls every SFU of the namespace, and the WASM runtime has no fuel metering,
// so a runaway loop would otherwise hammer both. A real moderation action
// touches a handful of users.
const maxWebRTCCallsPerInvocation = 100

var (
	errNoWebRTC = errors.New("this gateway cannot administer WebRTC rooms: WebRTC is not set up here (is it enabled for the namespace?)")

	// ErrWebRTCBudgetExceeded is the cause of a webrtc_* host call refused
	// because its invocation already made maxWebRTCCallsPerInvocation of them.
	ErrWebRTCBudgetExceeded = fmt.Errorf("webrtc call budget exceeded (max %d per invocation)", maxWebRTCCallsPerInvocation)
)

// SetWebRTCController wires what the WebRTC host calls act through. Called once
// at gateway start, before any function runs.
func (h *HostFunctions) SetWebRTCController(c serverless.WebRTCController) {
	h.webrtc = c
}

// webrtcNamespace returns the namespace of the calling function, and the
// controller to act through. A function acts on its own namespace only.
func (h *HostFunctions) webrtcNamespace(ctx context.Context, fn string) (string, serverless.WebRTCController, error) {
	cur := h.currentInvocationContext(ctx)
	if cur == nil || cur.Namespace == "" {
		return "", nil, &serverless.HostFunctionError{Function: fn, Cause: errNoInvocation}
	}
	if h.webrtc == nil {
		return "", nil, &serverless.HostFunctionError{Function: fn, Cause: errNoWebRTC}
	}
	if n := serverless.AddWebRTCCount(ctx); n > maxWebRTCCallsPerInvocation {
		return "", nil, &serverless.HostFunctionError{Function: fn, Cause: ErrWebRTCBudgetExceeded}
	}
	return cur.Namespace, h.webrtc, nil
}

// WebRTCAdmit admits user to room for ttl, from device (from any device when
// empty). It returns the admission as JSON
// {"room","user_id","device_id","expires_at"}.
func (h *HostFunctions) WebRTCAdmit(ctx context.Context, room, user, device string, ttl time.Duration) (string, error) {
	ns, c, err := h.webrtcNamespace(ctx, "webrtc_admit")
	if err != nil {
		return "", err
	}
	expires, err := c.Admit(ctx, ns, room, user, device, ttl)
	if err != nil {
		return "", &serverless.HostFunctionError{Function: "webrtc_admit", Cause: err}
	}
	out, err := json.Marshal(struct {
		Room      string `json:"room"`
		UserID    string `json:"user_id"`
		DeviceID  string `json:"device_id"`
		ExpiresAt int64  `json:"expires_at"`
	}{room, user, device, expires.Unix()})
	if err != nil {
		return "", &serverless.HostFunctionError{Function: "webrtc_admit", Cause: fmt.Errorf("failed to encode the admission: %w", err)}
	}
	return string(out), nil
}

// WebRTCKick revokes user's admissions to room and closes their connection.
func (h *HostFunctions) WebRTCKick(ctx context.Context, room, user string) error {
	ns, c, err := h.webrtcNamespace(ctx, "webrtc_kick")
	if err != nil {
		return err
	}
	if err := c.Kick(ctx, ns, room, user); err != nil {
		return &serverless.HostFunctionError{Function: "webrtc_kick", Cause: err}
	}
	return nil
}

// WebRTCMute stops (or resumes) the forwarding of user's audio in room.
func (h *HostFunctions) WebRTCMute(ctx context.Context, room, user string, muted bool) error {
	ns, c, err := h.webrtcNamespace(ctx, "webrtc_mute")
	if err != nil {
		return err
	}
	if err := c.Mute(ctx, ns, room, user, muted); err != nil {
		return &serverless.HostFunctionError{Function: "webrtc_mute", Cause: err}
	}
	return nil
}
