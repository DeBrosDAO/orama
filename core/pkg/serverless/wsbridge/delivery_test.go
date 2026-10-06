package wsbridge

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"
)

func decodeDelivery(t *testing.T, frame []byte) Delivery {
	t.Helper()
	var d Delivery
	if err := json.Unmarshal(frame, &d); err != nil {
		t.Fatalf("frame %q is not a Delivery: %v", frame, err)
	}
	return d
}

func TestForward_opted_in_client_gets_the_platform_stamped_topic(t *testing.T) {
	ps, ws := newFakePubSub(), newFakeWS()
	b := New(ps, ws, zap.NewNop())
	b.SetClientNamespace("c1", "ns")
	b.SetClientDeliveryEnvelope("c1")
	if err := b.Add(context.Background(), "ns", "c1", "chat.general"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// The publisher lies about the topic inside its own payload.
	payload := []byte(`{"topic":"billing.invoices","body":"hi"}`)
	ps.deliver("chat.general", payload)

	got := ws.sentTo("c1")
	if len(got) != 1 {
		t.Fatalf("client received %d frames, want 1", len(got))
	}
	d := decodeDelivery(t, got[0])
	if d.Type != DeliveryEventType || d.Topic != "chat.general" || string(d.DataB64) != string(payload) {
		t.Errorf("delivery = %+v; want type %q, topic chat.general, the payload untouched", d, DeliveryEventType)
	}
}

func TestForward_client_that_did_not_opt_in_keeps_the_raw_bytes(t *testing.T) {
	ps, ws := newFakePubSub(), newFakeWS()
	b := New(ps, ws, zap.NewNop())
	b.SetClientNamespace("legacy", "ns")
	b.SetClientNamespace("stamped", "ns")
	b.SetClientDeliveryEnvelope("stamped")
	for _, c := range []string{"legacy", "stamped"} {
		if err := b.Add(context.Background(), "ns", c, "t"); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	ps.deliver("t", []byte("raw"))

	if got := ws.sentTo("legacy"); len(got) != 1 || string(got[0]) != "raw" {
		t.Errorf("legacy client received %q, want the raw bytes", got)
	}
	if got := ws.sentTo("stamped"); len(got) != 1 || decodeDelivery(t, got[0]).Topic != "t" {
		t.Errorf("stamped client received %q, want a Delivery frame", got)
	}
}

func TestForward_binary_and_empty_payloads_survive_the_envelope(t *testing.T) {
	for name, payload := range map[string][]byte{
		"binary": {0x00, 0xff, 0x10},
		"empty":  {},
		"nil":    nil,
	} {
		t.Run(name, func(t *testing.T) {
			ps, ws := newFakePubSub(), newFakeWS()
			b := New(ps, ws, zap.NewNop())
			b.SetClientNamespace("c1", "ns")
			b.SetClientDeliveryEnvelope("c1")
			_ = b.Add(context.Background(), "ns", "c1", "t")

			ps.deliver("t", payload)

			got := ws.sentTo("c1")
			if len(got) != 1 || string(decodeDelivery(t, got[0]).DataB64) != string(payload) {
				t.Errorf("frames %q; want one carrying %q", got, payload)
			}
		})
	}
}

func TestRemoveClient_forgets_the_opt_in(t *testing.T) {
	b := New(newFakePubSub(), newFakeWS(), zap.NewNop())
	b.SetClientNamespace("c1", "ns")
	b.SetClientDeliveryEnvelope("c1")

	b.RemoveClient(context.Background(), "c1")

	if b.wantsEnvelope("c1") {
		t.Error("a removed client is still marked as opted in; a reused id would inherit it")
	}
}
