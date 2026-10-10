package hostfunctions

import (
	"context"
	"errors"

	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// topicNotFoundReason is the per-result reason a push_send_topic caller sees
// when the topic is not registered in its namespace or has expired.
const topicNotFoundReason = "TopicNotFound"

// PushSendTopic implements serverless.HostServices.PushSendTopic (FEAT-265).
//
// It delivers to the device registered under topicID in the invocation's
// namespace, through the same provider dispatch as PushSendV2, and returns the
// same JSON envelope. The namespace comes from the invocation context, so a
// function reaches only its own namespace's topics.
//
// A topic that is not registered, or has expired, is not a Go error: the
// envelope reports ok=false, devices_attempted=0 and one result with reason
// "TopicNotFound" and unregistered=true, so the caller can stop addressing it.
// The Go error is only for setup and validation failures (bad topic id,
// invalid JSON, no namespace, store failures).
func (h *HostFunctions) PushSendTopic(ctx context.Context, topicID string, msgJSON []byte) ([]byte, error) {
	const fn = "push_send_topic"
	if h.pushManager == nil {
		// Topics are served only by the per-namespace Manager, which every
		// gateway with push has; without it push is not configured here.
		return []byte(pushNotConfiguredEnvelope), nil
	}
	if err := push.ValidateTopicID(topicID); err != nil {
		return nil, &serverless.HostFunctionError{Function: fn, Cause: err}
	}
	msg, err := decodePushMessage(fn, msgJSON)
	if err != nil {
		return nil, err
	}
	namespace, err := h.pushNamespace(ctx, fn)
	if err != nil {
		return nil, err
	}

	result, err := h.pushManager.SendToTopicDetailed(ctx, namespace, topicID, msg)
	switch {
	case errors.Is(err, push.ErrPushNotConfigured):
		return []byte(pushNotConfiguredEnvelope), nil
	case errors.Is(err, push.ErrTopicNotFound):
		result = &push.SendDetailedResult{
			Results: []push.DeviceSendResult{{
				Reason:       topicNotFoundReason,
				Message:      push.ErrTopicNotFound.Error(),
				Unregistered: true,
			}},
		}
	case err != nil:
		return nil, &serverless.HostFunctionError{Function: fn, Cause: err}
	}
	return marshalPushResult(fn, result)
}
