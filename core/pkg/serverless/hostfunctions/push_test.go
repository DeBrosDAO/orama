package hostfunctions

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// decodePushMessage is shared by push_send, push_send_v2 and push_send_topic,
// so these pin the argument contract all three depend on.

func TestDecodePushMessage_mapsEveryField(t *testing.T) {
	msg, err := decodePushMessage("push_send_v2", []byte(`{
		"title":"t","body":"b","channel":"calls","priority":"high","badge":3,
		"sound":"ring","data":{"k":"v"},"target_provider":"apns",
		"exclude_provider":"expo","message_id":"m1"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg.Title != "t" || msg.Body != "b" || msg.Channel != "calls" || msg.Priority != push.PriorityHigh ||
		msg.Badge != 3 || msg.Sound != "ring" || msg.Data["k"] != "v" || msg.TargetProvider != "apns" ||
		msg.ExcludeProvider != "expo" || msg.MessageID != "m1" {
		t.Errorf("decoded %+v", msg)
	}
}

func TestDecodePushMessage_priorityDefaultsToNormal(t *testing.T) {
	for _, p := range []string{"", "normal", "urgent"} {
		msg, err := decodePushMessage("push_send", []byte(`{"priority":"`+p+`"}`))
		if err != nil {
			t.Fatalf("decode %q: %v", p, err)
		}
		if msg.Priority != push.PriorityNormal {
			t.Errorf("priority %q -> %q, want normal", p, msg.Priority)
		}
	}
}

func TestDecodePushMessage_rejectsOversizedAndInvalid(t *testing.T) {
	oversized := []byte(`{"body":"` + strings.Repeat("x", MaxPushSendArgsBytes) + `"}`)
	for name, raw := range map[string][]byte{
		"oversized": oversized,
		"invalid":   []byte(`{"title":`),
		"empty":     nil,
	} {
		_, err := decodePushMessage("push_send_topic", raw)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), "push_send_topic") {
			t.Errorf("%s: error does not name the host function: %v", name, err)
		}
	}
}

func TestPushNamespace_comesFromTheInvocation(t *testing.T) {
	h := &HostFunctions{}
	ns, err := h.pushNamespace(invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"}), "push_send")
	if err != nil || ns != "ns-a" {
		t.Errorf("pushNamespace = %q, %v; want ns-a", ns, err)
	}
	if _, err := h.pushNamespace(context.Background(), "push_send"); err == nil {
		t.Error("no invocation context resolved a namespace")
	}
	if _, err := h.pushNamespace(invocationCtx(&serverless.InvocationContext{}), "push_send"); err == nil {
		t.Error("an invocation with an empty namespace resolved one")
	}
}

// oneUserDevices is a PushDeviceStore holding one device for "user-1".
type oneUserDevices struct{ push.PushDeviceStore }

func (oneUserDevices) ListForUser(_ context.Context, ns, userID string) ([]push.PushDevice, error) {
	if userID != "user-1" {
		return nil, nil
	}
	return []push.PushDevice{{Namespace: ns, UserID: userID, DeviceID: "d1", Provider: "apns", Token: "tok-1"}}, nil
}

func accountManager(provider push.PushProvider) *push.Manager {
	factory := func(context.Context, push.Config) []push.PushProvider {
		if provider == nil {
			return nil
		}
		return []push.PushProvider{provider}
	}
	return push.NewManager(oneUserDevices{}, nil, push.Defaults{}, factory, nil)
}

func TestPushSendV2_deliversThroughTheManager(t *testing.T) {
	provider := &recordingProvider{}
	h := &HostFunctions{pushManager: accountManager(provider)}
	raw, err := h.PushSendV2(invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"}), "user-1", []byte(`{"title":"hi"}`))
	if err != nil {
		t.Fatalf("PushSendV2: %v", err)
	}
	if res := decodeEnvelope(t, raw); !res.Ok || res.DevicesSucceeded != 1 {
		t.Errorf("envelope = %s, want one delivery", raw)
	}
	if len(provider.tokens) != 1 || provider.tokens[0] != "tok-1" {
		t.Errorf("provider tokens = %v", provider.tokens)
	}
}

// With a Manager but no provider for the namespace, both account calls stay
// silent no-ops, as they were before the shared helpers.
func TestPushSend_namespaceWithoutProviderIsNoOp(t *testing.T) {
	h := &HostFunctions{pushManager: accountManager(nil)}
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"})
	if err := h.PushSend(ctx, "user-1", []byte(`{"title":"hi"}`)); err != nil {
		t.Errorf("PushSend: %v, want the silent no-op", err)
	}
	raw, err := h.PushSendV2(ctx, "user-1", []byte(`{"title":"hi"}`))
	if err != nil || string(raw) != pushNotConfiguredEnvelope {
		t.Errorf("PushSendV2 = %s, %v; want the not-configured envelope", raw, err)
	}
}

func TestPushSend_emptyUserIDIsAnError(t *testing.T) {
	h := &HostFunctions{pushManager: accountManager(&recordingProvider{})}
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"})
	if err := h.PushSend(ctx, "", []byte(`{}`)); err == nil {
		t.Error("PushSend accepted an empty user id")
	}
	if _, err := h.PushSendV2(ctx, "", []byte(`{}`)); err == nil {
		t.Error("PushSendV2 accepted an empty user id")
	}
}
